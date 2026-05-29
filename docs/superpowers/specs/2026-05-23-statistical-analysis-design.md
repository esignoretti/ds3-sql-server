# Statistical Analysis & Visualization — Design Document

**Date**: 2026-05-23
**Status**: Draft

## 1. Overview

Add a statistical analysis and visualization layer to DS3 SQL Server, allowing users to analyze query results, auto-generate summary findings, build interactive charts, and save reports for later reuse.

## 2. Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Browse Page (query → results table)                     │
│    └─ [Analyze] button → sends lastResult to /analyze  │
└────────────────────┬───────────────────────────────────┘
                     │ POST /analyze (result data as JSON)
                     ▼
┌─────────────────────────────────────────────────────────┐
│  Analysis Engine (internal/analysis/)                    │
│  ├─ Receives query result columns + rows                 │
│  ├─ Creates DuckDB temp table, inserts data              │
│  ├─ Computes per-column stats + correlations             │
│  ├─ Generates human-readable summary text                │
│  └─ Returns AnalysisResult JSON                          │
└────────────────────┬───────────────────────────────────┘
                     │ analysis JSON
                     ▼
┌─────────────────────────────────────────────────────────┐
│  Report Page (new template: report.html)                 │
│  ├─ Column selector + stats summary                      │
│  ├─ Chart builder with Chart.js                          │
│  ├─ Save/Load reports via report store                   │
│  └─ Export PDF via browser print                         │
└────────────────────┬───────────────────────────────────┘
                     │ POST/GET /api/reports
                     ▼
┌─────────────────────────────────────────────────────────┐
│  Report Store (internal/report/)                         │
│  ├─ JSON files at ~/.ds3sql/reports/<id>.json            │
│  └─ REST API: list, get, save, delete                    │
└─────────────────────────────────────────────────────────┘
```

## 3. Analysis Engine

### Location

New package: `internal/analysis/`

### Interface

```go
type Engine struct {
    pool chan *sql.DB  // borrows from the same DuckDB pool as query.Engine
}

func NewEngine(pool chan *sql.DB) *Engine

type AnalysisRequest struct {
    Columns []query.ColumnInfo `json:"columns"`
    Rows    [][]any            `json:"rows"`
}

type AnalysisResult struct {
    Columns     map[string]ColumnAnalysis `json:"columns"`
    Correlations []Correlation             `json:"correlations"`
    Summary     []string                   `json:"summary"`
    ElapsedMs   int64                      `json:"elapsed_ms"`
    Error       string                     `json:"error,omitempty"`
}

type ColumnAnalysis struct {
    Type       string        `json:"type"`       // "numeric", "categorical", "temporal", "boolean"
    Stats      any           `json:"stats"`       // type-specific stats struct
    Histogram  []Bin         `json:"histogram,omitempty"`
    TopValues  []ValueCount  `json:"top_values,omitempty"`
}

type Correlation struct {
    ColA    string  `json:"col_a"`
    ColB    string  `json:"col_b"`
    Type    string  `json:"type"`    // "pearson", "cramers_v", "frequency"
    Value   float64 `json:"value"`
}

type Bin struct {
    BinStart float64 `json:"bin_start"`
    BinEnd   float64 `json:"bin_end"`
    Count    int     `json:"count"`
}
```

### Flow

1. `Analyze(req AnalysisRequest) *AnalysisResult`
2. Borrow DuckDB connection from pool
3. Create temp table matching column schema using rows data
4. Run per-column SQL queries (type-dependent):
   - Numeric: `COUNT, MIN, MAX, AVG, STDDEV_SAMP, MEDIAN, PERCENTILE_CONT(0.25), PERCENTILE_CONT(0.75), COUNT(*) FILTER (WHERE col IS NULL), COUNT(DISTINCT col)`
   - Categorical: `COUNT, COUNT(DISTINCT), COUNT(*) FILTER (WHERE col IS NULL)`, plus `SELECT col, COUNT(*) as cnt FROM t GROUP BY col ORDER BY cnt DESC LIMIT 10`
   - Temporal: `COUNT, MIN, MAX, COUNT(*) FILTER (WHERE col IS NULL)`, plus distribution bucketing
   - Boolean: `COUNT, COUNT(*) FILTER (WHERE col IS NULL), SUM(CASE WHEN col THEN 1 ELSE 0 END) as true_count`
5. Run column-pair correlations (DuckDB `CORR()` for numeric pairs)
6. Generate summary text for each column (template-based, ~1-2 sentences per column)
7. Return DuckDB connection to pool

### Summary Text Generation

Template-based sentences:

- Numeric: `"{name} ranges from {min} to {max} (mean: {mean}, stddev: {stddev}). {null_pct}% null values."`
- Categorical: `"{name} has {distinct} distinct values; '{top1}' is most common ({top1_pct}%)."`
- Temporal: `"{name} spans from {min} to {max}. Data is distributed across {span_description}."`
- Boolean: `"{name} is true for {true_pct}% of rows."`

## 4. Report Store

### Location

New package: `internal/report/`

### Storage

JSON files at `~/.ds3sql/reports/<uuid>.json`. Each file stores the full report including query metadata, all analysis results, chart configurations, and a copy of the query result rows so reports are fully self-contained and viewable without re-querying S3.

```go
type ChartConfig struct {
    ID        string `json:"id"`
    Type      string `json:"type"`    // "bar", "line", "pie", "scatter", "histogram"
    XColumn   string `json:"x_column"`
    YColumn   string `json:"y_column"`
    GroupBy   string `json:"group_by,omitempty"`   // optional: color/group by this column
    Bucket    string `json:"bucket,omitempty"`      // temporal bucket: "auto", "hour", "day", "week", "month"
    Title     string `json:"title,omitempty"`
    SortOrder string `json:"sort_order,omitempty"`  // "desc", "asc", null for default
    MaxGroups int    `json:"max_groups,omitempty"`  // limit groups shown (default 10)
}
```

### API Handlers

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/reports` | List saved reports (metadata only, no results) |
| POST | `/api/reports` | Save new report (body: Report) |
| GET | `/api/reports/{id}` | Load full report + analysis |
| DELETE | `/api/reports/{id}` | Delete report |

All under authenticated routes.

### Store interface (for testability)

```go
type Store interface {
    List() ([]ReportSummary, error)
    Save(report *Report) error
    Get(id string) (*Report, error)
    Delete(id string) error
}

type DiskStore struct {
    baseDir string
}
```

## 5. API Handlers

### POST /analyze

New handler in `internal/api/`. Accepts the query result rows and column metadata (the same shape as `query.Result`), runs analysis, returns `AnalysisResult`.

**Request body:**
```json
{
  "columns": [{"name": "age", "type": "INTEGER"}, {"name": "country", "type": "VARCHAR"}],
  "rows": [[44, "China"], [32, "Italy"], ...]
}
```

The analysis engine creates a DuckDB temp table from the rows, computes statistics, and returns the analysis. The raw rows are not returned — the frontend already has them from the query result.

### GET/POST/DELETE /api/reports

Standard CRUD handlers.

## 6. Report Page (Web UI)

### New page: report.html

A new template added to the layout's template switch. It receives report data (analysis results, column info, saved chart configs).

**Layout** (3-zone):

- **Left sidebar (300px):**
  - Column list with checkboxes (each column name + type badge)
  - Clicking a column toggles visibility in stats/charts
  - Chart type selector (icon grid: Bar, Pie, Line, Scatter, Histogram)
  - [Add Chart] button

- **Main content:**
  - **Stats Summary section** — auto-generated text findings, grouped per column, with a small inline sparkline for numeric columns
  - **Chart section** — each chart is a card with Chart.js canvas + column picker dropdowns (X, Y, Group/Color by, temporal bucket, sort order) + delete button

- **Top bar:**
  - Report title (editable text input)
  - [Save] — saves to report store
  - [Export PDF] — calls `window.print()` with `@media print` CSS

### Chart.js Integration

Load Chart.js from CDN. Supported chart types with their column and aggregation rules:

| Chart Type | X Column | Y Column | GroupBy behavior | Bucket behavior |
|---|---|---|---|---|
| **Bar** | categorical | numeric (aggregated: count, sum, avg) | Multi-color grouped bars per group | N/A |
| **Pie** | categorical (one column only) | N/A (frequency) | N/A | N/A |
| **Line** | temporal or numeric | numeric (aggregated) | Multi-color lines per group | Temporal bucket: auto, hour, day, week, month |
| **Scatter** | numeric | numeric (raw, no aggregation) | Multi-color dots per group | N/A |
| **Grouped Bar** | temporal or categorical | numeric (aggregated) | Divides bars into segments/stacked sections | Temporal bucket for temporal X |

**Bucket (temporal aggregation):** When X is a temporal column, the chart pre-aggregates data into time buckets before rendering. Options: "auto" (DuckDB suggests based on span), "hour", "day", "week", "month". This prevents line charts with thousands of points and makes bar charts over time meaningful.

**GroupBy behavior:**
- Data is grouped by the `group_by` column value (capped at `max_groups`, default 10; remaining grouped as "Other")
- Bar charts render grouped bars side by side or stacked
- Line charts render one line per group
- Scatter charts render one color per group
- When `group_by` is set, Y column aggregation (count, sum, avg) is computed per group per X bucket

**Coloring:** Chart.js built-in palette, cycling through 10 distinguishable colors. Group values consistently colored across charts in the same report (same group value = same color).

### Report List Page

Optional addition: a "Reports" nav link in the sidebar leading to a list of saved reports (GET /api/reports → click to load).

## 7. Router Changes (`main.go`)

Add:
- `POST /analyze` — authenticated, takes result data, returns analysis
- `GET /api/reports` — authenticated
- `POST /api/reports` — authenticated
- `GET /api/reports/{id}` — authenticated
- `DELETE /api/reports/{id}` — authenticated
- `GET /report` — authenticated, report page
- `GET /reports` — authenticated, saved reports list page

## 8. Sidebar Navigation

Add "Reports" link to `layout.html` sidebar after "Console".

## 9. Files to Create/Modify

| File | Action |
|---|---|
| `internal/analysis/engine.go` | Create — analysis engine |
| `internal/analysis/engine_test.go` | Create — analysis tests |
| `internal/report/store.go` | Create — report store interface + disk impl |
| `internal/report/store_test.go` | Create — store tests |
| `internal/report/model.go` | Create — Report, ChartConfig types |
| `internal/api/analysis_handler.go` | Create — /analyze handler |
| `internal/api/report_handler.go` | Create — /api/reports CRUD handlers |
| `internal/web/handler.go` | Modify — add ReportPage, ReportsListPage, new template files |
| `internal/web/templates/report.html` | Create — report editor page |
| `internal/web/templates/reports_list.html` | Create — saved reports list |
| `internal/web/templates/layout.html` | Modify — add Reports nav link |
| `internal/web/static/style.css` | Modify — add report page styles |
| `internal/web/static/report.js` | Create — chart builder & report logic |
| `cmd/ds3sql-server/main.go` | Modify — register new routes, wire analysis engine |

## 10. Testing

- Analysis engine: unit tests with known datasets (numeric, categorical, temporal, boolean columns), verify stats match expected values
- Report store: disk store tests with temp dir, verify CRUD
- API handlers: table-driven tests for /analyze with valid/invalid inputs
- Web UI: manual testing of chart rendering, column selection, save/load cycle

## 11. Non-Goals

- PDF generation on server (uses browser print-to-PDF)
- Real-time collaboration on reports
- Report scheduling or email delivery
- Custom SQL for chart data (charts use the analysis result only)
- Image export of individual charts (deferred)
