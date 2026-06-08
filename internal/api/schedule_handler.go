package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// ScheduleStore is the subset of metastore.Store the handler needs.
type ScheduleStore interface {
	CreateSchedule(ctx context.Context, sch *metastore.Schedule) error
	UpdateSchedule(ctx context.Context, sch *metastore.Schedule) error
	ListSchedules(ctx context.Context, projectID string) ([]*metastore.Schedule, error)
	GetSchedule(ctx context.Context, id string) (*metastore.Schedule, error)
	DeleteSchedule(ctx context.Context, id, projectID string) error
}

type ScheduleHandler struct {
	store ScheduleStore
}

func NewScheduleHandler(store ScheduleStore) *ScheduleHandler {
	return &ScheduleHandler{store: store}
}

var scheduleCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (h *ScheduleHandler) CreateForProject(w http.ResponseWriter, r *http.Request, projectID, owner string) {
	var req struct {
		Cron        string `json:"cron"`
		SQL         string `json:"sql"`
		IntoTable   string `json:"into_table"`
		Type        string `json:"type"`
		Source      string `json:"source"`
		Format      string `json:"format"`
		PostAction  string `json:"post_action"`
		MoveBucket  string `json:"move_bucket"`
		MovePrefix  string `json:"move_prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Cron == "" || (req.SQL == "" && req.Type != "convert") {
		http.Error(w, `{"error":"cron and sql are required"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "convert" && req.Source == "" {
		http.Error(w, `{"error":"source is required for convert schedules"}`, http.StatusBadRequest)
		return
	}
	sched, err := scheduleCronParser.Parse(req.Cron)
	if err != nil {
		http.Error(w, `{"error":"invalid cron expression"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "query"
	}
	now := time.Now().UTC()
	s := &metastore.Schedule{
		ID:         uuid.NewString(),
		ProjectID:  projectID,
		Cron:       req.Cron,
		SQL:        req.SQL,
		IntoTable:  req.IntoTable,
		Owner:      owner,
		Type:       req.Type,
		Source:     req.Source,
		Format:     req.Format,
		PostAction: req.PostAction,
		MoveBucket: req.MoveBucket,
		MovePrefix: req.MovePrefix,
		NextRunAt:  sched.Next(now),
		CreatedAt:  now,
	}
	if err := h.store.CreateSchedule(r.Context(), s); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

func (h *ScheduleHandler) UpdateForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	var req struct {
		Cron        string `json:"cron"`
		SQL         string `json:"sql"`
		IntoTable   string `json:"into_table"`
		Type        string `json:"type"`
		Source      string `json:"source"`
		Format      string `json:"format"`
		PostAction  string `json:"post_action"`
		MoveBucket  string `json:"move_bucket"`
		MovePrefix  string `json:"move_prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Cron == "" || (req.SQL == "" && req.Type != "convert") {
		http.Error(w, `{"error":"cron and sql are required"}`, http.StatusBadRequest)
		return
	}
	sched, err := scheduleCronParser.Parse(req.Cron)
	if err != nil {
		http.Error(w, `{"error":"invalid cron expression"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "query"
	}
	now := time.Now().UTC()
	s := &metastore.Schedule{
		ID:         id,
		ProjectID:  projectID,
		Cron:       req.Cron,
		SQL:        req.SQL,
		IntoTable:  req.IntoTable,
		Type:       req.Type,
		Source:     req.Source,
		Format:     req.Format,
		PostAction: req.PostAction,
		MoveBucket: req.MoveBucket,
		MovePrefix: req.MovePrefix,
		NextRunAt:  sched.Next(now),
	}
	if err := h.store.UpdateSchedule(r.Context(), s); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (h *ScheduleHandler) GetForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	sch, err := h.store.GetSchedule(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	if sch.ProjectID != projectID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sch)
}

func (h *ScheduleHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	list, err := h.store.ListSchedules(r.Context(), projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"schedules": list})
}

func (h *ScheduleHandler) DeleteForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteSchedule(r.Context(), id, projectID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
