package auth

import "time"

type ProjectCred struct {
	ProjectID   string
	ProjectName string
	AccessKey   string
	SecretKey   string
}

type Session struct {
	Email           string
	Token           string
	RefreshToken    string
	ExpiresAt       time.Time
	GatewayEndpoint string
	Projects        []ProjectCred
}

// HasProject returns true if the session has access to the given projectID.
// An empty projectID matches any project (returns true if there's at least one).
func (s *Session) HasProject(projectID string) bool {
	if projectID == "" {
		return len(s.Projects) > 0
	}
	for _, p := range s.Projects {
		if p.ProjectID == projectID {
			return true
		}
	}
	return false
}

type IAMClient struct {
	httpClient *httpClient
}

func NewIAMClient(iamURL string) *IAMClient {
	c := newHTTPClient()
	if iamURL != "" {
		c.SetBaseURL(iamURL)
	}
	return &IAMClient{httpClient: c}
}

func (c *IAMClient) IAMURL() string {
	if c.httpClient == nil {
		return ""
	}
	c.httpClient.mu.Lock()
	defer c.httpClient.mu.Unlock()
	return c.httpClient.baseURL
}

func S3KeyName(userName, projectName string) string {
	safe := func(s string) string {
		b := make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
				b = append(b, c)
			} else {
				b = append(b, '_')
			}
		}
		return string(b)
	}
	return "ds3sql-" + safe(userName) + "-" + safe(projectName)
}
