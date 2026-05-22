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
	Account         struct {
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
