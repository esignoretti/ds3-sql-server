package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultCoordinatorURL = "https://api.eu00wi.cubbit.services"

type httpClient struct {
	mu           sync.Mutex
	baseURL      string
	client       *http.Client
	refreshToken string
}

func newHTTPClient() *httpClient {
	jar, _ := cookiejar.New(nil)
	return &httpClient{
		baseURL: defaultCoordinatorURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}
}

func (c *httpClient) SetBaseURL(u string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseURL = strings.TrimRight(u, "/")
}

func (c *httpClient) BaseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseURL
}

func (c *httpClient) GetRefreshCookie() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshToken
}

func (c *httpClient) RestoreRefreshCookie(value string) {
	if value == "" {
		return
	}
	c.mu.Lock()
	c.refreshToken = value
	base := c.baseURL
	jar := c.client.Jar
	c.mu.Unlock()

	u, err := url.Parse(base)
	if err != nil || jar == nil {
		return
	}
	jar.SetCookies(u, []*http.Cookie{{
		Name:  "_refresh",
		Value: value,
		Path:  "/",
	}})
}

func (c *httpClient) doRequest(method, path string, body interface{}, authToken string) ([]byte, error) {
	c.mu.Lock()
	baseURL := c.baseURL
	client := c.client
	c.mu.Unlock()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	for _, ck := range resp.Cookies() {
		if ck.Name == "_refresh" && ck.Value != "" {
			c.mu.Lock()
			c.refreshToken = ck.Value
			c.mu.Unlock()
		}
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := string(responseBody)
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		return nil, fmt.Errorf("HTTP %d %s: %s", resp.StatusCode, path, preview)
	}

	return responseBody, nil
}

type LoginOpts struct {
	TFA      string
	TenantID string
	IAMURL   string
}

type challengeRequest struct {
	Email    string `json:"email"`
	TenantID string `json:"tenant_id,omitempty"`
}

type challengeResponse struct {
	Salt      string `json:"salt"`
	Challenge string `json:"challenge"`
}

type signinRequest struct {
	Email           string `json:"email"`
	SignedChallenge string `json:"signed_challenge"`
	TfaCode         string `json:"tfa_code,omitempty"`
	TenantID        string `json:"tenant_id,omitempty"`
}

type signinResponse struct {
	Token        string `json:"token"`
	Exp          int64  `json:"exp"`
	ExpDate      string `json:"exp_date"`
	RefreshToken string `json:"-"`
}

type Account struct {
	ID              string     `json:"id"`
	EndpointGateway string     `json:"endpoint_gateway"`
	Emails          []struct {
		Email     string `json:"email"`
		IsDefault bool   `json:"default"`
	} `json:"emails"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	TenantID  string `json:"tenant_id"`
}

type Project struct {
	ID    string `json:"project_id"`
	Name  string `json:"project_name"`
	Users []struct {
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		IsRoot   bool   `json:"is_root"`
	} `json:"users"`
}

type ForgeJWTResponse struct {
	Token   string `json:"token"`
	Exp     int64  `json:"exp"`
	ExpDate string `json:"exp_date"`
}

type APIKey struct {
	Name      string `json:"name"`
	ApiKey    string `json:"api_key"`
	SecretKey string `json:"secret_key"`
}

func (c *IAMClient) Login(email, password string, opts *LoginOpts) (*Session, error) {
	httpClient := c.httpClient

	if opts != nil && opts.IAMURL != "" {
		httpClient.SetBaseURL(opts.IAMURL)
	}

	chalReq := challengeRequest{Email: email}
	if opts != nil && opts.TenantID != "" {
		chalReq.TenantID = opts.TenantID
	}

	chalData, err := httpClient.doRequest("POST", "/iam/v1/auth/signin/challenge", chalReq, "")
	if err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}

	var chal challengeResponse
	if err := json.Unmarshal(chalData, &chal); err != nil {
		return nil, fmt.Errorf("parse challenge: %w", err)
	}

	seed := sha256.Sum256(append([]byte(password), []byte(chal.Salt)...))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signature := ed25519.Sign(privateKey, []byte(chal.Challenge))
	signedChallenge := base64.StdEncoding.EncodeToString(signature)

	signinReq := signinRequest{
		Email:           email,
		SignedChallenge: signedChallenge,
	}
	if opts != nil {
		signinReq.TfaCode = opts.TFA
		signinReq.TenantID = opts.TenantID
	}

	signinData, err := httpClient.doRequest("POST", "/iam/v1/auth/signin", signinReq, "")
	if err != nil {
		return nil, fmt.Errorf("signin: %w", err)
	}

	var signin signinResponse
	if err := json.Unmarshal(signinData, &signin); err != nil {
		return nil, fmt.Errorf("parse signin: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, signin.ExpDate)
	if err != nil {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	return &Session{
		Email:        email,
		Token:        signin.Token,
		RefreshToken: httpClient.refreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func (c *IAMClient) GetAccount(token string) (*Account, error) {
	data, err := c.httpClient.doRequest("GET", "/iam/v1/accounts/me", nil, token)
	if err != nil {
		return nil, err
	}
	var account Account
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("parse account: %w", err)
	}
	return &account, nil
}

func (c *IAMClient) GetProjects(token string) ([]Project, error) {
	data, err := c.httpClient.doRequest("GET", "/composer-hub/v1/projects", nil, token)
	if err != nil {
		return nil, err
	}
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, fmt.Errorf("parse projects: %w", err)
	}
	return projects, nil
}

func (c *IAMClient) ForgeJWT(userID string) (*ForgeJWTResponse, error) {
	path := "/iam/v1/auth/forge/access?" + url.Values{"user_id": {userID}}.Encode()
	data, err := c.httpClient.doRequest("GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	var resp ForgeJWTResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse forge: %w", err)
	}
	return &resp, nil
}

func (c *IAMClient) ListAPIKeys(userID, forgeJWT string) ([]APIKey, error) {
	path := "/keyvault/api/v3/keys?" + url.Values{"user_id": {userID}}.Encode()
	data, err := c.httpClient.doRequest("GET", path, nil, forgeJWT)
	if err != nil {
		return nil, err
	}
	var keys []APIKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("parse api keys: %w", err)
	}
	return keys, nil
}

func (c *IAMClient) DeleteAPIKey(apiKey, userID, forgeJWT string) error {
	path := "/keyvault/api/v3/keys/" + url.PathEscape(apiKey) + "?" + url.Values{"user_id": {userID}}.Encode()
	_, err := c.httpClient.doRequest("DELETE", path, nil, forgeJWT)
	return err
}

func (c *IAMClient) CreateAPIKey(name, userID, forgeJWT string) (*APIKey, error) {
	path := "/keyvault/api/v3/keys/" + url.PathEscape(name) + "?" + url.Values{"user_id": {userID}}.Encode()
	data, err := c.httpClient.doRequest("POST", path, nil, forgeJWT)
	if err != nil {
		return nil, err
	}
	var key APIKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("parse api key: %w", err)
	}
	return &key, nil
}

func (c *IAMClient) Refresh(refreshToken string) (*Session, error) {
	if refreshToken != "" {
		c.httpClient.RestoreRefreshCookie(refreshToken)
	}

	data, err := c.httpClient.doRequest("GET", "/iam/v1/auth/refresh/access", nil, "")
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse refresh: %w", err)
	}

	return &Session{
		Token:        resp.Token,
		RefreshToken: c.httpClient.refreshToken,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}, nil
}
