# DS3 SQL Server — Phase 5: Query & Schema API Handlers

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the DuckDB engine into HTTP handlers for `POST /query` and `POST /schema`. These are the core query endpoints that power both CLI and Web UI.

**Architecture:** Handlers extract S3 credentials from the auth session, create a per-request DuckDB engine, execute the query, and return JSON. Errors are returned as structured JSON with `elapsed_ms`.

**Tech Stack:** Go 1.22+, chi router

---

### Task 1: Query and Schema HTTP handlers

**Files:**
- Create: `DS3-SQL Server/internal/api/query_handler.go`
- Create: `DS3-SQL Server/internal/api/schema_handler.go`

- [ ] **Step 1: Write the query handler**

`DS3-SQL Server/internal/api/query_handler.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type QueryHandler struct {
	engine *query.Engine
}

func NewQueryHandler(engine *query.Engine) *QueryHandler {
	return &QueryHandler{engine: engine}
}

type queryRequest struct {
	SQL    string `json:"sql"`
	Bucket string `json:"bucket"`
}

func (h *QueryHandler) Query(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.SQL == "" {
		http.Error(w, `{"error":"sql field is required"}`, http.StatusBadRequest)
		return
	}

	result := h.engine.Query(req.SQL, session.AccessKey, session.SecretKey, session.GatewayEndpoint)

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
```

- [ ] **Step 2: Write the schema handler**

`DS3-SQL Server/internal/api/schema_handler.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

type SchemaHandler struct {
	engine *query.Engine
}

func NewSchemaHandler(engine *query.Engine) *SchemaHandler {
	return &SchemaHandler{engine: engine}
}

type schemaRequest struct {
	Path string `json:"path"`
}

func (h *SchemaHandler) InferSchema(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	var req schemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, `{"error":"path field is required"}`, http.StatusBadRequest)
		return
	}

	result := h.engine.InferSchema(req.Path, session.AccessKey, session.SecretKey, session.GatewayEndpoint)

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
```

- [ ] **Step 3: Wire into main.go**

Add to the protected route group in `cmd/ds3sql-server/main.go`:

```go
// Query engine
queryEngine := query.NewEngine(
	cfg.Query.MaxRows,
	cfg.Query.MaxExecutionSecs,
	cfg.Query.MaxResultBytes,
)
queryHandler := api.NewQueryHandler(queryEngine)
schemaHandler := api.NewSchemaHandler(queryEngine)

// ... inside r.Group with auth middleware ...
r.Post("/query", queryHandler.Query)
r.Post("/schema", schemaHandler.InferSchema)
```

Full protected group should now look like:

```go
r.Group(func(r chi.Router) {
	r.Use(auth.Middleware(sessionStore))

	r.Get("/auth/me", authHandler.Me)

	r.Get("/buckets", func(w http.ResponseWriter, r *http.Request) {
		session := auth.GetSession(r)
		client, err := s3client.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
		if err != nil {
			http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
			return
		}
		api.NewBucketHandler(client).ListBuckets(w, r)
	})

	r.Get("/buckets/{bucket}", func(w http.ResponseWriter, r *http.Request) {
		session := auth.GetSession(r)
		client, err := s3client.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
		if err != nil {
			http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
			return
		}
		api.NewBucketHandler(client).ListObjects(w, r)
	})

	r.Post("/query", queryHandler.Query)
	r.Post("/schema", schemaHandler.InferSchema)
})
```

- [ ] **Step 4: Update imports and build**

Ensure `cmd/ds3sql-server/main.go` imports:

```go
import (
	...
	"github.com/esignoretti/ds3-sql-server/internal/query"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
	s3client "github.com/esignoretti/ds3-sql-server/internal/s3" // alias to avoid conflict with s3 package from aws
)
```

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql-server/
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: query and schema API handlers"
```
