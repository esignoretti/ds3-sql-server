package api

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
)

type AuthHandler struct {
	iamClient *auth.IAMClient
	store     *auth.SessionStore
}

func NewAuthHandler(iamClient *auth.IAMClient, store *auth.SessionStore) *AuthHandler {
	return &AuthHandler{iamClient: iamClient, store: store}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var email, password, tfaCode, tenantID, apiURL string

	ct := r.Header.Get("Content-Type")
	if ct == "application/x-www-form-urlencoded" {
		r.ParseForm()
		email = r.FormValue("email")
		password = r.FormValue("password")
		tfaCode = r.FormValue("tfa_code")
		tenantID = r.FormValue("tenant_id")
		apiURL = r.FormValue("api_url")
	} else {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		email = req.Email
		password = req.Password
	}

	if email == "" || password == "" {
		http.Error(w, `{"error":"email and password required"}`, http.StatusBadRequest)
		return
	}

	opts := &auth.LoginOpts{
		TFA:      tfaCode,
		TenantID: tenantID,
		IAMURL:   apiURL,
	}

	log.Printf("login: %s", email)
	session, err := h.iamClient.Login(email, password, opts)
	if err != nil {
		errMsg := "Authentication failed: " + err.Error()
		log.Printf("login failed: %v", err)
		if ct == "application/x-www-form-urlencoded" {
			http.Redirect(w, r, "/login?error="+url.QueryEscape(errMsg), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	}

	// Fetch account
	account, err := h.iamClient.GetAccount(session.Token)
	if err != nil {
		errMsg := "Authentication failed: " + err.Error()
		log.Printf("get account failed: %v", err)
		if ct == "application/x-www-form-urlencoded" {
			http.Redirect(w, r, "/login?error="+url.QueryEscape(errMsg), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	}
	session.GatewayEndpoint = stripProtocol(account.EndpointGateway)

	// Fetch projects and create S3 credentials
	projects, err := h.iamClient.GetProjects(session.Token)
	if err != nil {
		errMsg := "Authentication failed: " + err.Error()
		log.Printf("get projects failed: %v", err)
		if ct == "application/x-www-form-urlencoded" {
			http.Redirect(w, r, "/login?error="+url.QueryEscape(errMsg), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	}

	for _, p := range projects {
		log.Printf("project: %s (%s), users=%d", p.Name, p.ID, len(p.Users))
		for _, u := range p.Users {
			cred := h.reconcileProjectCredential(u.UserID, u.UserName, p.Name)
			if cred != nil {
				session.Projects = append(session.Projects, auth.ProjectCred{
					ProjectID:   p.ID,
					ProjectName: p.Name,
					AccessKey:   cred.ApiKey,
					SecretKey:   cred.SecretKey,
				})
				break
			}
		}
	}

	if len(session.Projects) == 0 {
		errMsg := "Authentication failed: no projects with accessible credentials"
		log.Printf("no projects configured for %s", email)
		if ct == "application/x-www-form-urlencoded" {
			http.Redirect(w, r, "/login?error="+url.QueryEscape(errMsg), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	}

	h.store.Set(session.Token, session)

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	if ct == "application/x-www-form-urlencoded" {
		http.Redirect(w, r, "/browse", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": session.Token})
}

func stripProtocol(u string) string {
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(u, p) {
			return u[len(p):]
		}
	}
	return u
}

func (h *AuthHandler) reconcileProjectCredential(userID, userName, projectName string) *auth.APIKey {
	forgeResp, err := h.iamClient.ForgeJWT(userID)
	if err != nil {
		log.Printf("forge for user %s failed: %v", userName, err)
		return nil
	}

	keyName := auth.S3KeyName(userName, projectName)

	// Always remove existing key first to force a fresh create (so we get the secret)
	existing, err := h.iamClient.ListAPIKeys(userID, forgeResp.Token)
	if err == nil {
		for _, k := range existing {
			if k.Name == keyName {
				h.iamClient.DeleteAPIKey(k.ApiKey, userID, forgeResp.Token)
				log.Printf("deleted stale key %s", keyName)
				break
			}
		}
	}

	// Create new key
	created, err := h.iamClient.CreateAPIKey(keyName, userID, forgeResp.Token)
	if err != nil {
		log.Printf("create key %s failed: %v", keyName, err)
		return nil
	}
	log.Printf("created key %s for %s/%s (secret=%s...)", keyName, userName, projectName, created.SecretKey[:min(8, len(created.SecretKey))])
	return created
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		http.Error(w, `{"error":"refresh_token required"}`, http.StatusBadRequest)
		return
	}

	session, err := h.iamClient.Refresh(req.RefreshToken)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.store.Set(session.Token, session)

	json.NewEncoder(w).Encode(map[string]string{"token": session.Token})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	type projectResp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	projs := make([]projectResp, len(session.Projects))
	for i, p := range session.Projects {
		projs[i] = projectResp{ID: p.ProjectID, Name: p.ProjectName}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"email":    session.Email,
		"projects": projs,
	})
}
