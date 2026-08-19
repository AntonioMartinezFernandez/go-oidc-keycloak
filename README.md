# Keycloak + Go API Authentication Example

This repository contains a minimal example of a Go REST API protected by Keycloak using OAuth 2.0, OpenID Connect (OIDC), and JWT access tokens.

The API uses:

- [Go](https://go.dev/)
- [Gin](https://gin-gonic.com/)
- [golang-jwt/jwt](https://github.com/golang-jwt/jwt)
- [Keycloak](https://www.keycloak.org/)
- Docker Compose

User profiles are stored in memory to keep the example simple.

## Architecture

```text
                         ┌──────────────────────┐
                         │       Keycloak       │
                         │                      │
                         │ http://localhost:8080│
                         │                      │
                         │ Realm: myrealm       │
                         └──────────┬───────────┘
                                    │
                              OAuth 2.0 / OIDC
                                    │
                                    │
┌──────────────┐             ┌──────▼───────────┐
│    Client    │             │     Go API       │
│              │             │                  │
│    curl      │             │ :8081            │
└──────┬───────┘             └──────┬───────────┘
       │                            │
       │ Authorization:             │
       │ Bearer <JWT>               │
       └───────────────────────────►│
                                    │
                              Validate JWT
                                    │
                                    ▼
                              User profile
                              in memory
```

The Go API does not contact Keycloak for every API request. It obtains Keycloak's public signing keys through the JWKS endpoint and validates JWT signatures locally.

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
```

This is only being enabled to make it easy to obtain a token with `curl`.

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
)
```

These values correspond to the Keycloak configuration created above.

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

The API now has two endpoints:

```text
GET /health
GET /api/v1/profile
```

`/health` is public.

`/api/v1/profile` requires a valid JWT access token.

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

# 11. Obtain an Access Token from Keycloak

For this development example, use the OAuth 2.0 Resource Owner Password Credentials grant.

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

---

# 12. Call the Protected API

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

# 13. What Happens Internally

When the request is made:

```http
GET /api/v1/profile HTTP/1.1
Host: localhost:8081
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
```

the Go API performs the following operations.

## Step 1 — Extract the Bearer token

The authentication middleware reads:

```http
Authorization: Bearer <JWT>
```

and extracts the JWT.

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

# 14. Test an Unauthenticated Request

Call the protected endpoint without a token:

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

# 15. Test an Invalid Token

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

# 16. JWT Validation and Key Rotation

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

# 17. Important Security Considerations

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

## Do not use the password grant in modern applications

The example uses:

```text
grant_type=password
```

only to make testing with `curl` straightforward.

For a real browser/mobile application, prefer:

```text
Authorization Code Flow
+
PKCE
```

The normal architecture would be:

```text
User
 │
 ▼
Frontend
 │
 │ Authorization Code + PKCE
 ▼
Keycloak
 │
 │ Access Token
 ▼
Frontend
 │
 │ Authorization: Bearer <JWT>
 ▼
Go API
```

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

---

# 18. Useful Keycloak Endpoints

For the `myrealm` realm:

### OIDC discovery

```text
http://localhost:8080/realms/myrealm/.well-known/openid-configuration
```

### Authorization endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/auth
```

### Token endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/token
```

### JWKS endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/certs
```

### UserInfo endpoint

```text
http://localhost:8080/realms/myrealm/protocol/openid-connect/userinfo
```

---

# 19. Stopping the Environment

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

# 20. Complete Request Flow

The complete flow can be summarized as:

```text
                    ┌─────────────┐
                    │   Keycloak  │
                    └──────┬──────┘
                           │
                    User authentication
                           │
                           ▼
                     Access Token
                         (JWT)
                           │
                           │
                           ▼
                    ┌─────────────┐
                    │   Go API    │
                    └──────┬──────┘
                           │
                    Extract Bearer token
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

This separation allows the Go API to remain independent from the user's credentials while still being able to cryptographically verify that the access token was issued by the trusted Keycloak realm.
