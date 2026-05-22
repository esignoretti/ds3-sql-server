package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type QueryHandler struct {
	engine *query.Engine
}

func NewQueryHandler(engine *query.Engine) *QueryHandler {
	return &QueryHandler{engine: engine}
}

type queryRequest struct {
	SQL    string `json:"sql"`
	Bucket string `json:"bucket"`
}

func (h *QueryHandler) QueryWithCreds(w http.ResponseWriter, r *http.Request, accessKey, secretKey, endpoint string) {
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}

	result := h.engine.Query(req.SQL, accessKey, secretKey, endpoint)

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
