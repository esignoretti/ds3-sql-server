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

	"crypto/ed25519"
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
	Account         struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	} `json:"account"`
}

type LoginOpts struct {
	TFA      string
	TenantID string
	IAMURL   string
}

func (c *IAMClient) Login(email, password string, opts *LoginOpts) (*Session, error) {
	iamURL := c.iamURL
	if opts != nil && opts.IAMURL != "" {
		iamURL = opts.IAMURL
	}

	// Step 1: Get challenge
	challengeReq := map[string]string{"email": email}
	if opts != nil && opts.TenantID != "" {
		challengeReq["tenant_id"] = opts.TenantID
	}
	body, _ := json.Marshal(challengeReq)

	resp, err := c.client.Post(iamURL+"/challenge", "application/json", bytes.NewReader(body))
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
	saltBytes, err := base64.StdEncoding.DecodeString(chal.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	challengeBytes, err := base64.StdEncoding.DecodeString(chal.Challenge)
	if err != nil {
		return nil, fmt.Errorf("decode challenge: %w", err)
	}

	key := sha256.Sum256(append([]byte(password), saltBytes...))
	privateKey := ed25519.NewKeyFromSeed(key[:])

	signature := ed25519.Sign(privateKey, challengeBytes)
	signed := base64.StdEncoding.EncodeToString(signature)

	// Step 3: Sign in
	signinReq := signinRequest{
		Email:           email,
		SignedChallenge: signed,
	}
	signinBody, _ := json.Marshal(signinReq)

	signinURL := iamURL + "/signin"
	if opts != nil && opts.TFA != "" {
		signinURL += "?tfa_code=" + opts.TFA
	}
	if opts != nil && opts.TenantID != "" {
		if opts.TFA != "" {
			signinURL += "&tenant_id=" + opts.TenantID
		} else {
			signinURL += "?tenant_id=" + opts.TenantID
		}
	}

	resp, err = c.client.Post(signinURL, "application/json", bytes.NewReader(signinBody))
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

	expiresAt, err := time.Parse(time.RFC3339, signin.ExpiresAt)
	if err != nil {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

	// Step 4: Get account details (S3 credentials)
	session := &Session{
		Email:        email,
		Token:        signin.Token,
		RefreshToken: signin.RefreshToken,
		ExpiresAt:    expiresAt,
	}

	meResp, err := c.getMe(signin.Token)
	if err != nil {
		session.GatewayEndpoint = ""
		session.AccessKey = ""
		session.SecretKey = ""
	} else {
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

	expiresAt, err := time.Parse(time.RFC3339, signin.ExpiresAt)
	if err != nil {
		expiresAt = time.Now().Add(24 * time.Hour)
	}

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

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get me failed (%d): %s", resp.StatusCode, string(b))
	}

	var me meResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return nil, err
	}
	return &me, nil
}
