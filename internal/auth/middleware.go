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
			token := ""

			// Try Authorization header first (CLI/API)
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimPrefix(auth, "Bearer ")
			}

			// Fall back to cookie (Web UI)
			if token == "" {
				c, err := r.Cookie("token")
				if err == nil {
					token = c.Value
				}
			}

			if token == "" {
				http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
				return
			}

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
