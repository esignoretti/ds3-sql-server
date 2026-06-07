package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

type TableHandler struct {
	cat *catalog.Service
}

func NewTableHandler(cat *catalog.Service) *TableHandler {
	return &TableHandler{cat: cat}
}

func (h *TableHandler) RegisterForProject(w http.ResponseWriter, r *http.Request, projectID, accessKey, secretKey, endpoint string) {
	dataset := chi.URLParam(r, "dataset")
	var req struct {
		Name             string   `json:"name"`
		Location         string   `json:"location"`
		Format           string   `json:"format"`
		StorageClass     string   `json:"storage_class"`
		PartitionColumns []string `json:"partition_columns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Location == "" {
		http.Error(w, `{"error":"name and location are required"}`, http.StatusBadRequest)
		return
	}
	if req.Format == "" {
		req.Format = "parquet"
	}
	tbl, err := h.cat.RegisterTable(r.Context(), catalog.RegisterTableInput{
		ProjectID:        projectID,
		Dataset:          dataset,
		Name:             req.Name,
		Location:         req.Location,
		Format:           req.Format,
		StorageClass:     req.StorageClass,
		PartitionColumns: req.PartitionColumns,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(tbl)
}

func (h *TableHandler) ListForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	tables, err := h.cat.ListTables(r.Context(), projectID, dataset)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tables": tables})
}

func (h *TableHandler) DescribeForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	tbl, err := h.cat.GetTable(r.Context(), projectID, dataset, name)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tbl)
}

// DropWithDeps drops a table; for managed tables it deletes the underlying data
// via the deleter and invalidates dependent result-cache entries.
func (h *TableHandler) DropWithDeps(w http.ResponseWriter, r *http.Request, projectID string, deleter catalog.PrefixDeleter, cache catalog.CacheInvalidator, accessKey, secretKey, endpoint string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	if err := h.cat.DropTableWithData(r.Context(), projectID, dataset, name, deleter, cache, accessKey, secretKey, endpoint); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TableHandler) DropForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	dataset := chi.URLParam(r, "dataset")
	name := chi.URLParam(r, "table")
	if err := h.cat.DropTable(r.Context(), projectID, dataset, name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, metastore.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
