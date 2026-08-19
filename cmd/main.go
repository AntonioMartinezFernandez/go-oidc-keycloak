package main

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
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
)

type UserProfile struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Language string `json:"language"`
}

var exampleUserID = "4e2f105e-2d66-4810-a807-5de1c821368e"

var exampleProfiles = map[string]UserProfile{
	exampleUserID: {
		ID:       exampleUserID,
		Username: "antonio",
		Name:     "Antonio",
		Email:    "antonio@example.com",
		Language: "es",
	},
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

type Server struct {
	keyStore *KeyStore

	// In a real application this would normally be a database or another
	// persistent storage mechanism.
	profiles map[string]UserProfile
}

func (s *Server) authenticate(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")

	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "missing_authorization",
		})
		return
	}

	parts := strings.SplitN(authHeader, " ", 2)

	if len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") ||
		parts[1] == "" {

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "invalid_authorization_header",
		})
		return
	}

	rawToken := parts[1]

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
		keyStore: keyStore,

		// Simulated in-memory user database.
		profiles: exampleProfiles,
	}

	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// All routes under /api/v1 require authentication.
	api := router.Group("/api/v1")
	api.Use(server.authenticate)

	api.GET("/profile", server.profile)

	log.Println("API listening on :8081")

	if err := router.Run(":8081"); err != nil {
		log.Fatal(err)
	}
}
