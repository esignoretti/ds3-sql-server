package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/column"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
	"github.com/go-chi/chi/v5"
)

type ColumnHandler struct {
	columnStore *column.Store
}

func NewColumnHandler(columnStore *column.Store) *ColumnHandler {
	return &ColumnHandler{columnStore: columnStore}
}

func (h *ColumnHandler) Preview(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	file := r.URL.Query().Get("file")
	linesStr := r.URL.Query().Get("lines")

	if bucket == "" || file == "" {
		http.Error(w, `{"error":"bucket and file are required"}`, http.StatusBadRequest)
		return
	}

	maxLines := 25
	if linesStr != "" {
		if n, err := fmt.Sscanf(linesStr, "%d", &maxLines); err != nil || n != 1 || maxLines < 1 || maxLines > 100 {
			maxLines = 25
		}
	}

	session := auth.GetSession(r)
	projectID := r.URL.Query().Get("project")
	var s3Client *s3.Client
	for _, p := range session.Projects {
		if projectID == "" || p.ProjectID == projectID {
			var err error
			s3Client, err = s3.NewClient(r.Context(), p.AccessKey, p.SecretKey, session.GatewayEndpoint)
			if err != nil {
				http.Error(w, `{"error":"failed to create S3 client"}`, http.StatusInternalServerError)
				return
			}
			break
		}
	}
	if s3Client == nil {
		http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		return
	}

	body, err := s3Client.GetObject(r.Context(), bucket, file)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var lines []string
	for scanner.Scan() && len(lines) < maxLines {
		lines = append(lines, scanner.Text())
	}

	savedConfig := h.columnStore.Match(bucket, file)

	resp := map[string]any{
		"filename":     file,
		"preview_lines": lines,
		"saved_config":  savedConfig,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ColumnHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	var req column.ColumnConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Bucket == "" || req.Pattern == "" {
		http.Error(w, `{"error":"bucket and pattern are required"}`, http.StatusBadRequest)
		return
	}

	if err := h.columnStore.Save(&req); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

func (h *ColumnHandler) ListConfigs(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	configs, err := h.columnStore.List(bucket)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if configs == nil {
		configs = []column.ColumnConfig{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"configs": configs})
}

func (h *ColumnHandler) DeleteConfig(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	pattern := chi.URLParam(r, "pattern")
	if err := h.columnStore.Delete(bucket, pattern); err != nil {
		http.Error(w, `{"error":"config not found"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
