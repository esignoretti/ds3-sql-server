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
