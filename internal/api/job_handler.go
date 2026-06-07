package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
)

type JobHandler struct {
	mgr *job.Manager
}

func NewJobHandler(mgr *job.Manager) *JobHandler {
	return &JobHandler{mgr: mgr}
}

// SubmitWithCreds runs a query job synchronously (Phase 1 sync fast-path) and
// returns the completed job inline.
func (h *JobHandler) SubmitWithCreds(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	var req struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}
	j := h.mgr.Run(r.Context(), job.ExecRequest{
		SQL:       req.SQL,
		ProjectID: projectID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
	})
	w.Header().Set("Content-Type", "application/json")
	if j.Status == "failed" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(j)
}

func (h *JobHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, ok := h.mgr.Get(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(j)
}
