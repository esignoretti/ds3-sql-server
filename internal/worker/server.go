package worker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/cache"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// SecretHeader carries the shared secret guarding worker endpoints.
const SecretHeader = "X-DS3SQL-Worker-Secret"

// WireBinding is the JSON-serializable form of a resolved table binding sent
// from coordinator to worker. Objects + StorageClass let the worker localize
// HDD data via its SSD cache before executing.
type WireBinding struct {
	Schema       string            `json:"schema"`
	Name         string            `json:"name"`
	ReaderSQL    string            `json:"reader_sql"`
	StorageClass string            `json:"storage_class"`
	Objects      []cache.ObjectRef `json:"objects,omitempty"`
}

// ExecuteRequest is the resolved plan dispatched to a worker.
type ExecuteRequest struct {
	SQL       string        `json:"sql"`
	Bindings  []WireBinding `json:"bindings"`
	AccessKey string        `json:"access_key"`
	SecretKey string        `json:"secret_key"`
	Endpoint  string        `json:"endpoint"`
}

// Server is the worker data-plane HTTP server.
type Server struct {
	engine *query.Engine
	secret string
	data   *cache.DataCache // optional local-SSD data cache (nil disables)
}

func NewServer(engine *query.Engine, secret string, data *cache.DataCache) *Server {
	return &Server{engine: engine, secret: secret, data: data}
}

// checkSecret validates the shared secret using a constant-time comparison.
// An empty server secret is always rejected.
func (s *Server) checkSecret(r *http.Request) bool {
	if s.secret == "" {
		return false
	}
	got := r.Header.Get(SecretHeader)
	return subtle.ConstantTimeCompare([]byte(s.secret), []byte(got)) == 1
}

// Execute handles POST /internal/execute. It validates the shared secret,
// optionally localizes HDD objects via the data cache, then runs QueryView.
func (s *Server) Execute(w http.ResponseWriter, r *http.Request) {
	if !s.checkSecret(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	bindings := s.resolveBindings(r.Context(), req)
	res := s.engine.QueryView(req.SQL, bindings, req.AccessKey, req.SecretKey, req.Endpoint)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// resolveBindings converts wire bindings to query.ViewBinding, rewriting HDD
// readers through the SSD data cache when one is configured.
func (s *Server) resolveBindings(ctx context.Context, req ExecuteRequest) []query.ViewBinding {
	if s.data != nil {
		cb := make([]cache.Binding, len(req.Bindings))
		for i, b := range req.Bindings {
			cb[i] = cache.Binding{
				Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL,
				StorageClass: b.StorageClass, Objects: b.Objects,
			}
		}
		if rewritten, err := s.data.RewriteBindings(ctx, cb); err == nil {
			out := make([]query.ViewBinding, len(rewritten))
			for i, b := range rewritten {
				out[i] = query.ViewBinding{Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL}
			}
			return out
		}
		// On cache failure, fall through to the original readers.
	}
	out := make([]query.ViewBinding, len(req.Bindings))
	for i, b := range req.Bindings {
		out[i] = query.ViewBinding{Schema: b.Schema, Name: b.Name, ReaderSQL: b.ReaderSQL}
	}
	return out
}
