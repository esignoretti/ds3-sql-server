# Column Configuration for Log Conversion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add UI and backend for previewing log files and manually defining column names, delimiter, and quote character before conversion. Configs are saved per bucket+file-pattern and reused automatically.

**Architecture:** A new `internal/column/` package stores column configs as JSON files on disk (`~/.ds3sql/columns/`). A preview API endpoint reads first 25 lines from S3 and returns them for live editing in the browser. The conversion engine checks for saved configs before falling back to auto-detect. The browse page adds a "Configure" button per convertible file pattern.

**Tech Stack:** Go 1.26, DuckDB, AWS SDK v2, chi, vanilla JS

---

### Task 1: Column config store

**Files:**
- Create: `internal/column/config.go`
- Create: `internal/column/config_test.go`

- [ ] **Step 1: Create config.go**

```go
package column

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ColumnDef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ColumnConfig struct {
	Bucket    string       `json:"bucket"`
	Pattern   string       `json:"pattern"`
	Delimiter string       `json:"delimiter"`
	Quote     string       `json:"quote"`
	HeaderRow bool         `json:"header_row"`
	Columns   []ColumnDef  `json:"columns"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type Store struct {
	baseDir string
}

func NewStore(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create column config dir: %w", err)
	}
	return &Store{baseDir: baseDir}, nil
}

func (s *Store) configPath(bucket string) string {
	dir := filepath.Join(s.baseDir, sanitizePath(bucket))
	os.MkdirAll(dir, 0755)
	return dir
}

func sanitizePath(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_").Replace(s)
}

func (s *Store) filePath(bucket, pattern string) string {
	name := sanitizePath(pattern)
	return filepath.Join(s.configPath(bucket), name+".json")
}

func (s *Store) Save(cfg *ColumnConfig) error {
	cfg.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal column config: %w", err)
	}
	if err := os.WriteFile(s.filePath(cfg.Bucket, cfg.Pattern), data, 0644); err != nil {
		return fmt.Errorf("write column config: %w", err)
	}
	return nil
}

func (s *Store) Get(bucket, pattern string) (*ColumnConfig, error) {
	data, err := os.ReadFile(s.filePath(bucket, pattern))
	if err != nil {
		return nil, fmt.Errorf("read column config: %w", err)
	}
	var cfg ColumnConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse column config: %w", err)
	}
	return &cfg, nil
}

func (s *Store) List(bucket string) ([]ColumnConfig, error) {
	dir := s.configPath(bucket)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read column config dir: %w", err)
	}
	var configs []ColumnConfig
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var cfg ColumnConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

func (s *Store) Delete(bucket, pattern string) error {
	return os.Remove(s.filePath(bucket, pattern))
}

// Match finds the best matching column config for a filename.
// Most specific pattern (longest) wins.
func (s *Store) Match(bucket, filename string) *ColumnConfig {
	configs, err := s.List(bucket)
	if err != nil || len(configs) == 0 {
		return nil
	}
	// Sort by pattern length descending (most specific first)
	sort.Slice(configs, func(i, j int) bool {
		return len(configs[i].Pattern) > len(configs[j].Pattern)
	})
	for _, cfg := range configs {
		matched, _ := filepath.Match(cfg.Pattern, filepath.Base(filename))
		if matched {
			return &cfg
		}
	}
	return nil
}
```

- [ ] **Step 2: Create config_test.go**

```go
package column

import (
	"os"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ColumnConfig{
		Bucket:    "test-logs",
		Pattern:   "*.log",
		Delimiter: " ",
		Quote:     "\"",
		Columns:   []ColumnDef{{Name: "ip", Type: "VARCHAR"}, {Name: "request", Type: "VARCHAR"}},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get("test-logs", "*.log")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(loaded.Columns))
	}

	list, err := store.List("test-logs")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 config, got %d", len(list))
	}

	if err := store.Delete("test-logs", "*.log"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("test-logs", "*.log"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMatch(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	store.Save(&ColumnConfig{Bucket: "logs", Pattern: "*.log", Delimiter: " "})
	store.Save(&ColumnConfig{Bucket: "logs", Pattern: "apache_*.log", Delimiter: " "})

	// Should match the more specific pattern
	cfg := store.Match("logs", "apache_access.log")
	if cfg == nil {
		t.Fatal("expected match")
	}
	if cfg.Pattern != "apache_*.log" {
		t.Fatalf("expected 'apache_*.log', got %s", cfg.Pattern)
	}

	// Should match *.log as fallback
	cfg2 := store.Match("logs", "syslog.log")
	if cfg2 == nil {
		t.Fatal("expected match")
	}

	// No match for unknown bucket
	cfg3 := store.Match("other", "test.log")
	if cfg3 != nil {
		t.Fatal("expected no match")
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test -v ./internal/column/
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/column/
git commit -m "feat: column config store with saved patterns and matching"
```

---

### Task 2: Preview endpoint – read first N lines from S3

**Files:**
- Create: `internal/api/column_handler.go`

- [ ] **Step 1: Add GetObject method to s3.Client**

Add to `internal/s3/client.go`:

```go
func (c *Client) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := c.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}
```

Add `"fmt"` and `"io"` to imports, and `awss3 "github.com/aws/aws-sdk-go-v2/service/s3"` if not already imported.

- [ ] **Step 2: Create column_handler.go**

```go
package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/column"
	"github.com/esignoretti/ds3-sql-server/internal/convert"
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
```

- [ ] **Step 3: Verify compile**

```bash
go build ./internal/api/ && go build ./internal/s3/
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/column_handler.go internal/s3/client.go
git commit -m "feat: column config API with S3 object read and preview endpoint"
```

---

### Task 3: Integrate column config into conversion engine

**Files:**
- Modify: `internal/convert/engine.go`
- Modify: `internal/api/convert_handler.go`

- [ ] **Step 1: Add ColumnStore reference to Engine**

In `engine.go`, add `colStore *column.Store` field and update `NewEngine`:

```go
type Engine struct {
	pool     chan *sql.DB
	workers  int
	jobs     *JobStore
	colStore *column.Store
}

func NewEngine(pool chan *sql.DB, workers int, colStore *column.Store) *Engine {
	poolSize := cap(pool)
	if workers < 1 {
		workers = 1
	}
	if workers > poolSize {
		workers = poolSize
	}
	return &Engine{
		pool:     pool,
		workers:  workers,
		jobs:     NewJobStore(),
		colStore: colStore,
	}
}
```

Add import: `"github.com/esignoretti/ds3-sql-server/internal/column"`

- [ ] **Step 2: Modify convertFile to check saved config**

Replace the format-detection section of `convertFile`:

```go
func (e *Engine) convertFile(file, bucket, endpoint, accessKey, secretKey string) error {
	db := <-e.pool
	defer func() { e.pool <- db }()

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

	secretSQL := "CREATE OR REPLACE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + rawEndpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')"
	if _, err := db.Exec(secretSQL); err != nil {
		return fmt.Errorf("set s3 credentials: %w", err)
	}

	s3Path := "s3://" + bucket + "/" + file
	outputPath := "s3://" + bucket + "/" + file + ".parquet"

	// Check for saved column config first
	savedCfg := e.colStore.Match(bucket, file)

	var readSQL string
	if savedCfg != nil {
		cfg := savedCfg
		delim := cfg.Delimiter
		quote := cfg.Quote
		headerStr := "FALSE"
		if cfg.HeaderRow {
			headerStr = "TRUE"
		}
		if len(cfg.Columns) > 0 {
			// Build SELECT with CAST and column names
			var selects []string
			for i, col := range cfg.Columns {
				selects = append(selects, fmt.Sprintf("CAST(column%d AS %s) AS %s", i, col.Type, col.Name))
			}
			readSQL = fmt.Sprintf(`SELECT %s FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
				strings.Join(selects, ","), s3Path, delim, quote, headerStr)
		} else {
			readSQL = fmt.Sprintf(`SELECT * FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
				s3Path, delim, quote, headerStr)
		}
	} else {
		f := detectFormat(file)
		switch f {
		case "syslog", "log":
			readSQL = fmt.Sprintf(`SELECT * FROM read_csv('%s', DELIM=' ', QUOTE='"', HEADER=FALSE, all_varchar=true, ignore_errors=true, null_padding=true)`, s3Path)
		case "json":
			readSQL = fmt.Sprintf("SELECT * FROM read_json_auto('%s')", s3Path)
		default:
			readSQL = fmt.Sprintf("SELECT * FROM read_csv_auto('%s', HEADER=FALSE)", s3Path)
		}
	}

	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (FORMAT PARQUET)", readSQL, outputPath)

	if _, err := db.Exec(copySQL); err != nil {
		return fmt.Errorf("convert %s: %w", file, err)
	}

	return nil
}
```

- [ ] **Step 3: Update ConvertHandler to pass column config preview**

No change needed — the preview already works via the ColumnHandler's Preview method. The conversion engine's `colStore` is set at creation.

- [ ] **Step 4: Verify compile**

```bash
go build ./internal/convert/ && go build ./internal/api/
```

- [ ] **Step 5: Commit**

```bash
git add internal/convert/engine.go
git commit -m "feat: integrate column config into conversion engine with saved config fallback"
```

---

### Task 4: Wire column store and routes in main.go

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

- [ ] **Step 1: Add column store init**

After the report store block, add:

```go
	// Column config store
	columnDir := os.Getenv("DS3SQL_COLUMN_DIR")
	if columnDir == "" {
		home, _ := os.UserHomeDir()
		columnDir = home + "/.ds3sql/columns"
	}
	columnStore, err := column.NewStore(columnDir)
	if err != nil {
		log.Fatalf("failed to init column store: %v", err)
	}
	columnHandler := api.NewColumnHandler(columnStore)
```

Add import: `"github.com/esignoretti/ds3-sql-server/internal/column"`

- [ ] **Step 2: Update convertEngine init to pass columnStore**

```go
	convertEngine := convert.NewEngine(queryEngine.Pool(), workers, columnStore)
```

- [ ] **Step 3: Add column config routes in the no-timeout group**

```go
		r.Get("/convert/preview", columnHandler.Preview)
		r.Get("/convert/columns", columnHandler.ListConfigs)
		r.Post("/convert/columns", columnHandler.SaveConfig)
		r.Delete("/convert/columns/{bucket}/{pattern}", columnHandler.DeleteConfig)
```

- [ ] **Step 4: Verify compile**

```bash
go build ./cmd/ds3sql-server/
```

- [ ] **Step 5: Commit**

```bash
git add cmd/ds3sql-server/main.go
git commit -m "feat: wire column config store and routes in main.go"
```

---

### Task 5: Web UI – column config page

**Files:**
- Create: `internal/web/templates/column_config.html`
- Create: `internal/web/static/column_config.js`
- Modify: `internal/web/handler.go`
- Modify: `internal/web/templates/layout.html`
- Modify: `internal/web/static/style.css`

- [ ] **Step 1: Add handler method in web/handler.go**

```go
func (h *Handler) ColumnConfigPage(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	data := PageData{LoggedIn: true, Page: "column_config", Projects: session.Projects}
	h.render(w, "layout.html", data)
}
```

- [ ] **Step 2: Add route in main.go**

```go
		r.Get("/column-config", webHandler.ColumnConfigPage)
```

- [ ] **Step 3: Add template switch in layout.html**

```
{{else if eq .Page "column_config"}}{{template "column_config" .}}
```

- [ ] **Step 4: Create column_config.html**

```html
{{define "column_config"}}
<div class="single-page">
  <div class="top-bar">
    <span id="config-breadcrumb" style="font-size:0.95rem;font-weight:600;">Column Configuration</span>
  </div>

  <div id="config-app">
    <p style="color:var(--text-muted);">Loading...</p>
  </div>
</div>

<script src="/static/column_config.js"></script>
<script>
var urlParams = new URLSearchParams(window.location.search);
var bucket = urlParams.get('bucket');
var file = urlParams.get('file');
var projectId = urlParams.get('project');

if (!bucket || !file) {
  document.getElementById('config-app').innerHTML = '<p style="color:var(--text-muted);">No file selected. <a href="/browse">Browse buckets</a> and click a file to configure.</p>';
} else {
  selProject = projectId || '';
  loadPreview(bucket, file);
}
</script>
{{end}}
```

- [ ] **Step 5: Create column_config.js**

```javascript
var selProject = '';
var currentConfig = {
  bucket: '',
  pattern: '',
  delimiter: ' ',
  quote: '"',
  header_row: false,
  columns: []
};

function loadPreview(bucket, file) {
  currentConfig.bucket = bucket;
  // Derive pattern from filename
  var parts = file.split('/');
  var filename = parts.pop();
  var extIdx = filename.lastIndexOf('.');
  currentConfig.pattern = (parts.length ? parts.join('/') + '/' : '') + '*.' + (extIdx >= 0 ? filename.slice(extIdx + 1) : '*');

  fetch('/convert/preview?project=' + encodeURIComponent(selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(file) + '&lines=25')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.saved_config) {
        currentConfig.delimiter = d.saved_config.delimiter;
        currentConfig.quote = d.saved_config.quote;
        currentConfig.header_row = d.saved_config.header_row;
        currentConfig.columns = d.saved_config.columns;
      }
      renderConfig(d);
    })
    .catch(function(e) { document.getElementById('config-app').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function renderConfig(d) {
  var html = '<div class="config-layout">';

  // Step 1: Delimiter & Quote
  html += '<div class="card"><h3>Step 1: Delimiter & Quote</h3>';
  html += '<div style="display:flex;gap:1rem;flex-wrap:wrap;align-items:center;margin-top:0.75rem;">';
  html += '<label>Delimiter: <select id="delim-select" onchange="onConfigChange()">';
  var delims = [[' ', 'Space'], ['\t', 'Tab'], [',', 'Comma'], ['|', 'Pipe'], [';', 'Semicolon']];
  delims.forEach(function(d) {
    html += '<option value="' + d[0] + '"' + (currentConfig.delimiter === d[0] ? ' selected' : '') + '>' + d[1] + ' (' + (d[0] === ' ' ? '⎵' : d[0] === '\t' ? '⇥' : d[0]) + ')</option>';
  });
  html += '<option value="custom" ' + (delims.every(function(d) { return d[0] !== currentConfig.delimiter; }) ? 'selected' : '') + '>Custom</option>';
  html += '</select></label>';
  html += '<div id="custom-delim-group" style="display:' + (delims.every(function(d) { return d[0] !== currentConfig.delimiter; }) ? 'inline-flex' : 'none') + ';align-items:center;gap:0.25rem;"><label>Custom: <input type="text" id="custom-delim" value="' + (delims.every(function(d) { return d[0] !== currentConfig.delimiter; }) ? currentConfig.delimiter : '') + '" style="width:60px;" onchange="onConfigChange()"></label></div>';
  html += '<label>Quote: <select id="quote-select" onchange="onConfigChange()">';
  [['"', 'Double quote'], ["'", 'Single quote'], ['', 'None']].forEach(function(q) {
    html += '<option value="' + q[0] + '"' + (currentConfig.quote === q[0] ? ' selected' : '') + '>' + q[1] + '</option>';
  });
  html += '</select></label>';
  html += '<label><input type="checkbox" id="header-row" onchange="onConfigChange()" ' + (currentConfig.header_row ? 'checked' : '') + '> Header row</label>';
  html += '</div></div>';

  // Step 2: Preview
  html += '<div class="card"><h3>Step 2: Preview & Name Columns</h3>';
  html += '<div id="preview-table" style="overflow-x:auto;margin-top:0.75rem;"><p style="color:var(--text-muted);">Configure delimiter above to preview</p></div>';
  html += '</div>';

  // Step 3: Pattern & Save
  html += '<div class="card"><h3>Step 3: Save Config</h3>';
  html += '<div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap;margin-top:0.75rem;">';
  html += '<label>Pattern: <input type="text" id="config-pattern" value="' + escHtml(currentConfig.pattern) + '" style="width:300px;font-family:monospace;"></label>';
  html += '<button class="btn" onclick="saveConfig()">💾 Save Config</button>';
  html += '<button class="btn btn-secondary" onclick="saveAndConvert()">💾 Save & Convert</button>';
  html += '</div><p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.5rem;">This config applies to all files matching the pattern (e.g., <code>*.log</code> or <code>apache/*.log</code>)</p>';
  html += '</div>';

  html += '</div>';

  document.getElementById('config-app').innerHTML = html;
  updatePreview(d);
}

function onConfigChange() {
  var delim = document.getElementById('delim-select').value;
  if (delim === 'custom') {
    document.getElementById('custom-delim-group').style.display = 'inline-flex';
    currentConfig.delimiter = document.getElementById('custom-delim').value;
  } else {
    document.getElementById('custom-delim-group').style.display = 'none';
    currentConfig.delimiter = delim;
  }
  currentConfig.quote = document.getElementById('quote-select').value;
  currentConfig.header_row = document.getElementById('header-row').checked conn

  // Re-fetch preview with new settings
  var bucket = currentConfig.bucket;
  var file = document.querySelector('[data-preview-file]');
  if (!file) return;
  fetch('/convert/preview?project=' + encodeURIComponent(selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(file.getAttribute('data-preview-file')) + '&lines=25&delimiter=' + encodeURIComponent(currentConfig.delimiter) + '&quote=' + encodeURIComponent(currentConfig.quote))
    .then(function(r) { return r.json(); })
    .then(function(d) { updatePreview(d); })
    .catch(function(e) {});
}

function updatePreview(d) {
  var container = document.getElementById('preview-table');
  if (!container || !d || !d.preview_lines || !d.preview_lines.length) {
    if (container) container.innerHTML = '<p style="color:var(--text-muted);">No preview available</p>';
    return;
  }
  // Build a simple table showing the raw lines with delimiter highlighted
  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';
  var delim = currentConfig.delimiter;
  d.preview_lines.forEach(function(line, idx) {
    var cells = line.split(delim);
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    if (idx === 0 && currentConfig.header_row) {
      // Header row: show as column name inputs
    }
    cells.forEach(function(cell, ci) {
      var colName = currentConfig.columns[ci] ? currentConfig.columns[ci].name : ('col' + ci);
      html += '<span style="flex:1;min-width:80px;padding:0 0.25rem;border-right:1px solid var(--primary);">';
      html += '<span style="color:var(--text-muted);font-size:0.65rem;display:block;">' + escHtml(colName) + '</span>';
      html += escHtml(cell);
      html += '</span>';
    });
    html += '</div>';
  });
  html += '</div>';
  html += '<div style="margin-top:0.75rem;"><h4 style="font-size:0.85rem;margin-bottom:0.5rem;">Column Names</h4>';
  // Determine number of columns
  var numCols = d.preview_lines[0].split(currentConfig.delimiter).length;
  if (currentConfig.columns.length !== numCols) {
    currentConfig.columns = [];
    for (var i = 0; i < numCols; i++) {
      currentConfig.columns.push({name: 'col' + i, type: 'VARCHAR'});
    }
  }
  html += '<div style="display:flex;gap:0.5rem;flex-wrap:wrap;">';
  currentConfig.columns.forEach(function(col, idx) {
    html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
    html += '<input type="text" value="' + escHtml(col.name) + '" style="width:100px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="currentConfig.columns[' + idx + '].name=this.value">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="currentConfig.columns[' + idx + '].type=this.value">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    html += '</div>';
  });
  html += '</div></div>';
  container.innerHTML = html;
}

function saveConfig() {
  currentConfig.pattern = document.getElementById('config-pattern').value;
  fetch('/convert/columns', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(currentConfig)
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    alert('Config saved for pattern: ' + currentConfig.pattern);
  })
  .catch(function(e) { alert('Error saving: ' + e.message); });
}

function saveAndConvert() {
  saveConfig();
  // Navigate back to browse after saving
  setTimeout(function() { window.location.href = '/browse'; }, 500);
}

function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
```

Note: the `onConfigChange` function has a typo (`conn` instead of ``) in the line setting `currentConfig.header_row`. Fix it: remove `conn`.

- [ ] **Step 6: Add CSS**

Add to `style.css`:

```css
.config-layout { display:flex; flex-direction:column; gap:1rem; }
.config-layout .card { background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:1rem; }
.config-layout .card h3 { font-size:1rem; margin-bottom:0.25rem; }
```

- [ ] **Step 7: Add "Configure" button to browse.html convertible files**

In the convertible section, update each item to include a configure link:

```javascript
html += ' <a href="/column-config?project=' + encodeURIComponent(selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(o.key) + '" style="color:var(--primary);font-size:0.75rem;text-decoration:none;">[configure]</a>';
```

Insert this after the file size span in the convertible label, but before the closing `</label>`.

Also add a data attribute for the file name needed by column_config.js. Actually, the JS uses `document.querySelector('[data-preview-file]')` — let me simplify that. Instead of a data attribute, column_config.js gets `bucket` and `file` from URL params, which it already does. The `onConfigChange` function can just use those URL params.

Simplify: remove the data-preview-file query and just use the URL params in onConfigChange.

- [ ] **Step 8: Verify compile**

```bash
go build ./internal/web/ && go build ./cmd/ds3sql-server/
```

- [ ] **Step 9: Commit**

```bash
git add internal/web/ cmd/ds3sql-server/main.go
git commit -m "feat: column config UI with preview, delimiter picker, and column names"
```

---

### Task 6: Final build

- [ ] **Step 1: Run full test suite**

```bash
go test -v -count=1 ./internal/column/ ./internal/convert/ ./internal/s3/ ./internal/query/
```

Expected: all pass

- [ ] **Step 2: Build binaries**

```bash
make build
```

- [ ] **Step 3: Push**

```bash
git push origin main
```
