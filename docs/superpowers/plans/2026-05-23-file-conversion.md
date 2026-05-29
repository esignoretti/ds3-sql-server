# File Conversion to Parquet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add server-side file conversion from unsupported formats (.log, .txt, .syslog, Apache logs, JSON logs) to Parquet, with parallel execution and a browser UI.

**Architecture:** A new `internal/convert/` package uses DuckDB's httpfs extension to read source files from S3, parse them with format-specific config, and `COPY` the result to a `.parquet` file on the same bucket. A worker pool (default 4 goroutines) converts files in parallel. Results are tracked in an in-memory job store with polling progress. The browse page UI shows convertible files in red with checkboxes and a progress panel.

**Tech Stack:** Go 1.26, DuckDB, AWS SDK v2, chi, HTMX, vanilla JS

---

### Task 1: S3 Client – Add DeleteObject method

**Files:**
- Modify: `internal/s3/client.go`

- [ ] **Step 1: Add DeleteObject method**

Add after the `NewClient` function:

```go
import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := c.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	return err
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/s3/
```

- [ ] **Step 3: Commit**

```bash
git add internal/s3/client.go
git commit -m "feat: add DeleteObject method to s3.Client"
```

---

### Task 2: Conversion Engine – Job store

**Files:**
- Create: `internal/convert/job.go`

- [ ] **Step 1: Create job.go**

```go
package convert

import (
	"sync"
	"time"
)

type FileResult struct {
	File      string `json:"file"`
	Status    string `json:"status"`
	Converted string `json:"converted,omitempty"`
	Error     string `json:"error,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type Job struct {
	ID        string       `json:"id"`
	Bucket    string       `json:"bucket"`
	Total     int          `json:"total"`
	Completed int          `json:"completed"`
	Status    string       `json:"status"`
	Results   []FileResult `json:"results"`
	CreatedAt time.Time    `json:"created_at"`
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
}

func NewJobStore() *JobStore {
	return &JobStore{
		jobs: make(map[string]*Job),
	}
}

func (s *JobStore) Set(id string, job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = job
}

func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *JobStore) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, job := range s.jobs {
		if now.Sub(job.CreatedAt) > maxAge {
			delete(s.jobs, id)
		}
	}
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/convert/
```

- [ ] **Step 3: Commit**

```bash
git add internal/convert/job.go
git commit -m "feat: add conversion job store"
```

---

### Task 3: Conversion Engine – Core engine with format detection and DuckDB conversion

**Files:**
- Create: `internal/convert/engine.go`
- Create: `internal/convert/engine_test.go`

- [ ] **Step 1: Create engine.go**

```go
package convert

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ConvertRequest struct {
	Bucket         string   `json:"bucket"`
	Files          []string `json:"files"`
	DeleteOriginal bool     `json:"delete_original"`
	Endpoint       string   `json:"-"`
	AccessKey      string   `json:"-"`
	SecretKey      string   `json:"-"`
}

type Engine struct {
	pool    chan *sql.DB
	workers int
	jobs    *JobStore
}

func NewEngine(pool chan *sql.DB, workers int) *Engine {
	if workers < 1 {
		workers = 1
	}
	return &Engine{
		pool:    pool,
		workers: workers,
		jobs:    NewJobStore(),
	}
}

func (e *Engine) JobStore() *JobStore {
	return e.jobs
}

func (e *Engine) Start(req ConvertRequest) (*Job, error) {
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("no files provided")
	}

	jobID := uuid.New().String()
	results := make([]FileResult, len(req.Files))
	for i, f := range req.Files {
		results[i] = FileResult{File: f, Status: "pending"}
	}

	job := &Job{
		ID:        jobID,
		Bucket:    req.Bucket,
		Total:     len(req.Files),
		Status:    "running",
		Results:   results,
		CreatedAt: time.Now(),
	}
	e.jobs.Set(jobID, job)

	// Launch workers
	go e.run(job, req)

	return job, nil
}

func (e *Engine) run(job *Job, req ConvertRequest) {
	files := make(chan int, len(req.Files))
	for i := range req.Files {
		files <- i
	}
	close(files)

	var wg sync.WaitGroup
	for w := 0; w < e.workers; w++ {
		wg.Add(1)
		go e.worker(&wg, job, req, files)
	}
	wg.Wait()

	// Check if all done or any errored
	job.mu.Lock()
	allDone := true
	for _, r := range job.Results {
		if r.Status == "error" {
			job.Status = "error"
			allDone = false
			break
		}
		if r.Status != "done" {
			allDone = false
		}
	}
	if allDone {
		job.Status = "done"
	}
	job.mu.Unlock()
}
```

- [ ] **Step 2: Add format detection + worker + helper to engine.go**

Add these to the same file:

```go
func detectFormat(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".syslog") || strings.HasSuffix(lower, ".syslog.1"):
		return "syslog"
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonl"):
		return "json"
	case strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".out") || strings.HasSuffix(lower, ".err"):
		return "text"
	default:
		return "text"
	}
}

func convertibleExt(ext string) bool {
	lower := strings.ToLower(ext)
	switch lower {
	case ".parquet", ".csv", ".tsv", ".json", ".jsonl":
		return false
	default:
		return true
	}
}

func (e *Engine) worker(wg *sync.WaitGroup, job *Job, req ConvertRequest, files chan int) {
	defer wg.Done()

	for idx := range files {
		file := req.Files[idx]

		// Update status to running
		job.mu.Lock()
		job.Results[idx].Status = "running"
		job.mu.Unlock()

		start := time.Now()
		err := e.convertFile(file, req.Bucket, req.Endpoint, req.AccessKey, req.SecretKey)
		elapsed := time.Since(start).Milliseconds()

		job.mu.Lock()
		job.Results[idx].ElapsedMs = elapsed
		if err != nil {
			job.Results[idx].Status = "error"
			job.Results[idx].Error = err.Error()
		} else {
			job.Results[idx].Status = "done"
			job.Results[idx].Converted = file + ".parquet"
			job.Completed++

			// Optionally delete original
			if req.DeleteOriginal {
				delErr := e.deleteOriginal(req.Bucket, file, req.Endpoint, req.AccessKey, req.SecretKey)
				if delErr != nil {
					job.Results[idx].Error = "converted but delete failed: " + delErr.Error()
				}
			}
		}
		job.mu.Unlock()
	}
}
```

- [ ] **Step 3: Add the convertFile method**

```go
func (e *Engine) convertFile(file, bucket, endpoint, accessKey, secretKey string) error {
	db := <-e.pool
	defer func() { e.pool <- db }()

	// Set S3 credentials
	useSSL := true
	rawEndpoint := endpoint
	if idx := strings.Index(rawEndpoint, "://"); idx >= 0 {
		useSSL = strings.HasPrefix(rawEndpoint[:idx], "https")
		rawEndpoint = rawEndpoint[idx+3:]
	}
	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}

	db.Exec("CREATE OR REPLACE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + rawEndpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')")

	s3Path := "s3://" + bucket + "/" + file
	outputPath := "s3://" + bucket + "/" + file + ".parquet"
	fmt := detectFormat(file)

	var readSQL string
	switch fmt {
	case "syslog":
		readSQL = fmt.Sprintf(`
			SELECT columns[1] AS month, columns[2] AS day, columns[3] AS time,
			       columns[4] AS host, columns[5] AS app,
			       CASE WHEN columns[6] LIKE '%%[%%' THEN columns[6] END AS pid,
			       CASE WHEN columns[6] LIKE '%%[%%' THEN regexp_extract(line, '\\[([^\\]]+)\\]', 1) ELSE '' END AS pid_clean,
			       columns[array_length(columns)] AS message
			FROM read_csv('%s', AUTO_DETECT=FALSE, DELIM=' ', HEADER=FALSE)
		`, s3Path)
	case "json":
		readSQL = fmt.Sprintf("SELECT * FROM read_json_auto('%s')", s3Path)
	default:
		readSQL = fmt.Sprintf("SELECT * FROM read_csv_auto('%s', HEADER=FALSE)", s3Path)
	}

	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (FORMAT PARQUET)", readSQL, outputPath)

	if _, err := db.Exec(copySQL); err != nil {
		return fmt.Errorf("convert %s: %w", file, err)
	}

	return nil
}

func (e *Engine) deleteOriginal(bucket, file, endpoint, accessKey, secretKey string) error {
	// Reuse S3 client to delete the original file
	ctx := context.Background()
	client, err := s3.NewClient(ctx, accessKey, secretKey, endpoint)
	if err != nil {
		return fmt.Errorf("create s3 client: %w", err)
	}
	return client.DeleteObject(ctx, bucket, file)
}
```

Make sure to add `"context"` and `"github.com/esignoretti/ds3-sql-server/internal/s3"` to the imports.
```

- [ ] **Step 4: Add missing import for sync**

Add `"sync"` to the imports in engine.go.

- [ ] **Step 5: Add Job.mu field**

Add a `mu sync.Mutex` field to the `Job` struct in `job.go`:

```go
type Job struct {
	mu        sync.Mutex
	ID        string       `json:"-"`
	Bucket    string       `json:"bucket"`
	Total     int          `json:"total"`
	Completed int          `json:"completed"`
	Status    string       `json:"status"`
	Results   []FileResult `json:"results"`
	CreatedAt time.Time    `json:"created_at"`
}
```

Note: `ID` and `mu` are `json:"-"` so they don't appear in API responses.

- [ ] **Step 6: Create engine_test.go**

```go
package convert

import (
	"testing"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"server.log", "text"},
		{"auth.syslog", "syslog"},
		{"access.log", "text"},
		{"data.json", "json"},
		{"data.jsonl", "json"},
		{"output.txt", "text"},
		{"error.out", "text"},
		{"crash.err", "text"},
	}
	for _, tt := range tests {
		got := detectFormat(tt.filename)
		if got != tt.expected {
			t.Errorf("detectFormat(%q) = %q, want %q", tt.filename, got, tt.expected)
		}
	}
}

func TestConvertibleExt(t *testing.T) {
	if convertibleExt(".parquet") {
		t.Error(".parquet should not be convertible")
	}
	if convertibleExt(".csv") {
		t.Error(".csv should not be convertible")
	}
	if !convertibleExt(".log") {
		t.Error(".log should be convertible")
	}
	if !convertibleExt(".txt") {
		t.Error(".txt should be convertible")
	}
	if !convertibleExt(".syslog") {
		t.Error(".syslog should be convertible")
	}
}
```

- [ ] **Step 7: Run tests**

```bash
go test -v ./internal/convert/
```

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/convert/
git commit -m "feat: conversion engine with format detection and DuckDB COPY to Parquet"
```

---

### Task 4: API Handler – /convert and /convert/status

**Files:**
- Create: `internal/api/convert_handler.go`

- [ ] **Step 1: Create convert_handler.go**

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/convert"
	"github.com/go-chi/chi/v5"
)

type ConvertHandler struct {
	engine *convert.Engine
}

func NewConvertHandler(engine *convert.Engine) *ConvertHandler {
	return &ConvertHandler{engine: engine}
}

type convertRequest struct {
	Bucket         string   `json:"bucket"`
	Files          []string `json:"files"`
	DeleteOriginal bool     `json:"delete_original"`
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

	// Build ConvertRequest with credentials from session
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
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/api/
```

Note: you may need to add `"github.com/esignoretti/ds3-sql-server/internal/auth"` and `auth.GetSession` if not already usable from context. The auth package already exports `GetSession` and is importable.

- [ ] **Step 3: Commit**

```bash
git add internal/api/convert_handler.go
git commit -m "feat: /convert and /convert/status/{id} API handlers"
```

---

### Task 5: Wire conversion engine and routes in main.go

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

- [ ] **Step 1: Add conversion engine init**

After the analysis engine block, add:

```go
	// Conversion engine
	workers := 4
	if cfg.Query.PoolSize < workers {
		workers = cfg.Query.PoolSize
	}
	convertEngine := convert.NewEngine(queryEngine.Pool(), workers)
	convertHandler := api.NewConvertHandler(convertEngine)
```

Add import: `"github.com/esignoretti/ds3-sql-server/internal/convert"`

- [ ] **Step 2: Add routes in the no-timeout group**

After the report routes:

```go
		r.Post("/convert", convertHandler.Start)
		r.Get("/convert/status/{id}", convertHandler.Status)
```

- [ ] **Step 3: Add job store cleanup goroutine**

In `main()`, after the graceful shutdown section or after initializing convertEngine:

```go
	// Job store cleanup every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				convertEngine.JobStore().Cleanup(30 * time.Minute)
			case <-done:
				return
			}
		}
	}()
```

- [ ] **Step 4: Verify compile**

```bash
go build ./cmd/ds3sql-server/
```

- [ ] **Step 5: Commit**

```bash
git add cmd/ds3sql-server/main.go
git commit -m "feat: wire conversion engine and routes in main.go"
```

---

### Task 6: Web UI – Browse page file listing with convertible files in red + conversion controls

**Files:**
- Modify: `internal/web/templates/browse.html`
- Modify: `internal/web/static/style.css`

- [ ] **Step 1: Update loadPrefix to separate convertible files**

Replace the section in `loadPrefix` that handles `supported` and `others` (lines 118-131) with three categories:

```javascript
var convertible = d.objects.filter(function(o) { var l = o.key.toLowerCase(); return l.endsWith('.log') || l.endsWith('.txt') || l.endsWith('.syslog') || l.endsWith('.out') || l.endsWith('.err'); });
var supported = d.objects.filter(function(o) { var l = o.key.toLowerCase(); return l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv'); });
var otherFiles = d.objects.filter(function(o) { var l = o.key.toLowerCase(); return !convertible.includes(o) && !supported.includes(o); });
```

Then render:

```javascript
if (convertible.length) {
  html += '<div style="margin-top:0.5rem;font-size:0.85rem;color:var(--red);font-weight:600;">Convertible files — select to convert:</div>';
  html += '<div id="convertible-list">';
  convertible.forEach(function(o) {
    html += '<label class="convert-item" style="display:flex;align-items:center;gap:0.375rem;padding:0.25rem 0.5rem;font-size:0.85rem;color:#f87171;cursor:pointer;">';
    html += '<input type="checkbox" class="convert-checkbox" data-file="' + escAttr(o.key) + '" onchange="updateConvertBtn()">';
    html += '⚠️ ' + escHtml(o.key.split('/').pop()) + ' <span style="color:var(--text-muted);font-size:0.75rem;">' + fmtSize(o.size) + '</span>';
    html += '</label>';
  });
  html += '</div>';
  html += '<div id="convert-controls" style="margin-top:0.5rem;display:none;gap:0.5rem;align-items:center;flex-wrap:wrap;">';
  html += '<button class="btn" style="font-size:0.8rem;padding:0.25rem 0.75rem;" onclick="startConvert()">⬇ Convert to Parquet</button>';
  html += '<label style="font-size:0.8rem;color:var(--text-muted);display:flex;align-items:center;gap:0.25rem;"><input type="checkbox" id="delete-original"> Delete original after conversion</label>';
  html += '</div>';
}
```

Keep existing `supported` section unchanged)Skip the `otherFiles` section (collapsed in details, opacity 0.5) unchanged.

- [ ] **Step 2: Add updateConvertBtn and startConvert functions**

Add to the script section:

```javascript
function updateConvertBtn() {
  var checked = document.querySelectorAll('.convert-checkbox:checked');
  var ctrl = document.getElementById('convert-controls');
  ctrl.style.display = checked.length ? 'flex' : 'none';
}

function startConvert() {
  var checked = document.querySelectorAll('.convert-checkbox:checked');
  if (!checked.length) return;
  var files = Array.from(checked).map(function(cb) { return cb.getAttribute('data-file'); });
  var deleteOrig = document.getElementById('delete-original').checked;

  // Show progress panel
  var html = '<div class="convert-progress" id="convert-progress">';
  html += '<div class="card"><h3>Converting to Parquet</h3>';
  html += '<div id="convert-status"><p style="color:var(--text-muted);">Starting conversion...</p></div>';
  html += '</div></div>';
  document.getElementById('browser-content').innerHTML += html;

  fetch('/convert?project=' + encodeURIComponent(selProject), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({bucket: selBucket, files: files, delete_original: deleteOrig})
  })
  .then(function(r) { return r.json(); })
  .then(function(job) {
    pollConvertStatus(job.job_id);
  })
  .catch(function(e) {
    document.getElementById('convert-status').innerHTML = '<span class="error">Error: ' + e.message + '</span>';
  });
}

function pollConvertStatus(jobId) {
  fetch('/convert/status/' + encodeURIComponent(jobId))
    .then(function(r) { return r.json(); })
    .then(function(job) {
      var html = '<div style="margin-bottom:0.75rem;">';
      var pct = job.total > 0 ? Math.round(job.completed / job.total * 100) : 0;
      html += '<div style="display:flex;align-items:center;gap:0.75rem;margin-bottom:0.5rem;">';
      html += '<div style="flex:1;height:8px;background:var(--surface-2);border-radius:4px;overflow:hidden;">';
      html += '<div style="height:100%;width:' + pct + '%;background:var(--primary);border-radius:4px;transition:width 0.5s;"></div></div>';
      html += '<span style="font-size:0.85rem;color:var(--text-muted);">' + job.completed + '/' + job.total + '</span>';
      html += '</div>';
      html += '<table style="font-size:0.8rem;"><thead><tr><th>File</th><th>Status</th><th>Time</th></tr></thead><tbody>';
      job.results.forEach(function(r) {
        var icon = r.status === 'done' ? '✅' : r.status === 'error' ? '❌' : r.status === 'running' ? '⏳' : '⬜';
        html += '<tr><td>' + escHtml(r.file) + '</td><td>' + icon + ' ' + r.status + (r.error ? ': ' + escHtml(r.error) : '') + '</td><td>' + (r.elapsed_ms ? r.elapsed_ms + 'ms' : '-') + '</td></tr>';
      });
      html += '</tbody></table>';
      html += '</div>';
      if (job.status === 'running') {
        html += '<p style="font-size:0.8rem;color:var(--text-muted);">Running... <a href="#" onclick="event.preventDefault();location.reload()">Refresh</a> when done.</p>';
      } else {
        html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.25rem 0.75rem;" onclick="location.reload()">Done — Refresh file list</button>';
      }
      document.getElementById('convert-status').innerHTML = html^M
      if (job.status === 'running') {
        setTimeout(function() { pollConvertStatus(jobId); }, 2000);
      }
    })
    .catch(function(e) {
      document.getElementById('convert-status').innerHTML = '<span class="error">Poll error: ' + e.message + '</span>';
    });
}
```

Note: remove the trailing `^M` character if present. Use clean line endings.

Also ensure `selBucket` is available in the browse scope (it already is — initialized at the top as `var selBucket = '';`).

- [ ] **Step 3: Add convertible files extensions to the existing convertibleExt check in the render**

The current filter for supported files uses these extensions. Keep it as is. The new `convertible` filter adds `.log`, `.txt`, `.syslog`, `.out`, `.err`.

- [ ] **Step 4: Add CSS**

Add to `style.css`:

```css
.convert-item:hover { background:var(--surface-2); border-radius:var(--radius); }
.convert-progress { margin-top:1rem; }
```

- [ ] **Step 5: Verify compile**

```bash
go build ./internal/web/ && go build ./cmd/ds3sql-server/
```

- [ ] **Step 6: Commit**

```bash
git add internal/web/
git commit -m "feat: add convertible files UI with red styling and conversion controls"
```

---

### Task 7: Final build and verify

- [ ] **Step 1: Run tests**

```bash
go test -v -count=1 ./internal/convert/ ./internal/s3/ ./internal/query/ ./internal/report/ ./internal/analysis/ ./internal/auth/
```

Expected: all pass

- [ ] **Step 2: Build**

```bash
make build
```

Expected: `ds3sql-server` and `ds3sql` binaries

- [ ] **Step 3: Push**

```bash
git push origin main
```
