package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type SchemaHandler struct {
	engine *query.Engine
}

func NewSchemaHandler(engine *query.Engine) *SchemaHandler {
	return &SchemaHandler{engine: engine}
}

type schemaRequest struct {
	Path string `json:"path"`
}

func (h *SchemaHandler) InferSchema(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	var req schemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, `{"error":"path field is required"}`, http.StatusBadRequest)
		return
	}

	result := h.engine.InferSchema(req.Path, session.AccessKey, session.SecretKey, session.GatewayEndpoint)

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
