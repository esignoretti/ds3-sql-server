package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
)

type JobHandler struct {
	mgr *job.Manager
}

func NewJobHandler(mgr *job.Manager) *JobHandler {
	return &JobHandler{mgr: mgr}
}

// SubmitWithCreds submits a job. With ?wait=<dur> it blocks up to that duration
// (default 2s) for a synchronous fast-path: if the job finishes in time it is
// returned inline (200); otherwise the job id is returned with 202 for polling.
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

	wait := 2 * time.Second
	if v := r.URL.Query().Get("wait"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}

	j := h.mgr.Submit(r.Context(), job.ExecRequest{
		SQL:       req.SQL,
		ProjectID: projectID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
	})

	// Poll for completion up to the wait window.
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if cur, ok := h.mgr.Get(j.ID); ok && isTerminal(cur.Status) {
			j = cur
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	w.Header().Set("Content-Type", "application/json")
	if cur, ok := h.mgr.Get(j.ID); ok {
		j = cur
	}
	if !isTerminal(j.Status) {
		w.WriteHeader(http.StatusAccepted)
	} else if j.Status == "failed" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(j)
}

func isTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
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

// ListForProject returns recent jobs for the project (query history).
func (h *JobHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	limit := 100
	jobs := h.mgr.List(projectID, limit)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

// Cancel cancels a running/queued job.
func (h *JobHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.mgr.Cancel(id) {
		http.Error(w, `{"error":"job not found or already finished"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
