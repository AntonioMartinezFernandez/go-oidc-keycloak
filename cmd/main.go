package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer   = "http://localhost:8080/realms/myrealm"
	audience = "my-api"
	jwksURL  = issuer + "/protocol/openid-connect/certs"

	// Authorization Code flow endpoints.
	authorizeURL = issuer + "/protocol/openid-connect/auth"
	tokenURL     = issuer + "/protocol/openid-connect/token"
	registerURL  = issuer + "/protocol/openid-connect/registrations"

	// my-api is configured as a public client (Direct access grants ON,
	// Client authentication OFF), so the Authorization Code flow relies on
	// PKCE instead of a client secret.
	clientID    = "my-api"
	redirectURI = "http://localhost:8081/auth/callback"

	sessionCookie  = "session_id"
	stateCookie    = "oauth_state"
	verifierCookie = "oauth_verifier"
)

type UserProfile struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Language string `json:"language"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type KeyStore struct {
	mu     sync.RWMutex
	keys   map[string]*rsa.PublicKey
	client *http.Client
}

func NewKeyStore() *KeyStore {
	return &KeyStore{
		keys: make(map[string]*rsa.PublicKey),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Refresh downloads the current public keys from Keycloak's JWKS endpoint.
func (ks *KeyStore) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		jwksURL,
		nil,
	)
	if err != nil {
		return err
	}

	resp, err := ks.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %s", resp.Status)
	}

	var jwks JWKS

	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS response: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)

	for _, key := range jwks.Keys {
		if key.Kty != "RSA" {
			continue
		}

		publicKey, err := jwkToRSAPublicKey(key)
		if err != nil {
			return fmt.Errorf(
				"failed to parse JWK %q: %w",
				key.Kid,
				err,
			)
		}

		keys[key.Kid] = publicKey
	}

	if len(keys) == 0 {
		return fmt.Errorf("JWKS contains no RSA keys")
	}

	ks.mu.Lock()
	ks.keys = keys
	ks.mu.Unlock()

	return nil
}

// jwkToRSAPublicKey converts an RSA JWK into an *rsa.PublicKey.
func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("invalid modulus: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("invalid exponent: %w", err)
	}

	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, fmt.Errorf("empty RSA modulus or exponent")
	}

	n := new(big.Int).SetBytes(nBytes)

	var e int

	for _, b := range eBytes {
		e = e<<8 | int(b)
	}

	if e == 0 {
		return nil, fmt.Errorf("invalid RSA exponent")
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}

// Get returns the public key associated with the given Key ID.
func (ks *KeyStore) Get(kid string) (*rsa.PublicKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()

	key, ok := ks.keys[kid]

	return key, ok
}

// TokenResponse is Keycloak's response body from the token endpoint.
type TokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	TokenType        string `json:"token_type"`
}

// Session holds what a browser-based login needs between requests.
//
// In a real application this would normally live in a shared store
// (Redis, a database) rather than an in-memory map, so it survives
// restarts and works across multiple API instances.
type Session struct {
	AccessToken string
	ExpiresAt   time.Time
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]Session),
	}
}

func (s *SessionStore) Set(id string, session Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[id] = session
}

func (s *SessionStore) Get(id string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[id]
	if !ok || time.Now().After(session.ExpiresAt) {
		return Session{}, false
	}

	return session, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)
}

type Server struct {
	keyStore   *KeyStore
	sessions   *SessionStore
	httpClient *http.Client

	// In a real application this would normally be a database or another
	// persistent storage mechanism.
	profiles map[string]UserProfile
}

// generateRandomString returns a URL-safe random string with n bytes of
// entropy, used for state, PKCE verifiers, and session IDs.
func generateRandomString(n int) (string, error) {
	b := make([]byte, n)

	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 code_challenge for a given code_verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// startAuthFlow begins an OIDC Authorization Code + PKCE flow against the
// given Keycloak endpoint. It's shared by /login and /signup: both send the
// browser to Keycloak, they just land on different pages there (the login
// form vs the registration form). Either way Keycloak redirects back to
// the same /auth/callback once the user is done.
func (s *Server) startAuthFlow(c *gin.Context, endpoint string) {
	state, err := generateRandomString(24)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed_to_generate_state",
		})
		return
	}

	verifier, err := generateRandomString(32)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed_to_generate_verifier",
		})
		return
	}

	// SameSite=Lax (gin's default secure cookie handling) allows these
	// cookies to survive the top-level redirect to Keycloak and back.
	c.SetSameSite(http.SameSiteLaxMode)

	// Short-lived: they only need to survive the round trip to Keycloak.
	// secure=false because this example runs over plain HTTP on localhost;
	// set it to true once the app is served over HTTPS.
	c.SetCookie(stateCookie, state, 300, "/", "", false, true)
	c.SetCookie(verifierCookie, verifier, 300, "/", "", false, true)

	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("response_type", "code")
	params.Set("scope", "openid")
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("code_challenge", pkceChallenge(verifier))
	params.Set("code_challenge_method", "S256")

	c.Redirect(http.StatusFound, endpoint+"?"+params.Encode())
}

// login sends the browser to Keycloak's login page.
func (s *Server) login(c *gin.Context) {
	s.startAuthFlow(c, authorizeURL)
}

// signup sends the browser to Keycloak's hosted registration page.
//
// This requires "User registration" to be enabled in the realm
// (Realm settings -> Login), and requires my-api's "Valid redirect URIs"
// to include redirectURI.
func (s *Server) signup(c *gin.Context) {
	s.startAuthFlow(c, registerURL)
}

// authCallback is where Keycloak redirects back to after login or signup.
// It validates state, exchanges the authorization code for tokens, and
// starts a browser session.
func (s *Server) authCallback(c *gin.Context) {
	if oauthErr := c.Query("error"); oauthErr != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             oauthErr,
			"error_description": c.Query("error_description"),
		})
		return
	}

	expectedState, err := c.Cookie(stateCookie)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "missing_state_cookie",
		})
		return
	}

	verifier, err := c.Cookie(verifierCookie)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "missing_verifier_cookie",
		})
		return
	}

	// The state/verifier cookies are single-use; clear them either way.
	c.SetCookie(stateCookie, "", -1, "/", "", false, true)
	c.SetCookie(verifierCookie, "", -1, "/", "", false, true)

	if c.Query("state") != expectedState {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "state_mismatch",
		})
		return
	}

	code := c.Query("code")
	if code == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "missing_code",
		})
		return
	}

	tokens, err := s.exchangeCodeForTokens(c.Request.Context(), code, verifier)
	if err != nil {
		log.Printf("token exchange failed: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
			"error": "token_exchange_failed",
		})
		return
	}

	sessionID, err := generateRandomString(24)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "failed_to_create_session",
		})
		return
	}

	s.sessions.Set(sessionID, Session{
		AccessToken: tokens.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
	})

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, sessionID, tokens.ExpiresIn, "/", "", false, true)

	c.Redirect(http.StatusFound, "/api/v1/profile")
}

// exchangeCodeForTokens performs the server-to-server call to Keycloak's
// token endpoint (step 5 of the Authorization Code flow) - the browser is
// never involved in this request.
func (s *Server) exchangeCodeForTokens(
	ctx context.Context,
	code string,
	verifier string,
) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		tokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %s", resp.Status)
	}

	var tokens TokenResponse

	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokens, nil
}

// extractToken pulls a bearer token either from the Authorization header
// (API clients using Direct Access Grants) or from a browser session
// cookie set by authCallback (users who went through /login or /signup).
func (s *Server) extractToken(c *gin.Context) (string, error) {
	if authHeader := c.GetHeader("Authorization"); authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)

		if len(parts) != 2 ||
			!strings.EqualFold(parts[0], "Bearer") ||
			parts[1] == "" {
			return "", fmt.Errorf("invalid_authorization_header")
		}

		return parts[1], nil
	}

	sessionID, err := c.Cookie(sessionCookie)
	if err != nil {
		return "", fmt.Errorf("missing_authorization")
	}

	session, ok := s.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session_expired")
	}

	return session.AccessToken, nil
}

func (s *Server) authenticate(c *gin.Context) {
	rawToken, err := s.extractToken(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": err.Error(),
		})
		return
	}

	token, err := s.parseAndValidateToken(c.Request.Context(), rawToken)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_token",
		})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_claims",
		})
		return
	}

	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "missing_subject",
		})
		return
	}

	// Store the authenticated user's identity in the Gin context.
	c.Set("user_id", sub)

	if username, ok := claims["preferred_username"].(string); ok {
		c.Set("username", username)
	}

	c.Next()
}

func (s *Server) parseAndValidateToken(
	ctx context.Context,
	rawToken string,
) (*jwt.Token, error) {
	// Parse the token once to extract the Key ID (kid).
	unverifiedToken, _, err := new(jwt.Parser).ParseUnverified(
		rawToken,
		jwt.MapClaims{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	kid, ok := unverifiedToken.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, fmt.Errorf("missing kid")
	}

	// Try the cached public keys first.
	publicKey, ok := s.keyStore.Get(kid)

	// If the key is unknown, refresh the JWKS.
	//
	// This handles Keycloak key rotation.
	if !ok {
		if err := s.keyStore.Refresh(ctx); err != nil {
			return nil, fmt.Errorf(
				"failed to refresh JWKS: %w",
				err,
			)
		}

		publicKey, ok = s.keyStore.Get(kid)

		if !ok {
			return nil, fmt.Errorf(
				"unknown key id %q",
				kid,
			)
		}
	}

	// Now perform the actual cryptographic validation.
	token, err := jwt.Parse(
		rawToken,
		func(token *jwt.Token) (any, error) {
			// Explicitly restrict the accepted signing algorithm.
			if token.Method != jwt.SigningMethodRS256 {
				return nil, fmt.Errorf(
					"unexpected signing method: %s",
					token.Method.Alg(),
				)
			}

			return publicKey, nil
		},
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
	)

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("token is invalid")
	}

	return token, nil
}

func (s *Server) profile(c *gin.Context) {
	userIDValue, exists := c.Get("user_id")

	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "user_identity_not_available",
		})
		return
	}

	userID, ok := userIDValue.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "invalid_user_identity",
		})
		return
	}

	profile, exists := s.profiles[userID]

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "profile_not_found",
		})
		return
	}

	c.JSON(http.StatusOK, profile)
}

func main() {
	ctx := context.Background()

	keyStore := NewKeyStore()

	// Load Keycloak's public keys when the application starts.
	if err := keyStore.Refresh(ctx); err != nil {
		log.Fatalf(
			"failed to load Keycloak JWKS: %v",
			err,
		)
	}

	server := &Server{
		keyStore:   keyStore,
		sessions:   NewSessionStore(),
		httpClient: &http.Client{Timeout: 5 * time.Second},

		// Simulated in-memory user database.
		profiles: map[string]UserProfile{
			"user-123": {
				ID:       "user-123",
				Username: "antonio",
				Name:     "Antonio",
				Email:    "antonio@example.com",
				Language: "es",
			},
		},
	}

	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// Browser-facing routes: start/complete the Authorization Code flow.
	router.GET("/login", server.login)
	router.GET("/signup", server.signup)
	router.GET("/auth/callback", server.authCallback)

	// All routes under /api/v1 require authentication, either via a
	// Bearer token (Direct Access Grants) or a browser session cookie
	// (Authorization Code flow via /login or /signup).
	api := router.Group("/api/v1")
	api.Use(server.authenticate)

	api.GET("/profile", server.profile)

	log.Println("API listening on :8081")

	if err := router.Run(":8081"); err != nil {
		log.Fatal(err)
	}
}
