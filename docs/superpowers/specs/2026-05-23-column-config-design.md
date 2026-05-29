# Manual Column Configuration for Log Conversion — Design Document

**Date**: 2026-05-23
**Status**: Draft

## 1. Overview

Add a column configuration UI to the conversion feature allowing users to preview the first 25 lines of a file, choose a delimiter and quote character, name columns, and save the configuration per bucket+file-pattern for automatic reuse.

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Browse Page — convertible file listing                      │
│  └─ Click file → [Configure Columns] button                  │
└────────────────────┬─────────────────────────────────────────┘
                     │ GET /convert/preview?bucket=x&file=y&lines=25
                     ▼
┌──────────────────────────────────────────────────────────────┐
│  Preview returns first 25 lines as raw text + column info    │
│  Browser renders split preview with editable column headers   │
│  User configures: delimiter, quote, column names, column types│
│  On save: POST /convert/columns → saves to disk               │
└────────────────────┬─────────────────────────────────────────┘
                     │ Stored at ~/.ds3sql/columns/<bucket>/<pattern>.json
                     ▼
┌──────────────────────────────────────────────────────────────┐
│  Conversion engine checks for saved column config first       │
│  If found: use config (DELIM, QUOTE, column names/types)      │
│  If not found: fall back to auto-detect                      │
└──────────────────────────────────────────────────────────────┘
```

## 3. Column Config Store

### Location

New package: `internal/column/`

### Storage

JSON files at `~/.ds3sql/columns/<bucket>/<pattern>.json`. Example: `~/.ds3sql/columns/test-logs/*.log.json`

```go
type ColumnConfig struct {
	Bucket      string       `json:"bucket"`
	Pattern     string       `json:"pattern"`     // glob pattern like "*.log"
	Delimiter   string       `json:"delimiter"`   // " ", "\t", ",", "|", ";"
	Quote       string       `json:"quote"`       // "\"", "'", ""
	HeaderRow   bool         `json:"header_row"`
	Columns     []ColumnDef  `json:"columns"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type ColumnDef struct {
	Name string `json:"name"`
	Type string `json:"type"` // "VARCHAR", "INTEGER", "BIGINT", "DOUBLE", "BOOLEAN", "TIMESTAMP"
}

type ColumnStore struct {
	baseDir string
}

func NewColumnStore(baseDir string) (*ColumnStore, error)
func (s *ColumnStore) Get(bucket, pattern string) (*ColumnConfig, error)
func (s *ColumnStore) Save(config *ColumnConfig) error
func (s *ColumnStore) List(bucket string) ([]ColumnConfig, error)
func (s *ColumnStore) Delete(bucket, pattern string) error
func (s *ColumnStore) Match(bucket, filename string) *ColumnConfig // finds best matching pattern
```

### Matching

When converting a file, `Match()` checks for a stored config by iterating patterns for the given bucket, longest-pattern-first (most specific wins). Example: if `./*.log` and `./apache/*.log` both exist, `apache/access.log` matches `./apache/*.log` first.

## 4. API Handlers

### GET /convert/preview

Returns raw text lines + parsed column info for the first 25 lines of a file.

| Parameter | Description |
|-----------|-------------|
| `bucket` | S3 bucket |
| `file` | File key |
| `lines` | Number of lines to preview (default 25) |
| `delimiter` | Optional delimiter to test-parse (for live preview) |
| `quote` | Optional quote char |

**Response:**
```json
{
  "filename": "Apache_2k.log",
  "total_lines": 2000,
  "preview_lines": [
    "192.168.1.1 - frank [10/Oct/2000:13:55:36 -0700] \"GET / HTTP/1.0\" 200 2326 \"http://...\"",
    "127.0.0.1 - - [10/Oct/2000:13:55:36 -0700] \"GET /index.html HTTP/1.1\" 200 1234 \"-\" \"curl/7.68\""
  ],
  "parsed": {
    "columns": [
      {"name": "column0", "values": ["192.168.1.1", "127.0.0.1"]},
      {"name": "column1", "values": ["-", "-"]},
      {"name": "column2", "values": ["frank", "-"]}
    ],
    "delimiter": " ",
    "quote": "\""
  },
  "saved_config": null
}
```

### POST /convert/columns

Save a column configuration for a bucket+pattern.

**Request:**
```json
{
  "bucket": "test-logs",
  "pattern": "*.log",
  "delimiter": " ",
  "quote": "\"",
  "header_row": false,
  "columns": [
    {"name": "ip", "type": "VARCHAR"},
    {"name": "request", "type": "VARCHAR"},
    {"name": "status", "type": "INTEGER"}
  ]
}
```

### GET /convert/columns?bucket=test-logs

List saved column configs for a bucket.

## 5. Conversion Engine Integration

The `convertFile` method in the conversion engine now:
1. Check `ColumnStore.Match(bucket, file)` for a saved config
2. If found: use the config's `DELIM`, `QUOTE`, and column definitions
3. If not found: fall back to current auto-detect logic

When a config with named columns is found, the read query becomes:
```sql
SELECT CAST(column0 AS VARCHAR) AS ip,
       CAST(column1 AS VARCHAR) AS request,
       CAST(column2 AS INTEGER) AS status
FROM read_csv('s3://...', DELIM=' ', QUOTE='"', HEADER=FALSE, all_varchar=true, ignore_errors=true, null_padding=true)
```

## 6. Web UI Changes

### Browse Page

Each convertible file gets a new option: clicking the filename opens a column config panel (before conversion). The existing "Convert to Parquet" button still works for auto-detect. A new "Configure" option is added per file (or for the whole pattern).

### Column Config Page

New template or modal with:
- **Delimiter picker**: Space, Tab, Comma, Pipe, Semicolon, Custom input
- **Quote char**: `"`, `'`, None
- **Header row**: toggle (checkbox)
- **Preview table**: first 25 lines shown with delimiter applied, column headers are editable text inputs
- **Column type**: dropdown per column (VARCHAR, INTEGER, BIGINT, DOUBLE, BOOLEAN, TIMESTAMP)
- **Save**: saves config, reloads the file list, marks file as "configured"

### Saved Config Indication

Files with a saved column config show a small badge or icon (e.g., a gear icon) next to the filename, and conversion uses the saved config automatically.

## 7. Column Store

Same structure as `report.DiskStore` — JSON files on disk at `~/.ds3sql/columns/`.

```go
func (s *ColumnStore) Match(bucket, filename string) *ColumnConfig {
	// 1. List all configs for bucket
	// 2. Sort by pattern length descending (most specific first)
	// 3. Return first glob match
}
```

## 8. Files to Create/Modify

| File | Action |
|---|---|
| `internal/column/config.go` | Create — ColumnConfig, ColumnDef, ColumnStore types |
| `internal/column/config_test.go` | Create — tests for Match, CRUD |
| `internal/api/column_handler.go` | Create — /convert/preview, /convert/columns endpoints |
| `internal/api/convert_handler.go` | Modify — integrate ColumnStore into conversion flow |
| `internal/convert/engine.go` | Modify — check ColumnStore before auto-detect, use saved config |
| `cmd/ds3sql-server/main.go` | Modify — wire ColumnStore |
| `internal/web/templates/browse.html` | Modify — add Configure button, config badge |
| `internal/web/templates/column_config.html` | Create — column config page |
| `internal/web/static/column_config.js` | Create — JS for live preview + column editor |
| `internal/web/static/style.css` | Modify — column config styles |

## 9. Testing

- ColumnStore CRUD unit tests
- Match() with glob patterns (most-specific-first)
- Preview endpoint with known test files
- Conversion engine with saved config vs auto-detect fallback
