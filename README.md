# Keycloak + Go API Authentication Example

This repository contains a minimal example of a Go REST API protected by Keycloak using OAuth 2.0, OpenID Connect (OIDC), and JWT access tokens.

Two ways to obtain a token are demonstrated:

- **Resource Owner Password Credentials (ROPC)** — a direct `username`/`password` exchange via `curl`, useful for quick local testing.
- **Authorization Code Flow with PKCE** — the browser-based flow used by `/login` and `/signup`, where the user authenticates directly on Keycloak's own pages and the API never sees their password.

The API uses:

- [Go](https://go.dev/)
- [Gin](https://gin-gonic.com/)
- [golang-jwt/jwt](https://github.com/golang-jwt/jwt)
- [Keycloak](https://www.keycloak.org/)
- Docker Compose

User profiles and browser sessions are stored in memory to keep the example simple.

## Architecture

```text
                         ┌────────────────────────┐
                         │        Keycloak        │
                         │  http://localhost:8080 │
                         │  Realm: myrealm        │
                         └────────────┬───────────┘
                                      │
                              OAuth 2.0 / OIDC
                                      │
                                      ▼
                         ┌─────────────────────────┐
                         │          Go API         │
                         │         :8081           │
                         │                         │
                         │  /login    /signup      │
                         │  /auth/callback         │
                         │  /api/v1/profile         │
                         └─────────┬─────────┬─────┘
                                   │         │
                    Authorization: │         │  session cookie
                    Bearer <JWT>   │         │  (Authorization Code
                    (ROPC)         │         │   + PKCE)
                                   │         │
                            ┌──────▼──┐   ┌──▼───────┐
                            │  curl   │   │ Browser  │
                            └─────────┘   └──────────┘
```

The Go API does not contact Keycloak for every API request. It obtains Keycloak's public signing keys through the JWKS endpoint and validates JWT signatures locally. The only per-request calls to Keycloak are the token exchange in `/auth/callback` and, occasionally, a JWKS refresh on signing-key rotation.

---

## Prerequisites

Install the following tools:

- Docker
- Docker Compose
- Go 1.22 or newer
- curl

Verify the installation:

```bash
docker --version
docker compose version
go version
curl --version
```

---

# 1. Start Keycloak

Start Keycloak:

```bash
docker compose up -d
```

Check that the container is running:

```bash
docker compose ps
```

You should see the Keycloak container with port `8080` exposed.

You can also inspect the logs:

```bash
docker compose logs -f keycloak
```

Keycloak should now be available at:

```text
http://localhost:8080
```

---

# 2. Open the Keycloak Administration Console

Open:

```text
http://localhost:8080
```

Log in using:

```text
Username: admin
Password: admin
```

These credentials come from the environment variables in `docker-compose.yaml`.

> `start-dev` is intended for development and testing. It should not be used as-is for a production deployment.

---

# 3. Create the Realm

A realm is an isolated security domain containing users, clients, roles, credentials, and other Keycloak configuration.

In the Keycloak Administration Console:

1. Open the realm selector.
2. Select **Create realm**.
3. Enter:

```text
Realm name: myrealm
```

4. Create the realm.

The resulting issuer URL will be:

```text
http://localhost:8080/realms/myrealm
```

---

# 4. Create the API Client

Create a client representing the API.

Go to:

```text
Clients
    → Create client
```

Configure:

```text
Client type: OpenID Connect
Client ID:   my-api
```

Continue through the configuration screens.

For this example, enable:

```text
Direct access grants: ON
Standard flow:        ON
```

`Direct access grants` is what allows the ROPC `curl` requests in this guide to work. `Standard flow` is what enables the Authorization Code flow used by `/login` and `/signup` — without it, those routes will fail when Keycloak tries to redirect back to the API.

Leave `Client authentication` **OFF**. The client stays public (no client secret): the Authorization Code exchange in `/auth/callback` is secured with PKCE instead, so no secret needs to be stored in the API.

On the same client settings page, set:

```text
Valid redirect URIs: http://localhost:8081/auth/callback
```

Keycloak will refuse to redirect back to any URI that isn't listed here — this is what stops an attacker-registered client from stealing authorization codes meant for this API.

The resulting client ID is:

```text
my-api
```

The Go API expects this value as the JWT audience:

```text
aud = my-api
```

> By default, Keycloak does **not** put the client ID in the `aud` claim. Access tokens are issued with `aud = account` unless an audience mapper is added. Without the step below, the Go API will reject valid tokens with `invalid_token`.

---

## 4.1 Add an Audience Mapper

Go to:

```text
Clients
    → my-api
    → Client scopes
    → my-api-dedicated
    → Mappers
    → Configure a new mapper
    → Audience
```

Choose:

```text
Mapper type: Audience
```

Configure:

```text
Name:                      aud-my-api
Included Client Audience:  my-api
Add to access token:       ON
```

Save.

Tokens issued after this point will include `my-api` in the `aud` claim:

```text
"aud": "my-api"
```

> Tokens obtained **before** this change will still have the old `aud` value. Request a new token after saving the mapper.

---

## 4.2 Enable User Registration (for `/signup`)

The `/signup` endpoint sends the browser to Keycloak's own hosted registration page rather than implementing signup in the Go API itself.

Go to:

```text
Realm settings
    → Login
    → User registration: ON
```

Without this, `/signup` will still redirect correctly, but Keycloak will fall back to showing the login page instead of a registration form.

---

# 5. Create a User

Go to:

```text
Users
    → Add user
```

Create:

```text
Username: antonio
Email:    antonio@example.com
First name: Antonio
Last name: Martinez
```

IMPORTANT: set the email, first name, and last name to have a valid user profile. Otherwise, the Keycloak API will return an error (`{"error":"invalid_grant","error_description":"Account is not fully set up"}`)

Save the user.

Then open the user's:

```text
Credentials
```

Set:

```text
Password: password
```

Make sure:

```text
Temporary: OFF
```

The user can now authenticate using:

```text
username = antonio
password = password
```

---

# 6. Verify the Keycloak OIDC Configuration

Keycloak automatically exposes the OpenID Connect discovery document.

Open:

```text
http://localhost:8080/realms/myrealm/.well-known/openid-configuration
```

Among other things, it contains the issuer and JWKS endpoint:

```json
{
  "issuer": "http://localhost:8080/realms/myrealm",
  "jwks_uri": "http://localhost:8080/realms/myrealm/protocol/openid-connect/certs"
}
```

The API uses the following JWKS endpoint:

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/certs
```

You can inspect it with:

```bash
curl \
  http://localhost:8080/realms/myrealm/protocol/openid-connect/certs
```

The response contains Keycloak's public RSA keys.

For example:

```json
{
  "keys": [
    {
      "kid": "...",
      "kty": "RSA",
      "alg": "RS256",
      "use": "sig",
      "n": "...",
      "e": "AQAB"
    }
  ]
}
```

The `kid` identifies the signing key used by a JWT.

---

# 7. Configure the Go API

The API expects the following Keycloak configuration:

```go
const (
    issuer   = "http://localhost:8080/realms/myrealm"
    audience = "my-api"
    jwksURL  = issuer + "/protocol/openid-connect/certs"

    // Authorization Code flow endpoints.
    authorizeURL = issuer + "/protocol/openid-connect/auth"
    tokenURL     = issuer + "/protocol/openid-connect/token"
    registerURL  = issuer + "/protocol/openid-connect/registrations"

    // my-api is a public client (Client authentication OFF), so the
    // Authorization Code flow relies on PKCE instead of a client secret.
    clientID    = "my-api"
    redirectURI = "http://localhost:8081/auth/callback"
)
```

These values correspond to the Keycloak configuration created above. `redirectURI` must exactly match the "Valid redirect URIs" entry configured in section 4.

The API listens on:

```text
http://localhost:8081
```

---

# 8. Initialize the Go Project

Download dependencies:

```bash
go mod tidy
```

Update the value of `exampleUserID` in `cmd/main.go` to match the Keycloak user ID (from Keycloak, User, antonio, ID):

---

# 9. Start the Go API

Run:

```bash
go run main.go
```

The API should print:

```text
API listening on :8081
```

The API now has the following endpoints:

```text
GET /health              — public
GET /login                — redirects the browser to Keycloak's login page
GET /signup                — redirects the browser to Keycloak's registration page
GET /auth/callback           — Keycloak redirects here after login/signup; starts a session
GET /api/v1/profile            — requires a Bearer token OR an authenticated session
```

`/health` is public.

`/login`, `/signup`, and `/auth/callback` are public — they're the routes that establish a session in the first place.

`/api/v1/profile` requires either:

- an `Authorization: Bearer <JWT>` header (obtained via ROPC, section 11), or
- a `session_id` cookie set by `/auth/callback` after a successful browser login or signup.

---

# 10. Test the Health Endpoint

Run:

```bash
curl http://localhost:8081/health
```

Expected response:

```json
{
  "status": "ok"
}
```

---

# 11. Obtain an Access Token via curl (ROPC)

For quick local testing without a browser, use the OAuth 2.0 Resource Owner Password Credentials grant.

Run:

```bash
curl -X POST \
  http://localhost:8080/realms/myrealm/protocol/openid-connect/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=my-api' \
  -d 'grant_type=password' \
  -d 'username=antonio' \
  -d 'password=password'
```

Keycloak should return something similar to:

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 300,
  "refresh_expires_in": 1800,
  "refresh_token": "...",
  "token_type": "Bearer",
  "scope": "..."
}
```

Copy the `access_token`.

You can also store it directly in a shell variable:

```bash
TOKEN=$(curl -s -X POST \
  http://localhost:8080/realms/myrealm/protocol/openid-connect/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=my-api' \
  -d 'grant_type=password' \
  -d 'username=antonio' \
  -d 'password=password' \
  | jq -r '.access_token')
```

If `jq` is installed, verify it:

```bash
echo "$TOKEN"
```

> Note the double quotes below — `Authorization: Bearer $TOKEN"` inside single quotes would send the literal string `$TOKEN` instead of its value.

---

# 12. Call the Protected API with the Token

Use the access token in the HTTP `Authorization` header:

```bash
curl \
  http://localhost:8081/api/v1/profile \
  -H "Authorization: Bearer $TOKEN"
```

Expected response:

```json
{
  "id": "user-123",
  "username": "antonio",
  "name": "Antonio",
  "email": "antonio@example.com",
  "language": "es"
}
```

The API identifies the user using the `sub` claim from the JWT.

---

# 13. Log In or Sign Up via the Browser (Authorization Code + PKCE)

This is the flow a real frontend would use — the API never handles the user's password.

**Log in as an existing user:**

1. Open `http://localhost:8081/login` in a browser.
2. You're redirected to Keycloak's login page. Sign in as `antonio` / `password`.
3. Keycloak redirects back to `http://localhost:8081/auth/callback` with an authorization code.
4. The API exchanges the code for tokens, starts a session, and redirects to `/api/v1/profile`.
5. You should see antonio's profile JSON, and a `session_id` cookie set in the browser.

**Register a new user:**

1. Open `http://localhost:8081/signup` instead.
2. You land on Keycloak's own hosted registration form (username, email, first/last name, password).
3. After submitting, the same callback flow runs, and you're redirected to `/api/v1/profile`.

> The example's in-memory `profiles` map only has a seeded entry for `user-123` (antonio). A newly registered user will authenticate successfully but get `{"error":"profile_not_found"}` from `/api/v1/profile`, since there's no profile pre-populated for their Keycloak `sub`. A real application would create or link a profile record at this point — this example skips that to stay focused on the auth flow.

---

# 14. What Happens Internally

When a request is made:

```http
GET /api/v1/profile HTTP/1.1
Host: localhost:8081
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
```

the Go API performs the following operations.

## Step 1 — Extract the token

The authentication middleware first checks for:

```http
Authorization: Bearer <JWT>
```

If that header is absent, it falls back to reading the `session_id` cookie and looking up the access token stored for that session (set by `/auth/callback` after a browser login or signup). Either path produces the same raw JWT for the remaining steps.

## Step 2 — Read the JWT header

The JWT header contains information similar to:

```json
{
  "alg": "RS256",
  "typ": "JWT",
  "kid": "abc123"
}
```

The API uses `kid` to identify the public key.

## Step 3 — Obtain the public key

The API retrieves Keycloak's JWKS from:

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/certs
```

The keys are cached in memory.

The API does not need to contact Keycloak for every request.

## Step 4 — Verify the JWT signature

The API verifies the RSA-SHA256 signature using the public key.

If the signature is invalid:

```http
HTTP/1.1 401 Unauthorized
```

## Step 5 — Validate the claims

The API validates:

```text
iss
aud
exp
```

For this example:

```text
iss = http://localhost:8080/realms/myrealm
aud = my-api
```

The token must also have a valid expiration time.

## Step 6 — Extract the user identity

The JWT contains:

```json
{
  "sub": "user-123",
  "preferred_username": "antonio"
}
```

The API uses:

```text
sub = user-123
```

as the user identifier.

## Step 7 — Retrieve the profile

The example contains an in-memory map:

```go
profiles := map[string]UserProfile{
    "user-123": {
        ID:       "user-123",
        Username: "antonio",
        Name:     "Antonio",
        Email:    "antonio@example.com",
        Language: "es",
    },
}
```

The API performs the equivalent of:

```go
profile := profiles["user-123"]
```

and returns it.

---

# 15. Test an Unauthenticated Request

Call the protected endpoint without a token and without a session cookie:

```bash
curl http://localhost:8081/api/v1/profile
```

Expected response:

```http
HTTP/1.1 401 Unauthorized
```

```json
{
  "error": "missing_authorization"
}
```

---

# 16. Test an Invalid Token

Use an invalid token:

```bash
curl \
  http://localhost:8081/api/v1/profile \
  -H "Authorization: Bearer invalid-token"
```

Expected response:

```http
HTTP/1.1 401 Unauthorized
```

```json
{
  "error": "invalid_token"
}
```

---

# 17. JWT Validation and Key Rotation

The API initially downloads Keycloak's public keys when it starts.

The flow is:

```text
Go API
   │
   │ GET JWKS
   ▼
Keycloak
   │
   │ public keys
   ▼
KeyStore
   │
   └── in-memory cache
```

When a JWT contains an unknown `kid`, the API refreshes the JWKS:

```text
JWT
 │
 └── kid = new-key
          │
          ▼
      KeyStore
          │
          └── key not found
                  │
                  ▼
             Refresh JWKS
                  │
                  ▼
               Keycloak
                  │
                  ▼
             new public key
```

This allows the API to handle Keycloak signing-key rotation.

---

# 18. Important Security Considerations

This repository is a demonstration and is intentionally simplified.

## Do not use `start-dev` in production

The Docker Compose configuration uses:

```text
start-dev
```

for convenience.

A production Keycloak deployment should use:

- PostgreSQL or another supported production database
- TLS
- secure administrator credentials
- appropriate hostname configuration
- appropriate resource limits
- persistent storage
- production Keycloak configuration

## The password grant (ROPC) is for local testing only

Sections 11–12 use:

```text
grant_type=password
```

only to make testing with `curl` straightforward, without needing a browser.

For any real browser or mobile application, use the flow implemented by `/login` and `/signup` instead:

```text
Authorization Code Flow
+
PKCE
```

so the API and the browser code never see the user's raw credentials — only Keycloak does.

## Access tokens vs ID tokens

The API should receive an **access token**.

Do not use an OIDC ID token as the API authorization credential simply because it is also a JWT.

The distinction is:

```text
ID Token
    → describes the authentication event and user identity
    → intended for the OIDC client

Access Token
    → authorizes access to a protected resource
    → intended for the API/resource server
```

## The session store is in-memory and single-instance

`/auth/callback` stores access tokens in a plain `map[string]Session` guarded by a mutex. This is fine for a local demo, but:

- Sessions reset on every restart.
- It won't work across multiple API instances behind a load balancer.

A production deployment would back this with a shared store (Redis, a database) instead.

## Cookies are not marked `Secure`

The `session_id`, `oauth_state`, and `oauth_verifier` cookies are set with `secure=false` because this example runs over plain HTTP on `localhost`. Once the app is served over HTTPS, set `secure=true` so these cookies are never sent over an unencrypted connection.

---

# 19. Useful Keycloak Endpoints

For the `myrealm` realm:

### OIDC discovery

```text
http://localhost:8080/realms/myrealm/.well-known/openid-configuration
```

### Authorization endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/auth
```

Used by `/login` to start the Authorization Code flow.

### Registration endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/registrations
```

Used by `/signup` to start the same flow on Keycloak's hosted registration page. Requires "User registration" to be enabled (section 4.2).

### Token endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/token
```

Used both by the ROPC `curl` requests (section 11) and by `/auth/callback` to exchange an authorization code for tokens.

### JWKS endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/certs
```

### UserInfo endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/userinfo
```

---

# 20. Stopping the Environment

Stop the containers:

```bash
docker compose down
```

If you want to remove the containers and associated anonymous volumes:

```bash
docker compose down -v
```

The current example does not configure persistent Keycloak storage, so recreating the container can reset the Keycloak configuration.

---

# 21. Complete Request Flow

The complete flow can be summarized as:

```text
                    ┌─────────────┐
                    │   Keycloak  │
                    └──────┬──────┘
                           │
              User authentication
              (password grant, or
               login/registration
                   page + PKCE)
                           │
                           ▼
                     Access Token
                         (JWT)
                           │
                           ▼
                    ┌─────────────┐
                    │   Go API    │
                    └──────┬──────┘
                           │
                Extract token from either
              Authorization header or session
                           │
                           ▼
                      Read JWT kid
                           │
                           ▼
                    Find JWK in cache
                           │
                      cache miss?
                      /          \
                    yes           no
                     │             │
                     ▼             │
                 Keycloak          │
                    JWKS           │
                     │             │
                     └──────┬──────┘
                            ▼
                     Public RSA key
                            │
                            ▼
                    Verify JWT signature
                            │
                            ▼
                    Validate JWT claims
                    ├── iss
                    ├── aud
                    └── exp
                            │
                            ▼
                         sub
                            │
                            ▼
                    In-memory profile
                            │
                            ▼
                       HTTP 200
```

The key security boundary is therefore:

```text
Keycloak
   │
   │ authenticates the user
   │ and signs the access token
   ▼
JWT
   │
   │ cryptographically verified
   ▼
Go API
   │
   │ authorizes the request
   ▼
Protected resource
```

This separation allows the Go API to remain independent from the user's credentials while still being able to cryptographically verify that the access token was issued by the trusted Keycloak realm — whether that token arrived via a raw `curl` request or via a browser session established through `/login` or `/signup`.
