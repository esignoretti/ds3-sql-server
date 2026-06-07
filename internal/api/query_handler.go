package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type QueryHandler struct {
	engine *query.Engine
	cat    *catalog.Service
}

func NewQueryHandler(engine *query.Engine, cat *catalog.Service) *QueryHandler {
	return &QueryHandler{engine: engine, cat: cat}
}

type queryRequest struct {
	SQL       string `json:"sql"`
	Bucket    string `json:"bucket"`
	ProjectID string `json:"project_id,omitempty"`
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

	// Extract projectID from query param (set by main.go route closure) or body.
	projectID := req.ProjectID
	if projectID == "" {
		projectID = r.URL.Query().Get("project")
	}

	var result *query.Result
	if projectID != "" && h.cat != nil {
		// Resolve catalog table references into DuckDB views before querying.
		bindings, err := h.cat.ResolvePruned(r.Context(), projectID, req.SQL)
		if err != nil {
			result = &query.Result{Error: "resolve tables: " + err.Error()}
		} else if len(bindings) > 0 {
			result = h.engine.QueryView(req.SQL, bindings, accessKey, secretKey, endpoint)
		}
	}
	if result == nil {
		result = h.engine.Query(req.SQL, accessKey, secretKey, endpoint)
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
