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
	var tfaCode, tenantID, apiURL string

	// Accept both JSON and form-encoded
	ct := r.Header.Get("Content-Type")
	if ct == "application/x-www-form-urlencoded" {
		r.ParseForm()
		req.Email = r.FormValue("email")
		req.Password = r.FormValue("password")
		tfaCode = r.FormValue("tfa_code")
		tenantID = r.FormValue("tenant_id")
		apiURL = r.FormValue("api_url")
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

	opts := &auth.LoginOpts{
		TFA:      tfaCode,
		TenantID: tenantID,
		IAMURL:   apiURL,
	}

	session, err := h.iamClient.Login(req.Email, req.Password, opts)
	if err != nil {
		// Form POST — redirect back to login with error
		if ct == "application/x-www-form-urlencoded" {
			http.Redirect(w, r, "/login?error=authentication+failed", http.StatusFound)
			return
		}
		// HTMX — return error in HTML
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`<div class="error-message">Authentication failed. Check your credentials.</div>`))
			return
		}
		// API — JSON
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "authentication failed"})
		return
	}

	h.store.Set(session.Token, session)

	resp := loginResponse{
		Token:        session.Token,
		RefreshToken: session.RefreshToken,
		ExpiresAt:    session.ExpiresAt.Format("2006-01-02T15:04:05Z"),
	}

	// Set cookie for browser/HTMX requests
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	// Standard form POST (from login page)
	if ct == "application/x-www-form-urlencoded" {
		http.Redirect(w, r, "/browse", http.StatusFound)
		return
	}

	// HTMX requests get a redirect header
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/browse")
		w.WriteHeader(http.StatusOK)
		return
	}

	// API/CLI get JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/login", http.StatusFound)
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
		http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
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

	resp := struct {
		Email           string `json:"email"`
		GatewayEndpoint string `json:"gateway_endpoint"`
	}{
		Email:           session.Email,
		GatewayEndpoint: session.GatewayEndpoint,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
