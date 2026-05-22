package auth

import (
	"net/http"
	"time"
)

type Session struct {
	Email           string
	Token           string
	RefreshToken    string
	ExpiresAt       time.Time
	AccessKey       string
	SecretKey       string
	GatewayEndpoint string
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
