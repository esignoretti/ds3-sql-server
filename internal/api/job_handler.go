package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/write"
)

type JobHandler struct {
	mgr *job.Manager
}

func NewJobHandler(mgr *job.Manager) *JobHandler {
	return &JobHandler{mgr: mgr}
}

// SubmitWithCreds runs query jobs synchronously (Phase 1 fast-path) and routes
// ctas/load jobs to the async write path (returns 202 + the queued job).
func (h *JobHandler) SubmitWithCreds(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	var req struct {
		Type        string   `json:"type"`
		SQL         string   `json:"sql"`
		Source      string   `json:"source"`
		Into        string   `json:"into"`
		Format      string   `json:"format"`
		PartitionBy []string `json:"partition_by"`
		Mode        string   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Explicit load type.
	if strings.EqualFold(req.Type, "load") {
		if req.Source == "" || req.Into == "" {
			http.Error(w, `{"error":"load requires source and into"}`, http.StatusBadRequest)
			return
		}
		j := h.mgr.Submit(r.Context(), job.ExecRequest{
			Type:      "load",
			ProjectID: projectID,
			AccessKey: accessKey,
			SecretKey: secretKey,
			Endpoint:  endpoint,
			Load: &job.LoadRequest{
				Source: req.Source, Into: req.Into, Format: req.Format,
				PartitionBy: req.PartitionBy, Mode: req.Mode,
			},
		})
		writeJSON(w, http.StatusAccepted, j)
		return
	}

	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}

	// CTAS detected by SQL shape -> async write path.
	if write.IsCTAS(req.SQL) {
		j := h.mgr.Submit(r.Context(), job.ExecRequest{
			Type:      "ctas",
			SQL:       req.SQL,
			ProjectID: projectID,
			AccessKey: accessKey,
			SecretKey: secretKey,
			Endpoint:  endpoint,
		})
		writeJSON(w, http.StatusAccepted, j)
		return
	}

	// Plain query -> synchronous fast-path with wait.
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

	if cur, ok := h.mgr.Get(j.ID); ok {
		j = cur
	}
	if !isTerminal(j.Status) {
		writeJSON(w, http.StatusAccepted, j)
		return
	}
	if j.Status == "failed" {
		writeJSON(w, http.StatusBadRequest, j)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

// GetForProject gets a job by ID after verifying it belongs to the caller's project.
func (h *JobHandler) GetForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	j, ok := h.mgr.Get(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	if j.ProjectID != projectID {
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

// CancelForProject cancels a job after verifying it belongs to the caller's project.
func (h *JobHandler) CancelForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	j, ok := h.mgr.Get(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	if j.ProjectID != projectID {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	if !h.mgr.Cancel(id) {
		http.Error(w, `{"error":"job not found or already finished"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
