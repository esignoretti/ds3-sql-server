package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
)

type BucketHandler struct {
	client *s3.Client
}

func NewBucketHandler(client *s3.Client) *BucketHandler {
	return &BucketHandler{client: client}
}

func (h *BucketHandler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.client.ListBuckets(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"buckets": buckets})
}

func (h *BucketHandler) ListObjects(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := r.URL.Query().Get("prefix")

	result, err := h.client.ListObjects(r.Context(), bucket, prefix, "/", 100)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *BucketHandler) ListBucketsHTML(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.client.ListBuckets(r.Context())
	if err != nil {
		w.Write([]byte("<p class='error'>Error loading buckets</p>"))
		return
	}

	for _, b := range buckets {
		w.Write([]byte("<div class='bucket-item' onclick=\"loadPrefix('" + b.Name + "', '')\">📁 " + b.Name + "</div>"))
	}
}
