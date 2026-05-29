package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/convert"
	"github.com/go-chi/chi/v5"
)

type ConvertHandler struct {
	engine *convert.Engine
}

func NewConvertHandler(engine *convert.Engine) *ConvertHandler {
	return &ConvertHandler{engine: engine}
}

func (h *ConvertHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bucket         string   `json:"bucket"`
		Files          []string `json:"files"`
		DeleteOriginal bool     `json:"delete_original"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Bucket == "" {
		http.Error(w, `{"error":"bucket is required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Files) == 0 {
		http.Error(w, `{"error":"at least one file is required"}`, http.StatusBadRequest)
		return
	}

	session := auth.GetSession(r)
	projectID := r.URL.Query().Get("project")
	for _, p := range session.Projects {
		if projectID == "" || p.ProjectID == projectID {
			job, err := h.engine.Start(convert.ConvertRequest{
				Bucket:         req.Bucket,
				Files:          req.Files,
				DeleteOriginal: req.DeleteOriginal,
				Endpoint:       session.GatewayEndpoint,
				AccessKey:      p.AccessKey,
				SecretKey:      p.SecretKey,
			})
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"job_id": job.ID,
				"total":  job.Total,
				"status": job.Status,
			})
			return
		}
	}
	http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
}

func (h *ConvertHandler) Status(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	job, ok := h.engine.JobStore().Get(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"job_id":    job.ID,
		"total":     job.Total,
		"completed": job.Completed,
		"status":    job.Status,
		"results":   job.Results,
	})
}
