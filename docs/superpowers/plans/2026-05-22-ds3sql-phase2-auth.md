# DS3 SQL Server — Phase 2: Authentication (Cubbit IAM)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Cubbit IAM challenge-response authentication flow. Users log in with email + password, receive a JWT, and the server validates it on protected routes.

**Architecture:** The auth service reimplements the Cubbit IAM protocol: `POST /auth/challenge` → sign with Ed25519 → `POST /auth/signin` → JWT. Middleware extracts the JWT from `Authorization: Bearer` headers. The S3 credentials (access key, secret key, gateway endpoint) are cached in-memory per session.

**Tech Stack:** Go 1.22+, `golang.org/x/crypto/ed25519`, `golang.org/x/crypto/sha3`, `github.com/golang-jwt/jwt/v5`

---

### Task 1: Auth service — Cubbit IAM client

**Files:**
- Create: `DS3-SQL Server/internal/auth/auth.go`
- Create: `DS3-SQL Server/internal/auth/challenge.go`

- [ ] **Step 1: Write the auth types and session struct**

`DS3-SQL Server/internal/auth/auth.go`:

```go
package auth

import (
	"time"
)

type Session struct {
	Email         string
	Token         string
	RefreshToken  string
	ExpiresAt     time.Time
	AccessKey     string
	SecretKey     string
	GatewayEndpoint string
}

type Credentials struct {
	AccessKey string
	SecretKey string
	Endpoint  string
}

type IAMClient struct {
	iamURL string
	client *http.Client
}

func NewIAMClient(iamURL string) *IAMClient {
	return &IAMClient{
		iamURL: iamURL,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}
```

- [ ] **Step 2: Write the challenge-response flow**

`DS3-SQL Server/internal/auth/challenge.go`:

```go
package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/ed25519"
)

type challengeResponse struct {
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
}

type signinRequest struct {
	Email           string `json:"email"`
	SignedChallenge string `json:"signed_challenge"`
}

type signinResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
}

type meResponse struct {
	EndpointGateway string `json:"endpoint_gateway"`
	Account struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	} `json:"account"`
}

func (c *IAMClient) Login(email, password string) (*Session, error) {
	// Step 1: Get challenge
	challengeReq := map[string]string{"email": email}
	body, _ := json.Marshal(challengeReq)

	resp, err := c.client.Post(c.iamURL+"/challenge", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("challenge request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("challenge failed (%d): %s", resp.StatusCode, string(b))
	}

	var chal challengeResponse
	if err := json.NewDecoder(resp.Body).Decode(&chal); err != nil {
		return nil, fmt.Errorf("decode challenge: %w", err)
	}

	// Step 2: Sign the challenge
	saltBytes, _ := base64.StdEncoding.DecodeString(chal.Salt)
	challengeBytes, _ := base64.StdEncoding.DecodeString(chal.Challenge)

	key := sha256.Sum256(append([]byte(password), saltBytes...))
	privateKey := ed25519.NewKeyFromSeed(key[:])

	signature := ed25519.Sign(privateKey, challengeBytes)
	signed := base64.StdEncoding.EncodeToString(signature)

	// Step 3: Sign in
	signinReq := signinRequest{
		Email:           email,
		SignedChallenge: signed,
	}
	body, _ = json.Marshal(signinReq)

	resp, err = c.client.Post(c.iamURL+"/signin", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("signin request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("signin failed (%d): %s", resp.StatusCode, string(b))
	}

	var signin signinResponse
	if err := json.NewDecoder(resp.Body).Decode(&signin); err != nil {
		return nil, fmt.Errorf("decode signin: %w", err)
	}

	expiresAt, _ := time.Parse(time.RFC3339, signin.ExpiresAt)

	// Step 4: Get account details (S3 credentials)
	session := &Session{
		Email:        email,
		Token:        signin.Token,
		RefreshToken: signin.RefreshToken,
		ExpiresAt:    expiresAt,
	}

	meResp, err := c.getMe(signin.Token)
	if err == nil {
		session.GatewayEndpoint = meResp.EndpointGateway
		session.AccessKey = meResp.Account.AccessKey
		session.SecretKey = meResp.Account.SecretKey
	}

	return session, nil
}

func (c *IAMClient) Refresh(refreshToken string) (*Session, error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})

	resp, err := c.client.Post(c.iamURL+"/signin/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, string(b))
	}

	var signin signinResponse
	if err := json.NewDecoder(resp.Body).Decode(&signin); err != nil {
		return nil, fmt.Errorf("decode refresh: %w", err)
	}

	expiresAt, _ := time.Parse(time.RFC3339, signin.ExpiresAt)

	return &Session{
		Token:        signin.Token,
		RefreshToken: signin.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func (c *IAMClient) getMe(token string) (*meResponse, error) {
	req, _ := http.NewRequest("GET", c.iamURL+"/accounts/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var me meResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return nil, err
	}
	return &me, nil
}
```

- [ ] **Step 3: Add dependencies**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get golang.org/x/crypto/ed25519 golang.org/x/crypto/sha3
```

Expected: `go: added ...`

- [ ] **Step 4: Write test for auth flow**

`DS3-SQL Server/internal/auth/auth_test.go` — this is an integration test that requires a real IAM endpoint. Write a unit test for the signing logic:

```go
package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/ed25519"
)

func TestKeyDerivation(t *testing.T) {
	password := "test-password"
	salt := base64.StdEncoding.EncodeToString([]byte("test-salt"))

	saltBytes, _ := base64.StdEncoding.DecodeString(salt)
	key := sha256.Sum256(append([]byte(password), saltBytes...))
	privKey := ed25519.NewKeyFromSeed(key[:])
	pubKey := privKey.Public().(ed25519.PublicKey)

	msg := []byte("challenge-data")
	sig := ed25519.Sign(privKey, msg)

	if !ed25519.Verify(pubKey, msg, sig) {
		t.Fatal("signature verification failed")
	}
}
```

- [ ] **Step 5: Run test**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go test ./internal/auth/ -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add internal/auth/ && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: Cubbit IAM auth client with challenge-response"
```

---

### Task 2: Session store and JWT middleware

**Files:**
- Create: `DS3-SQL Server/internal/auth/session.go`
- Create: `DS3-SQL Server/internal/auth/middleware.go`

- [ ] **Step 1: Write in-memory session store**

`DS3-SQL Server/internal/auth/session.go`:

```go
package auth

import (
	"sync"
	"time"
)

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

func (s *SessionStore) Set(token string, session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = session
}

func (s *SessionStore) Get(token string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(session.ExpiresAt) {
		delete(s.sessions, token)
		return nil, false
	}
	return session, true
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}
```

- [ ] **Step 2: Write JWT auth middleware**

`DS3 SQL Server/internal/auth/middleware.go`:

```go
package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const SessionKey contextKey = "session"

func Middleware(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			session, ok := store.Get(token)
			if !ok {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), SessionKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetSession(r *http.Request) *Session {
	session, _ := r.Context().Value(SessionKey).(*Session)
	return session
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go test ./internal/auth/ -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add internal/auth/ && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: session store and auth middleware"
```

---

### Task 3: Auth HTTP handlers

**Files:**
- Create: `DS3-SQL Server/internal/api/auth_handler.go`
- Modify: `DS3-SQL Server/cmd/ds3sql-server/main.go`

- [ ] **Step 1: Write auth API handler**

The handler accepts both JSON (from CLI/API) and form-encoded (from Web UI login form):

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
)

type AuthHandler struct {
	iamClient *auth.IAMClient
	store     *auth.SessionStore
}

func NewAuthHandler(iamClient *auth.IAMClient, store *auth.SessionStore) *AuthHandler {
	return &AuthHandler{iamClient: iamClient, store: store}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest

	// Accept both JSON and form-encoded
	ct := r.Header.Get("Content-Type")
	if ct == "application/x-www-form-urlencoded" {
		r.ParseForm()
		req.Email = r.FormValue("email")
		req.Password = r.FormValue("password")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
	}

	if req.Email == "" || req.Password == "" {
		http.Error(w, `{"error":"email and password required"}`, http.StatusBadRequest)
		return
	}

	session, err := h.iamClient.Login(req.Email, req.Password)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	h.store.Set(session.Token, session)

	resp := loginResponse{
		Token:        session.Token,
		RefreshToken: session.RefreshToken,
		ExpiresAt:    session.ExpiresAt.Format("2006-01-02T15:04:05Z"),
	}

	// HTMX requests get a redirect header; API/CLI get JSON
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/browse")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type meResponse struct {
	Email           string `json:"email"`
	GatewayEndpoint string `json:"gateway_endpoint"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		http.Error(w, `{"error":"refresh_token required"}`, http.StatusBadRequest)
		return
	}

	newSession, err := h.iamClient.Refresh(req.RefreshToken)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnauthorized)
		return
	}

	h.store.Set(newSession.Token, newSession)

	resp := loginResponse{
		Token:        newSession.Token,
		RefreshToken: newSession.RefreshToken,
		ExpiresAt:    newSession.ExpiresAt.Format("2006-01-02T15:04:05Z"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	resp := meResponse{
		Email:           session.Email,
		GatewayEndpoint: session.GatewayEndpoint,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 2: Wire auth routes into main.go**

Edit `cmd/ds3sql-server/main.go` — add imports and routes after the middleware setup:

```go
// Add to imports
"github.com/esignoretti/ds3-sql-server/internal/auth"
"github.com/esignoretti/ds3-sql-server/internal/api"
```

After `r.Use(middleware.Timeout(60 * time.Second))`, add:

```go
// Auth
iamClient := auth.NewIAMClient(cfg.IAMURL)
sessionStore := auth.NewSessionStore()
authHandler := api.NewAuthHandler(iamClient, sessionStore)

r.Post("/auth/login", authHandler.Login)
r.Post("/auth/refresh", authHandler.Refresh)

// Protected group
r.Group(func(r chi.Router) {
	r.Use(auth.Middleware(sessionStore))
	r.Get("/auth/me", authHandler.Me)
})

// Health is public
r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
})
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql-server/
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: auth HTTP handlers and route wiring"
```
