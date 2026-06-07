package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
)

type DatasetHandler struct {
	cat *catalog.Service
}

func NewDatasetHandler(cat *catalog.Service) *DatasetHandler {
	return &DatasetHandler{cat: cat}
}

func (h *DatasetHandler) CreateForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if err := h.cat.CreateDataset(r.Context(), projectID, req.Name); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"name": req.Name})
}

func (h *DatasetHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	datasets, err := h.cat.ListDatasets(r.Context(), projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"datasets": datasets})
}
