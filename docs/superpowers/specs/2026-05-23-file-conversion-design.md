# File Conversion to Parquet — Design Document

**Date**: 2026-05-23
**Status**: Draft

## 1. Overview

Add a file conversion feature to the DS3 SQL Server browse page that allows users to convert unsupported log/text files (`.log`, `.txt`, `.syslog`, `.out`, `.err`, Apache access logs) to Parquet format directly on S3. Conversion runs server-side using DuckDB, streams data through S3 (no local temp files), and supports parallel processing with configurable concurrency. Users can optionally delete the original file after conversion.

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Browse Page (bucket listing)                                 │
│  ├─ Supported files → normal styling for query selection      │
│  ├─ Unsupported files → red, checkbox, [Convert to Parquet]   │
│  └─ [Convert Selected] button → POST /convert                 │
└────────────────────┬─────────────────────────────────────────┘
                     │ POST /convert
                     ▼
┌──────────────────────────────────────────────────────────────┐
│  Conversion Engine (internal/convert/)                        │
│  ├─ Borrows DuckDB connection from pool                       │
│  ├─ Worker pool (N goroutines, default 4)                    │
│  │  └─ For each file:                                         │
│  │     ├─ Detect format from extension                        │
│  │     ├─ Build format-specific DuckDB read query             │
│  │     ├─ COPY result to s3://bucket/file.parquet             │
│  │     └─ Optionally DELETE original via s3.Client            │
│  └─ Job store (in-memory, TTL-cleaned)                        │
└────────────────────┬─────────────────────────────────────────┘
                     │ GET /convert/status/{id}
                     ▼
┌──────────────────────────────────────────────────────────────┐
│  Web UI polls status → progress per file + overall            │
└──────────────────────────────────────────────────────────────┘
```

## 3. Conversion Engine

### Location

New package: `internal/convert/`

### Types

```go
type Engine struct {
    pool    chan *sql.DB
    workers int
    jobs    *JobStore
}

type JobStore struct {
    mu     sync.RWMutex
    jobs   map[string]*Job
}

type Job struct {
    ID        string           `json:"id"`
    Bucket    string           `json:"bucket"`
    Total     int              `json:"total"`
    Completed int              `json:"completed"`
    Status    string           `json:"status"` // "running", "done", "error"
    Results   []FileResult     `json:"results"`
    CreatedAt time.Time        `json:"created_at"`
}

type FileResult struct {
    File      string `json:"file"`
    Status    string `json:"status"`    // "pending", "running", "done", "error"
    Converted string `json:"converted,omitempty"`
    Error     string `json:"error,omitempty"`
    ElapsedMs int64  `json:"elapsed_ms"`
}

type ConvertRequest struct {
    Bucket         string   `json:"bucket"`
    Files          []string `json:"files"`
    DeleteOriginal bool     `json:"delete_original"`
}
```

### Format Detection

| Extension | DuckDB Reader | Notes |
|-----------|---------------|-------|
| `.log` | `read_csv_auto` with `HEADER=FALSE`, `AUTO_DETECT=TRUE` | Generic log files |
| `.txt`, `.out`, `.err` | `read_csv_auto` with `HEADER=FALSE` | Plain text output files |
| `.syslog` | `read_csv` with `DELIM=' '`, custom column names | Syslog format: month, day, time, host, app, pid, message |
| Apache access (`.log` with combined detection) | `read_csv` with `DELIM=' '` + regexp_extract for quoted fields | Combined format: IP, ident, user, time, request, status, size, referer, user_agent |
| `.json`, `.jsonl` | `read_json_auto` | Handles nested JSON structures (converted to flat columns) |

### Apache Combined Format

For files detected as Apache combined format (heuristic: first line matches `^(\S+) (\S+) (\S+) \[([^\]]+)\] "([^"]*)" (\d+) (\d+) "([^"]*)" "([^"]*)"`):

```sql
SELECT
    columns[1] AS ip,
    columns[2] AS ident,
    columns[3] AS user,
    columns[4] AS time_str,
    columns[5] AS request,
    CAST(columns[6] AS INTEGER) AS status,
    CAST(columns[7] AS BIGINT) AS size,
    columns[8] AS referer,
    columns[9] AS user_agent
FROM read_csv('s3://bucket/file.log', AUTO_DETECT=FALSE, DELIM=' ', HEADER=FALSE, QUOTE='"')
```

### Syslog Format

```sql
SELECT
    columns[1] AS month,
    columns[2] AS day,
    columns[3] AS time,
    columns[4] AS host,
    columns[5] AS app,
    columns[6] AS pid,
    columns[7] AS message
FROM read_csv('s3://bucket/file.syslog', AUTO_DETECT=FALSE, DELIM=' ', HEADER=FALSE)
```

### Conversion Flow (per file)

1. Borrow DuckDB connection from pool
2. Determine format from extension (and optional content sniff on first line)
3. Build read query with format-specific configuration
4. Execute: `COPY (SELECT * FROM <read_query>) TO 's3://bucket/file.parquet' (FORMAT PARQUET)`
5. If successful and `delete_original` is true: call `s3.Client.DeleteObject()`
6. Return connection to pool

### Parallelism

- Worker pool of `N` goroutines (configurable, default `convert.workers: 4`)
- Files sent via buffered channel, results collected via channel
- Each worker borrows its own DuckDB connection (pool must have >= `workers` connections; if pool is smaller, workers serialize on borrow)

## 4. S3 Client Additions

### DeleteObject

```go
func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error
```

Uses `awss3.DeleteObjectInput` with the existing configured client. No `GetObject` needed — DuckDB reads directly via httpfs.

## 5. API Handlers

### POST /convert

New handler in `internal/api/`. Accepts conversion request, creates a job, launches workers in background goroutine, returns job ID immediately.

**Request:**
```json
{
  "bucket": "my-bucket",
  "files": ["logs/app.log", "logs/auth.syslog"],
  "delete_original": false
}
```

**Response (202):**
```json
{
  "job_id": "uuid",
  "total": 2,
  "status": "running"
}
```

### GET /convert/status/{id}

Returns current job progress.

**Response (200):**
```json
{
  "job_id": "uuid",
  "total": 2,
  "completed": 1,
  "status": "running",
  "results": [
    {"file": "logs/app.log", "status": "done", "converted": "logs/app.log.parquet", "elapsed_ms": 3420},
    {"file": "logs/auth.syslog", "status": "running", "elapsed_ms": 1200}
  ]
}
```

When all files complete: `"status": "done"`. On any error: `"status": "error"` with per-file error details.

## 6. Web UI Changes

### Browse Page File Listing

Replace the current "Other" section with two sub-sections:

- **Supported files** (queryable) — same as current: `.parquet`, `.csv`, `.json`, `.jsonl`, `.tsv` — blue/white styling, click to select for query
- **Convertible files** — `.log`, `.txt`, `.syslog`, `.out`, `.err` — **red** text, checkbox for selection (separate from the query-path selection)

Each convertible file gets:
- Red-colored filename with a warning icon
- A checkbox (multi-select)
- Clicking selects/deselects for conversion

### Conversion Controls

When convertible files are checked:
- "Convert to Parquet" button appears with a count badge
- "Delete original" toggle (checkbox, default off, styled below the button)
- Clicking "Convert" hides the button, shows a progress panel

### Progress Panel

Inline progress panel (below the file list):

```
┌─ Converting ──────────────────────────────────┐
│ [████████░░░░░░░░░░░░] 2/5 files complete      │
│                                                │
│ ✓ logs/app.log → logs/app.log.parquet (3.4s)   │
│ ✗ logs/auth.syslog → ERROR: parse error        │
│ ◌ logs/error.log → running... (2.1s)           │
│ ◌ logs/debug.log → pending                     │
│ ⬜ logs/test.log → pending                     │
│                                                │
│ [Done]                                         │
└────────────────────────────────────────────────┘
```

After completion, the file list refreshes automatically (converted files now show as Parquet in the supported section).

## 7. Router Changes

Add to `main.go` (authenticated, no-timeout group):

| Method | Path | Handler |
|--------|------|---------|
| POST | `/convert` | `convertHandler.Start` |
| GET | `/convert/status/{id}` | `convertHandler.Status` |

## 8. Job Store

In-memory, TTL-based cleanup (30 minutes after completion):

```go
type JobStore struct {
    mu   sync.RWMutex
    jobs map[string]*Job
}

func NewJobStore() *JobStore
func (s *JobStore) Set(id string, job *Job)
func (s *JobStore) Get(id string) (*Job, bool)
func (s *JobStore) Cleanup() // remove jobs older than 30min
```

A background goroutine in main.go runs `Cleanup` every 5 minutes.

## 9. Files to Create/Modify

| File | Action |
|---|---|
| `internal/convert/engine.go` | Create — conversion engine with format detection |
| `internal/convert/engine_test.go` | Create — tests for format detection and basic flow |
| `internal/convert/job.go` | Create — Job, JobStore types |
| `internal/s3/client.go` | Modify — add DeleteObject method |
| `internal/api/convert_handler.go` | Create — POST /convert, GET /convert/status/{id} |
| `cmd/ds3sql-server/main.go` | Modify — register new routes |
| `internal/web/templates/browse.html` | Modify — convertible files UI, progress panel |
| `internal/web/static/style.css` | Modify — conversion styles |

## 10. Testing

- Unit tests for format detection (Apache, syslog, JSON, generic)
- Integration test for full conversion flow (requires DuckDB + S3-like endpoint)
- Test parallel conversion with multiple files
- Test error handling (bad file, parse error, delete failure)
- Test job store cleanup

## 11. Non-Goals

- Local file upload and conversion (S3-only)
- Conversion to formats other than Parquet
- Custom column mapping UI (format detection is automatic)
- Batch conversion across multiple buckets
- Scheduled/periodic conversion
