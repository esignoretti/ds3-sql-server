package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/report"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ReportHandler struct {
	store report.Store
}

func NewReportHandler(store report.Store) *ReportHandler {
	return &ReportHandler{store: store}
}

type reportSaveRequest struct {
	Title        string               `json:"title"`
	SQL          string               `json:"sql"`
	ProjectID    string               `json:"project_id"`
	QueryColumns []report.ColumnInfo  `json:"query_columns"`
	QueryRows    [][]any              `json:"query_rows"`
	Analysis     any                  `json:"analysis"`
	Charts       []report.ChartConfig `json:"charts"`
}

func (h *ReportHandler) List(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.store.List()
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []report.ReportSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reports": summaries})
}

func (h *ReportHandler) Save(w http.ResponseWriter, r *http.Request) {
	var req reportSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	now := time.Now()
	rep := &report.Report{
		ID:           uuid.New().String(),
		CreatedAt:    now,
		UpdatedAt:    now,
		Title:        req.Title,
		SQL:          req.SQL,
		ProjectID:    req.ProjectID,
		QueryColumns: req.QueryColumns,
		QueryRows:    req.QueryRows,
		Analysis:     req.Analysis,
		Charts:       req.Charts,
	}

	if err := h.store.Save(rep); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rep)
}

func (h *ReportHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, err := h.store.Get(id)
	if err != nil {
		http.Error(w, `{"error":"report not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rep)
}

func (h *ReportHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.Delete(id); err != nil {
		http.Error(w, `{"error":"report not found"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
