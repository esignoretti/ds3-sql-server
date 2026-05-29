# Statistical Analysis & Visualization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add statistical analysis, interactive charting, and persistent reports to the query result page.

**Architecture:** A new `internal/analysis/` package creates a DuckDB temp table from query result rows and runs per-column statistics. A new `internal/report/` package stores reports as JSON files on disk. The web UI gets a new report page with Chart.js for interactive charts, column selection, group-by, temporal bucketing, save/load, and PDF export via browser print.

**Tech Stack:** Go 1.26, DuckDB (existing pool), Chart.js (CDN), HTML/CSS/JS (no build step)

---

### Task 1: Analysis engine – types and empty scaffold

**Files:**
- Create: `internal/analysis/engine.go`
- Create: `internal/analysis/engine_test.go`

- [ ] **Step 1: Write the test file with a placeholder test**

```go
package analysis

import (
	"testing"
)

func TestAnalyzeNumericColumn(t *testing.T) {
	t.Skip("not yet implemented")
}
```

- [ ] **Step 2: Run test to verify it compiles and skips**

```bash
go test ./internal/analysis/
```

Expected: PASS (1 skipped)

- [ ] **Step 3: Create engine.go with types and struct**

```go
package analysis

import (
	"database/sql"
	"time"
)

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type AnalysisRequest struct {
	Columns []ColumnInfo `json:"columns"`
	Rows    [][]any      `json:"rows"`
}

type ColumnAnalysis struct {
	Type      string        `json:"type"`
	Stats     any           `json:"stats"`
	Histogram []Bin         `json:"histogram,omitempty"`
	TopValues []ValueCount  `json:"top_values,omitempty"`
}

type Bin struct {
	BinStart float64 `json:"bin_start"`
	BinEnd   float64 `json:"bin_end"`
	Count    int     `json:"count"`
}

type ValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
	Pct   float64 `json:"pct"`
}

type Correlation struct {
	ColA  string  `json:"col_a"`
	ColB  string  `json:"col_b"`
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

type AnalysisResult struct {
	Columns     map[string]ColumnAnalysis `json:"columns"`
	Correlations []Correlation             `json:"correlations"`
	Summary     []string                   `json:"summary"`
	ElapsedMs   int64                      `json:"elapsed_ms"`
	Error       string                     `json:"error,omitempty"`
}

type Engine struct {
	pool chan *sql.DB
}

func NewEngine(pool chan *sql.DB) *Engine {
	return &Engine{pool: pool}
}

func (e *Engine) Analyze(req AnalysisRequest) *AnalysisResult {
	start := time.Now()
	_ = start
	return &AnalysisResult{
		Columns: make(map[string]ColumnAnalysis),
		ElapsedMs: 0,
		Error: "not yet implemented",
	}
}
```

- [ ] **Step 4: Verify it compiles**

```bash
go build ./internal/analysis/
```

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/
git commit -m "chore: add analysis engine scaffold with types"
```

---

### Task 2: Analysis engine – DuckDB temp table + numeric stats

**Files:**
- Modify: `internal/analysis/engine.go`
- Modify: `internal/analysis/engine_test.go`

- [ ] **Step 1: Write the test**

Replace the test file with:

```go
package analysis

import (
	"testing"
)

func TestAnalyzeNumericColumn(t *testing.T) {
	pool := make(chan *sql.DB, 1)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	pool <- db

	engine := NewEngine(pool)
	result := engine.Analyze(AnalysisRequest{
		Columns: []ColumnInfo{{Name: "age", Type: "INTEGER"}},
		Rows:    [][]any{{25}, {30}, {35}, {40}, {45}, {50}, {55}, {60}, {65}, {70}},
	})
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	col, ok := result.Columns["age"]
	if !ok {
		t.Fatal("missing column 'age'")
	}
	if col.Type != "numeric" {
		t.Fatalf("expected type 'numeric', got %s", col.Type)
	}
	stats, ok := col.Stats.(map[string]any)
	if !ok {
		t.Fatal("stats not a map")
	}
	if stats["count"] != float64(10) {
		t.Fatalf("expected count=10, got %v", stats["count"])
	}
	if stats["min"] != float64(25) {
		t.Fatalf("expected min=25, got %v", stats["min"])
	}
	if stats["max"] != float64(70) {
		t.Fatalf("expected max=70, got %v", stats["max"])
	}
}
```

Need to add `"database/sql"` and `_ "github.com/marcboeker/go-duckdb"` to imports.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/analysis/
```

Expected: FAIL — Analyze returns Error="not yet implemented"

- [ ] **Step 3: Implement DuckDB temp table creation + numeric stats**

Replace `Analyze` method body:

```go
func (e *Engine) Analyze(req AnalysisRequest) *AnalysisResult {
	start := time.Now()

	if len(req.Columns) == 0 {
		return &AnalysisResult{Error: "no columns provided", ElapsedMs: time.Since(start).Milliseconds()}
	}
	if len(req.Rows) == 0 {
		return &AnalysisResult{Error: "no rows provided", ElapsedMs: time.Since(start).Milliseconds()}
	}

	db := <-e.pool
	defer func() { e.pool <- db }()

	// Build CREATE TABLE statement
	colDefs := make([]string, len(req.Columns))
	for i, c := range req.Columns {
		duckType := mapType(c.Type)
		colDefs[i] = fmt.Sprintf("c%d %s", i, duckType)
	}
	createSQL := "CREATE TEMP TABLE analysis_data AS SELECT * FROM (VALUES "
	rowSQL := make([]string, len(req.Rows))
	for ri, row := range req.Rows {
		vals := make([]string, len(row))
		for vi, v := range row {
			if v == nil {
				vals[vi] = "NULL"
			} else {
				switch val := v.(type) {
				case string:
					vals[vi] = "'" + strings.ReplaceAll(val, "'", "''") + "'"
				default:
					vals[vi] = fmt.Sprintf("%v", v)
				}
			}
		}
		rowSQL[ri] = "(" + strings.Join(vals, ",") + ")"
	}
	createSQL += strings.Join(rowSQL, ",") + ") AS t(" + strings.Join(colDefs, ",") + ")"
	if _, err := db.Exec(createSQL); err != nil {
		return &AnalysisResult{Error: "create temp table: " + err.Error(), ElapsedMs: time.Since(start).Milliseconds()}
	}
	defer db.Exec("DROP TABLE IF EXISTS analysis_data")

	columns := make(map[string]ColumnAnalysis)
	var summary []string

	for _, c := range req.Columns {
		colAlias := fmt.Sprintf("c%d", indexOf(req.Columns, c))
		duckType := mapType(c.Type)

		switch duckType {
		case "INTEGER", "BIGINT", "DOUBLE", "FLOAT", "DECIMAL":
			// Numeric stats
			statsSQL := fmt.Sprintf(`
				SELECT
					COUNT(*), MIN(c%d), MAX(c%d), AVG(c%d), STDDEV_SAMP(c%d),
					COUNT(*) FILTER (WHERE c%d IS NULL),
					COUNT(DISTINCT c%d)
				FROM analysis_data`, indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c))
			var count, nullCount, distinctCount int
			var min, max, avg, stddev *float64
			row := db.QueryRow(statsSQL)
			if err := row.Scan(&count, &min, &max, &avg, &stddev, &nullCount, &distinctCount); err != nil {
				summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
				continue
			}

			stats := map[string]any{
				"count":         count,
				"null_count":    nullCount,
				"distinct":      distinctCount,
				"min":           min,
				"max":           max,
				"mean":          avg,
				"stddev":        stddev,
			}

			// Histogram: use WIDTH_BUCKET
			if min != nil && max != nil && *min < *max {
				histSQL := fmt.Sprintf(`
					SELECT
						WIDTH_BUCKET(c%d, %f, %f, 10) AS bucket,
						MIN(c%d) AS bin_start, MAX(c%d) AS bin_end,
						COUNT(*) AS cnt
					FROM analysis_data
					WHERE c%d IS NOT NULL
					GROUP BY bucket
					ORDER BY bucket`, indexOf(req.Columns, c), *min, *max, indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c))
				hRows, err := db.Query(histSQL)
				if err == nil {
					var bins []Bin
					for hRows.Next() {
						var bucket int
						var b Bin
						if err := hRows.Scan(&bucket, &b.BinStart, &b.BinEnd, &b.Count); err == nil {
							bins = append(bins, b)
						}
					}
					hRows.Close()
					if bins != nil {
						columns[c.Name] = ColumnAnalysis{
							Type:      "numeric",
							Stats:     stats,
							Histogram: bins,
						}
					}
				}
			} else {
				columns[c.Name] = ColumnAnalysis{
					Type:  "numeric",
					Stats: stats,
				}
			}

			// Summary text
			nullPct := 0.0
			if count > 0 {
				nullPct = float64(nullCount) / float64(count) * 100
			}
			summary = append(summary, fmt.Sprintf("%s ranges from %.2f to %.2f (mean: %.2f, stddev: %.2f). %.1f%% null values.",
				c.Name, safeFloat(min), safeFloat(max), safeFloat(avg), safeFloat(stddev), nullPct))

			// Correlations: numeric vs numeric
			// (handled later in a second pass)

		default:
			// categorical — handled in next task
			columns[c.Name] = ColumnAnalysis{
				Type:  "categorical",
				Stats: map[string]any{"note": "deferred to next task"},
			}
		}
	}

	elapsed := time.Since(start).Milliseconds()
	return &AnalysisResult{
		Columns:   columns,
		Summary:   summary,
		ElapsedMs: elapsed,
	}
}
```

Add these helpers:

```go
func mapType(duckType string) string {
	up := strings.ToUpper(duckType)
	switch {
	case strings.Contains(up, "INT") || strings.Contains(up, "DECIMAL") || strings.Contains(up, "FLOAT") || strings.Contains(up, "DOUBLE") || strings.Contains(up, "NUMERIC"):
		return duckType
	case strings.Contains(up, "VARCHAR") || strings.Contains(up, "CHAR") || strings.Contains(up, "TEXT"):
		return "VARCHAR"
	case strings.Contains(up, "TIMESTAMP") || strings.Contains(up, "DATE") || strings.Contains(up, "TIME"):
		return "TIMESTAMP"
	case up == "BOOLEAN" || up == "BOOL":
		return "BOOLEAN"
	default:
		return "VARCHAR"
	}
}

func indexOf(cols []ColumnInfo, target ColumnInfo) int {
	for i, c := range cols {
		if c.Name == target.Name && c.Type == target.Type {
			return i
		}
	}
	return -1
}

func safeFloat(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
```

Need to add imports: `"database/sql"`, `"fmt"`, `"strings"`, `"time"`, `_ "github.com/marcboeker/go-duckdb"`

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -v ./internal/analysis/
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/analysis/
git commit -m "feat: analysis engine with DuckDB temp table and numeric stats"
```

---

### Task 3: Analysis engine – categorical, temporal, boolean stats + correlations + summary

**Files:**
- Modify: `internal/analysis/engine.go`

- [ ] **Step 1: Add categorical stats branch**

After the numeric branch's closing `}`, inside the `switch`:

```go
	case "VARCHAR":
		// Categorical stats
		var count, nullCount, distinctCount int
		countSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE c%d IS NULL), COUNT(DISTINCT c%d) FROM analysis_data",
			indexOf(req.Columns, c), indexOf(req.Columns, c))
		row := db.QueryRow(countSQL)
		if err := row.Scan(&count, &nullCount, &distinctCount); err != nil {
			summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
			continue
		}

		stats := map[string]any{
			"count":      count,
			"null_count": nullCount,
			"distinct":   distinctCount,
		}

		// Top 10 values
		topSQL := fmt.Sprintf("SELECT c%d, COUNT(*) as cnt FROM analysis_data WHERE c%d IS NOT NULL GROUP BY c%d ORDER BY cnt DESC LIMIT 10",
			indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c))
		tRows, err := db.Query(topSQL)
		var topValues []ValueCount
		if err == nil {
			for tRows.Next() {
				var val string
				var cnt int
				if err := tRows.Scan(&val, &cnt); err == nil {
					pct := 0.0
					if count > 0 {
						pct = float64(cnt) / float64(count) * 100
					}
					topValues = append(topValues, ValueCount{Value: val, Count: cnt, Pct: pct})
				}
			}
			tRows.Close()
		}

		columns[c.Name] = ColumnAnalysis{
			Type:      "categorical",
			Stats:     stats,
			TopValues: topValues,
		}

		top1 := ""
		top1Pct := 0.0
		if len(topValues) > 0 {
			top1 = topValues[0].Value
			top1Pct = topValues[0].Pct
		}
		summary = append(summary, fmt.Sprintf("%s has %d distinct values; '%s' is most common (%.1f%%).",
			c.Name, distinctCount, top1, top1Pct))

	case "TIMESTAMP":
		// Temporal stats
		var count, nullCount int
		var minStr, maxStr *string
		countSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE c%d IS NULL), MIN(c%d::VARCHAR), MAX(c%d::VARCHAR) FROM analysis_data",
			indexOf(req.Columns, c), indexOf(req.Columns, c), indexOf(req.Columns, c))
		row := db.QueryRow(countSQL)
		if err := row.Scan(&count, &nullCount, &minStr, &maxStr); err != nil {
			summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
			continue
		}

		minVal := ""
		maxVal := ""
		if minStr != nil {
			minVal = *minStr
		}
		if maxStr != nil {
			maxVal = *maxStr
		}

		columns[c.Name] = ColumnAnalysis{
			Type: "temporal",
			Stats: map[string]any{
				"count":      count,
				"null_count": nullCount,
				"min":        minVal,
				"max":        maxVal,
			},
		}
		summary = append(summary, fmt.Sprintf("%s spans from %s to %s.", c.Name, minVal, maxVal))

	case "BOOLEAN":
		// Boolean stats
		var count, nullCount, trueCount int
		boolSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE c%d IS NULL), SUM(CASE WHEN c%d THEN 1 ELSE 0 END) FROM analysis_data",
			indexOf(req.Columns, c), indexOf(req.Columns, c))
		row := db.QueryRow(boolSQL)
		if err := row.Scan(&count, &nullCount, &trueCount); err != nil {
			summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
			continue
		}

		truePct := 0.0
		if count > 0 {
			truePct = float64(trueCount) / float64(count) * 100
		}
		columns[c.Name] = ColumnAnalysis{
			Type: "boolean",
			Stats: map[string]any{
				"count":      count,
				"null_count": nullCount,
				"true_count": trueCount,
				"true_pct":   truePct,
			},
		}
		summary = append(summary, fmt.Sprintf("%s is true for %.1f%% of rows.", c.Name, truePct))
```

- [ ] **Step 2: Add correlation pass after column loop**

After the column loop and before the return, add numeric-numeric correlations:

```go
	// Correlations: numeric vs numeric
	numCols := make([]ColumnInfo, 0)
	for _, c := range req.Columns {
		if strings.Contains(strings.ToUpper(mapType(c.Type)), "INT") ||
			strings.Contains(strings.ToUpper(mapType(c.Type)), "FLOAT") ||
			strings.Contains(strings.ToUpper(mapType(c.Type)), "DOUBLE") ||
			strings.Contains(strings.ToUpper(mapType(c.Type)), "DECIMAL") {
			numCols = append(numCols, c)
		}
	}
	var correlations []Correlation
	for i := 0; i < len(numCols); i++ {
		for j := i + 1; j < len(numCols); j++ {
			ci := indexOf(req.Columns, numCols[i])
			cj := indexOf(req.Columns, numCols[j])
			corrSQL := fmt.Sprintf("SELECT CORR(c%d, c%d) FROM analysis_data", ci, cj)
			var corrVal *float64
			if err := db.QueryRow(corrSQL).Scan(&corrVal); err == nil && corrVal != nil {
				correlations = append(correlations, Correlation{
					ColA: numCols[i].Name, ColB: numCols[j].Name,
					Type: "pearson", Value: *corrVal,
				})
			}
		}
	}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/analysis/
```

- [ ] **Step 4: Update tests to cover categorical, temporal, boolean**

```go
func TestAnalyzeCategoricalColumn(t *testing.T) {
	pool := make(chan *sql.DB, 1)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	pool <- db

	engine := NewEngine(pool)
	result := engine.Analyze(AnalysisRequest{
		Columns: []ColumnInfo{{Name: "country", Type: "VARCHAR"}},
		Rows:    [][]any{{"China"}, {"China"}, {"Italy"}, {"Italy"}, {"Italy"}, {"USA"}, {"USA"}, {nil}},
	})
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	col, ok := result.Columns["country"]
	if !ok {
		t.Fatal("missing column 'country'")
	}
	if col.Type != "categorical" {
		t.Fatalf("expected 'categorical', got %s", col.Type)
	}
	if len(col.TopValues) == 0 {
		t.Fatal("expected top values")
	}
	if col.TopValues[0].Value != "Italy" {
		t.Fatalf("expected top value 'Italy', got %s", col.TopValues[0].Value)
	}
}

func TestAnalyzeCorrelations(t *testing.T) {
	pool := make(chan *sql.DB, 1)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	pool <- db

	engine := NewEngine(pool)
	result := engine.Analyze(AnalysisRequest{
		Columns: []ColumnInfo{{Name: "x", Type: "DOUBLE"}, {Name: "y", Type: "DOUBLE"}},
		Rows:    [][]any{{1.0, 2.0}, {2.0, 4.0}, {3.0, 6.0}, {4.0, 8.0}, {5.0, 10.0}},
	})
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.Correlations) == 0 {
		t.Fatal("expected at least one correlation")
	}
	// Perfect linear relationship -> CORR should be close to 1.0
	if result.Correlations[0].Value < 0.99 {
		t.Fatalf("expected correlation near 1.0, got %f", result.Correlations[0].Value)
	}
}
```

- [ ] **Step 5: Run tests**

```bash
go test -v ./internal/analysis/
```

Expected: all 3 tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/analysis/
git commit -m "feat: categorical, temporal, boolean stats with correlations"
```

---

### Task 4: Analysis engine – add summary text generation

**Files:**
- Modify: `internal/analysis/engine.go`

This is already integrated in the per-column branches from Task 2/3. Verify the summary text is generated for each column type and add a test:

- [ ] **Step 1: Add summary test**

```go
func TestAnalyzeSummary(t *testing.T) {
	pool := make(chan *sql.DB, 1)
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	pool <- db

	engine := NewEngine(pool)
	result := engine.Analyze(AnalysisRequest{
		Columns: []ColumnInfo{{Name: "val", Type: "INTEGER"}},
		Rows:    [][]any{{1}, {2}, {3}, {4}, {5}},
	})
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.Summary) == 0 {
		t.Fatal("expected summary text")
	}
	t.Logf("Summary: %s", result.Summary[0])
}
```

- [ ] **Step 2: Run tests**

```bash
go test -v ./internal/analysis/
```

Expected: all pass

- [ ] **Step 3: Commit**

```bash
git add internal/analysis/engine_test.go
git commit -m "test: add summary text generation test"
```

---

### Task 5: Report store – model and interface

**Files:**
- Create: `internal/report/model.go`
- Create: `internal/report/store.go`
- Create: `internal/report/store_test.go`

- [ ] **Step 1: Create model.go**

```go
package report

import (
	"time"
)

type ChartConfig struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	XColumn   string `json:"x_column"`
	YColumn   string `json:"y_column"`
	GroupBy   string `json:"group_by,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Title     string `json:"title,omitempty"`
	SortOrder string `json:"sort_order,omitempty"`
	MaxGroups int    `json:"max_groups,omitempty"`
}

type Report struct {
	ID           string        `json:"id"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	Title        string        `json:"title"`
	SQL          string        `json:"sql"`
	ProjectID    string        `json:"project_id"`
	QueryColumns []ColumnInfo  `json:"query_columns"`
	QueryRows    [][]any       `json:"query_rows"`
	Analysis     any           `json:"analysis"`
	Charts       []ChartConfig `json:"charts"`
}

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ReportSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	RowCount  int       `json:"row_count"`
}

type Store interface {
	List() ([]ReportSummary, error)
	Save(report *Report) error
	Get(id string) (*Report, error)
	Delete(id string) error
}
```

- [ ] **Step 2: Create store.go**

```go
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type DiskStore struct {
	baseDir string
}

func NewDiskStore(baseDir string) (*DiskStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create report dir: %w", err)
	}
	return &DiskStore{baseDir: baseDir}, nil
}

func (s *DiskStore) path(id string) string {
	return filepath.Join(s.baseDir, id+".json")
}

func (s *DiskStore) List() ([]ReportSummary, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("read report dir: %w", err)
	}
	var summaries []ReportSummary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		rowCount := 0
		if r.QueryRows != nil {
			rowCount = len(r.QueryRows)
		}
		summaries = append(summaries, ReportSummary{
			ID: r.ID, Title: r.Title, CreatedAt: r.CreatedAt, RowCount: rowCount,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

func (s *DiskStore) Save(report *Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(s.path(report.ID), data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func (s *DiskStore) Get(id string) (*Report, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", id, err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", id, err)
	}
	return &r, nil
}

func (s *DiskStore) Delete(id string) error {
	if err := os.Remove(s.path(id)); err != nil {
		return fmt.Errorf("delete report %s: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 3: Create store_test.go**

```go
package report

import (
	"os"
	"testing"
	"time"
	"github.com/google/uuid"
)

func TestDiskStoreCRUD(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-reports-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	r := &Report{
		ID:        uuid.New().String(),
		CreatedAt: time.Now(),
		Title:     "Test Report",
		SQL:       "SELECT * FROM test",
		QueryRows: [][]any{{"hello"}, {"world"}},
		Charts:    []ChartConfig{{ID: "c1", Type: "bar", XColumn: "x"}},
	}

	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Test Report" {
		t.Fatalf("expected title 'Test Report', got %s", loaded.Title)
	}
	if len(loaded.Charts) != 1 {
		t.Fatalf("expected 1 chart, got %d", len(loaded.Charts))
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 report in list, got %d", len(list))
	}
	if list[0].RowCount != 2 {
		t.Fatalf("expected row_count 2, got %d", list[0].RowCount)
	}

	if err := store.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(r.ID); err == nil {
		t.Fatal("expected error after delete")
	}
}
```

Need to add `"github.com/google/uuid"` — this is already in go.mod as an indirect dep.

- [ ] **Step 4: Run tests**

```bash
go test -v ./internal/report/
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/report/
git commit -m "feat: report store with disk-backed CRUD"
```

---

### Task 6: API handlers – /analyze and /api/reports CRUD

**Files:**
- Create: `internal/api/analysis_handler.go`
- Create: `internal/api/report_handler.go`

- [ ] **Step 1: Create analysis_handler.go**

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/analysis"
)

type AnalysisHandler struct {
	engine *analysis.Engine
}

func NewAnalysisHandler(engine *analysis.Engine) *AnalysisHandler {
	return &AnalysisHandler{engine: engine}
}

func (h *AnalysisHandler) Analyze(w http.ResponseWriter, r *http.Request) {
	var req analysis.AnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	result := h.engine.Analyze(req)

	w.Header().Set("Content-Type", "application/json")
	if result.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	json.NewEncoder(w).Encode(result)
}
```

- [ ] **Step 2: Create report_handler.go**

```go
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/report"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ReportHandler struct {
	store report.Store
}

func NewReportHandler(store report.Store) *ReportHandler {
	return &ReportHandler{store: store}
}

type reportSaveRequest struct {
	Title        string              `json:"title"`
	SQL          string              `json:"sql"`
	ProjectID    string              `json:"project_id"`
	QueryColumns []report.ColumnInfo `json:"query_columns"`
	QueryRows    [][]any             `json:"query_rows"`
	Analysis     any                 `json:"analysis"`
	Charts       []report.ChartConfig `json:"charts"`
}

func (h *ReportHandler) List(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.store.List()
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []report.ReportSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reports": summaries})
}

func (h *ReportHandler) Save(w http.ResponseWriter, r *http.Request) {
	var req reportSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	now := time.Now()
	rep := &report.Report{
		ID:           uuid.New().String(),
		CreatedAt:    now,
		UpdatedAt:    now,
		Title:        req.Title,
		SQL:          req.SQL,
		ProjectID:    req.ProjectID,
		QueryColumns: req.QueryColumns,
		QueryRows:    req.QueryRows,
		Analysis:     req.Analysis,
		Charts:       req.Charts,
	}

	if err := h.store.Save(rep); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rep)
}

func (h *ReportHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, err := h.store.Get(id)
	if err != nil {
		http.Error(w, `{"error":"report not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rep)
}

func (h *ReportHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.Delete(id); err != nil {
		http.Error(w, `{"error":"report not found"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/api/
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/
git commit -m "feat: /analyze and /api/reports CRUD handlers"
```

---

### Task 7: Wire routes and engine in main.go

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

- [ ] **Step 1: Add imports and initialize analysis engine + report store**

Add imports: `"github.com/esignoretti/ds3-sql-server/internal/analysis"`, `"github.com/esignoretti/ds3-sql-server/internal/report"`, `"github.com/go-chi/chi/v5"` (already imported).

After the schemaHandler initialization, add:

```go
	// Analysis engine
	analysisEngine := analysis.NewEngine(queryEngine.Pool())
	analysisHandler := api.NewAnalysisHandler(analysisEngine)

	// Report store
	reportDir := os.Getenv("DS3SQL_REPORT_DIR")
	if reportDir == "" {
		home, _ := os.UserHomeDir()
		reportDir = home + "/.ds3sql/reports"
	}
	reportStore, err := report.NewDiskStore(reportDir)
	if err != nil {
		log.Fatalf("failed to init report store: %v", err)
	}
	reportHandler := api.NewReportHandler(reportStore)
```

Need to add `Pool()` method on query.Engine that returns the pool channel (for analysis engine to share the pool). Add to `internal/query/engine.go`:

```go
func (e *Engine) Pool() chan *sql.DB {
	return e.pool
}
```

- [ ] **Step 2: Register new routes inside the authenticated group**

After the schema route, add:

```go
		r.Post("/analyze", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					analysisHandler.Analyze(w, r)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		r.Get("/api/reports", reportHandler.List)
		r.Post("/api/reports", reportHandler.Save)
		r.Get("/api/reports/{id}", reportHandler.Get)
		r.Delete("/api/reports/{id}", reportHandler.Delete)
```

- [ ] **Step 3: Add report page routes**

After the existing `/browse` and `/query` protected page routes:

```go
		r.Get("/report", webHandler.ReportPage)
		r.Get("/reports", webHandler.ReportsPage)
```

- [ ] **Step 4: Verify it compiles**

```bash
go build ./cmd/ds3sql-server/
```

- [ ] **Step 5: Commit**

```bash
git add cmd/ds3sql-server/main.go internal/query/engine.go
git commit -m "feat: wire analysis engine, report store, and new routes"
```

---

### Task 8: Web UI – report page template + handler methods

**Files:**
- Create: `internal/web/templates/report.html`
- Create: `internal/web/templates/reports_list.html`
- Modify: `internal/web/handler.go`
- Modify: `internal/web/templates/layout.html`

- [ ] **Step 1: Add handler methods**

Add to `internal/web/handler.go`:

```go
func (h *Handler) ReportPage(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	data := PageData{LoggedIn: true, Page: "report", Projects: session.Projects}
	h.render(w, "layout.html", data)
}

func (h *Handler) ReportsPage(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	data := PageData{LoggedIn: true, Page: "reports", Projects: session.Projects}
	h.render(w, "layout.html", data)
}
```

- [ ] **Step 2: Update layout.html template switch**

In the template block for authenticated pages, add:

```
{{else if eq .Page "report"}}{{template "report" .}}
{{else if eq .Page "reports"}}{{template "reports_list" .}}
```

- [ ] **Step 3: Add Reports nav link to sidebar**

After the Console link:

```html
<li><a href="/reports" class="{{if eq .Page "reports"}}active{{end}}">Reports</a></li>
```

- [ ] **Step 4: Create reports_list.html (saved reports page)**

```html
{{define "reports_list"}}
<div class="single-page">
  <h2 style="margin-bottom:1rem;">Saved Reports</h2>
  <div id="report-list">
    <p style="color:var(--text-muted);">Loading...</p>
  </div>
</div>

<script>
fetch('/api/reports')
  .then(function(r) { return r.json(); })
  .then(function(d) {
    var html = '';
    if (!d.reports || !d.reports.length) {
      html = '<p style="color:var(--text-muted);">No saved reports. Run a query and analyze it to create one.</p>';
    } else {
      html = '<table><thead><tr><th>Title</th><th>Created</th><th>Rows</th><th></th></tr></thead><tbody>';
      d.reports.forEach(function(r) {
        html += '<tr><td><a href="/report?id=' + encodeURIComponent(r.id) + '">' + escHtml(r.title) + '</a></td><td>' + new Date(r.created_at).toLocaleString() + '</td><td>' + r.row_count + '</td>';
        html += '<td><button class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.6rem;" onclick="deleteReport(\'' + r.id + '\',this)">Delete</button></td></tr>';
      });
      html += '</tbody></table>';
    }
    document.getElementById('report-list').innerHTML = html;
  })
  .catch(function(e) { document.getElementById('report-list').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });

function deleteReport(id, btn) {
  if (!confirm('Delete this report?')) return;
  fetch('/api/reports/' + encodeURIComponent(id), {method: 'DELETE'})
    .then(function() { btn.closest('tr').remove(); })
    .catch(function(e) { alert('Error: ' + e.message); });
}
</script>
{{end}}
```

- [ ] **Step 5: Create report.html stub**

Full report page will be built in Task 9 with Charts.js. For now, create a minimal page that receives analysis data via URL query parameter or POST body:

```html
{{define "report"}}
<div class="single-page">
  <div id="report-app">
    <p style="color:var(--text-muted);">Loading report...</p>
  </div>
</div>

<script>
// Load report data from URL param or from sessionStorage
var reportId = new URLSearchParams(window.location.search).get('id');
var analysisData = null;

if (reportId) {
  fetch('/api/reports/' + encodeURIComponent(reportId))
    .then(function(r) { return r.json(); })
    .then(function(d) { renderReport(d); })
    .catch(function(e) { document.getElementById('report-app').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
} else {
  analysisData = JSON.parse(sessionStorage.getItem('ds3sql_last_analysis'));
  if (!analysisData) {
    document.getElementById('report-app').innerHTML = '<p style="color:var(--text-muted);">No analysis data. Run a query and click Analyze to create a report.</p>';
  } else {
    renderReport(analysisData);
  }
}

function renderReport(data) {
  document.getElementById('report-app').innerHTML = '<p>Report page loaded. Full chart UI coming in next task.</p><pre>' + JSON.stringify(data, null, 2) + '</pre>';
}
</script>
{{end}}
```

- [ ] **Step 6: Verify it compiles**

```bash
go build ./internal/web/ && go build ./cmd/ds3sql-server/
```

- [ ] **Step 7: Commit**

```bash
git add internal/web/
git commit -m "feat: add report page templates and handler methods"
```

---

### Task 9: Web UI – chart builder with Chart.js + "Analyze" button in browse page

**Files:**
- Create: `internal/web/static/report.js`
- Modify: `internal/web/templates/report.html`
- Modify: `internal/web/templates/browse.html`
- Modify: `internal/web/templates/layout.html`
- Modify: `internal/web/static/style.css`

- [ ] **Step 1: Add Chart.js CDN to layout.html**

```html
<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.min.js"></script>
<script src="https://unpkg.com/htmx.org@1.9.12"></script>
```

- [ ] **Step 2: Add "Analyze" button to browse.html after query results**

In the export/controls toolbar section, after the page controls, add:

```html
<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.25rem 0.75rem;" onclick="analyzeResults()">📊 Analyze</button>
```

- [ ] **Step 3: Add analyzeResults JavaScript function to browse.html**

```javascript
function analyzeResults() {
  if (!lastResult) { alert('Run a query first'); return; }
  var status = document.getElementById('query-status');
  status.innerHTML = 'Analyzing...';
  fetch('/analyze?project=' + encodeURIComponent(selProject), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      columns: lastResult.columns,
      rows: lastResult.rows
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { status.innerHTML = '<span class="error">' + d.error + '</span>'; return; }
    sessionStorage.setItem('ds3sql_last_analysis', JSON.stringify(d));
    sessionStorage.setItem('ds3sql_last_query', JSON.stringify({
      sql: document.getElementById('sql-editor').value,
      columns: lastResult.columns,
      rows: lastResult.rows
    }));
    window.location.href = '/report';
  })
  .catch(function(e) { status.innerHTML = '<span class="error">Error: ' + e.message + '</span>'; });
}
```

- [ ] **Step 4: Create report.js – chart builder logic**

Create `internal/web/static/report.js`:

```javascript
var reportState = {
  columns: [],
  rows: [],
  analysis: null,
  charts: [],
  title: 'Untitled Report',
  sql: '',
  projectId: ''
};

function initReport(data, queryData) {
  if (data) {
    reportState.analysis = data;
    reportState.columns = queryData ? queryData.columns : [];
    reportState.rows = queryData ? queryData.rows : [];
    reportState.sql = queryData ? queryData.sql : '';
    renderReport();
  }
}

function renderReport() {
  var app = document.getElementById('report-app');
  if (!reportState.analysis) { app.innerHTML = '<p>No analysis data</p>'; return; }

  var html = '<div class="report-layout">';

  // Top bar
  html += '<div class="report-topbar">';
  html += '<input type="text" id="report-title" value="' + escHtml(reportState.title) + '" class="input" style="font-size:1.25rem;font-weight:600;width:400px;background:transparent;border:none;color:var(--text);" onchange="reportState.title=this.value">';
  html += '<div style="display:flex;gap:0.5rem;">';
  html += '<button class="btn btn-secondary" onclick="saveReport()">💾 Save</button>';
  html += '<button class="btn btn-secondary" onclick="window.print()">📄 Export PDF</button>';
  html += '</div></div>';

  // Main content: left sidebar + right canvas
  html += '<div class="report-body">';
  html += '<div class="report-sidebar">';
  html += '<h3>Columns</h3>';
  reportState.columns.forEach(function(c, i) {
    html += '<label class="report-column-label"><input type="checkbox" checked data-col="' + i + '" onchange="toggleColumn(' + i + ',this.checked)"> ' + escHtml(c.name) + ' <span class="type-badge">' + c.type + '</span></label>';
  });
  html += '<h3 style="margin-top:1.5rem;">Add Chart</h3>';
  html += '<div class="chart-types">';
  ['Bar','Pie','Line','Scatter','Histogram'].forEach(function(t) {
    html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.3rem 0.6rem;" onclick="addChart(\'' + t.toLowerCase() + '\')">' + t + '</button>';
  });
  html += '</div>';
  html += '</div>';

  // Main canvas
  html += '<div class="report-canvas">';

  // Stats summary
  html += '<div class="stats-summary"><h3>Summary</h3>';
  if (reportState.analysis.summary) {
    html += '<ul>';
    reportState.analysis.summary.forEach(function(s) {
      html += '<li>' + escHtml(s) + '</li>';
    });
    html += '</ul>';
  }
  html += '</div>';

  // Charts
  html += '<div id="chart-container"></div>';

  html += '</div></div></div>';

  app.innerHTML = html;
  renderCharts();
}

function toggleColumn(idx, visible) {
  // Filter data for charts
  renderCharts();
}

function addChart(type) {
  var id = 'c' + Date.now();
  reportState.charts.push({
    id: id,
    type: type,
    x_column: reportState.columns[0] ? reportState.columns[0].name : '',
    y_column: reportState.columns.length > 1 ? reportState.columns[1].name : '',
    group_by: '',
    bucket: 'auto',
    title: type.charAt(0).toUpperCase() + type.slice(1),
    max_groups: 10
  });
  renderCharts();
}

function renderCharts() {
  var container = document.getElementById('chart-container');
  if (!container) return;
  var html = '';
  reportState.charts.forEach(function(chart, idx) {
    html += '<div class="chart-card" id="chart-' + chart.id + '">';
    html += '<div class="chart-card-header">';
    html += '<input type="text" value="' + escHtml(chart.title) + '" style="background:transparent;border:none;color:var(--text);font-weight:600;" onchange="reportState.charts[' + idx + '].title=this.value">';
    html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.5rem;" onclick="removeChart(\'' + chart.id + '\')">✕</button>';
    html += '</div>';
    html += '<div class="chart-config">';
    html += '<label>X: <select onchange="reportState.charts[' + idx + '].x_column=this.value">';
    reportState.columns.forEach(function(c) {
      html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.x_column ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
    });
    html += '</select></label>';
    if (chart.type !== 'pie' && chart.type !== 'histogram') {
      html += '<label>Y: <select onchange="reportState.charts[' + idx + '].y_column=this.value">';
      reportState.columns.forEach(function(c) {
        html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.y_column ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
      });
      html += '</select></label>';
    }
    html += '<label>Group: <select onchange="reportState.charts[' + idx + '].group_by=this.value"><option value="">None</option>';
    reportState.columns.forEach(function(c) {
      html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.group_by ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
    });
    html += '</select></label>';
    if (chart.type === 'line') {
      html += '<label>Bucket: <select onchange="reportState.charts[' + idx + '].bucket=this.value">';
      ['auto','hour','day','week','month'].forEach(function(b) {
        html += '<option value="' + b + '"' + (b === chart.bucket ? ' selected' : '') + '>' + b + '</option>';
      });
      html += '</select></label>';
    }
    html += '</div>';
    html += '<div class="chart-canvas-wrapper"><canvas id="canvas-' + chart.id + '"></canvas></div>';
    html += '</div>';
  });
  container.innerHTML = html;

  // Render each chart
  reportState.charts.forEach(function(chart) {
    var canvas = document.getElementById('canvas-' + chart.id);
    if (!canvas) return;
    renderChart(canvas, chart);
  });
}

function renderChart(canvas, config) {
  var ctx = canvas.getContext('2d');
  // Destroy existing chart if any
  if (canvas._chart) { canvas._chart.destroy(); }

  var data = buildChartData(config);

  var chartConfig = {
    type: config.type === 'histogram' ? 'bar' : config.type,
    data: {
      labels: data.labels,
      datasets: data.datasets
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { labels: { color: '#DEE4EA' } }
      },
      scales: {
        x: { ticks: { color: '#9099A1' }, grid: { color: '#31393F' } },
        y: { ticks: { color: '#9099A1' }, grid: { color: '#31393F' } }
      }
    }
  };
  // Special handling for pie
  if (config.type === 'pie') {
    chartConfig.options.scales = {};
  }

  canvas._chart = new Chart(ctx, chartConfig);
}

function buildChartData(config) {
  var labels = [];
  var datasets = [];

  // Basic single-series
  if (config.type === 'pie') {
    var counts = {};
    reportState.rows.forEach(function(row) {
      var idx = reportState.columns.findIndex(function(c) { return c.name === config.x_column; });
      if (idx < 0) return;
      var val = String(row[idx] || 'null');
      counts[val] = (counts[val] || 0) + 1;
    });
    var entries = Object.entries(counts).sort(function(a,b) { return b[1] - a[1]; }).slice(0, 20);
    labels = entries.map(function(e) { return e[0]; });
    datasets = [{
      data: entries.map(function(e) { return e[1]; }),
      backgroundColor: CHART_COLORS.slice(0, entries.length)
    }];
    return {labels: labels, datasets: datasets};
  }

  if (config.type === 'histogram') {
    var col = reportState.analysis.columns[config.x_column];
    if (col && col.histogram) {
      labels = col.histogram.map(function(b) { return b.bin_start.toFixed(1) + '-' + b.bin_end.toFixed(1); });
      datasets = [{ data: col.histogram.map(function(b) { return b.count; }), backgroundColor: CHART_COLORS[0] }];
    }
    return {labels: labels, datasets: datasets};
  }

  // Bar, Line, Scatter – with optional group_by
  var xIdx = reportState.columns.findIndex(function(c) { return c.name === config.x_column; });
  var yIdx = reportState.columns.findIndex(function(c) { return c.name === config.y_column; });
  var gIdx = config.group_by ? reportState.columns.findIndex(function(c) { return c.name === config.group_by; }) : -1;

  if (xIdx < 0 || yIdx < 0) return {labels: [], datasets: []};

  if (config.group_by && gIdx >= 0) {
    // Grouped: aggregate per (x, group)
    var map = {};
    reportState.rows.forEach(function(row) {
      var xv = String(row[xIdx] ?? 'null');
      var gv = String(row[gIdx] ?? 'null');
      var yv = parseFloat(row[yIdx]);
      if (isNaN(yv)) return;
      var key = xv + '||' + gv;
      if (!map[key]) map[key] = {sum: 0, count: 0};
      map[key].sum += yv;
      map[key].count++;
    });
    // Collect unique groups
    var groupSet = new Set();
    Object.keys(map).forEach(function(k) { groupSet.add(k.split('||')[1]); });
    var groups = Array.from(groupSet).slice(0, config.max_groups || 10);
    var xSet = new Set();
    Object.keys(map).forEach(function(k) { xSet.add(k.split('||')[0]); });
    labels = Array.from(xSet);
    datasets = groups.map(function(g, gi) {
      return {
        label: g,
        data: labels.map(function(l) {
          var key = l + '||' + g;
          return map[key] ? (map[key].sum / map[key].count) : 0;
        }),
        backgroundColor: CHART_COLORS[gi % CHART_COLORS.length]
      };
    });
  } else {
    // Simple aggregation
    var agg = {};
    reportState.rows.forEach(function(row) {
      var xv = String(row[xIdx] ?? 'null');
      var yv = parseFloat(row[yIdx]);
      if (isNaN(yv)) return;
      if (!agg[xv]) agg[xv] = {sum: 0, count: 0};
      agg[xv].sum += yv;
      agg[xv].count++;
    });
    var entries = Object.entries(agg).sort(function(a,b) { return b[1].sum - a[1].sum; });
    labels = entries.map(function(e) { return e[0]; });
    datasets = [{
      data: entries.map(function(e) { return e[1].sum / e[1].count; }),
      backgroundColor: CHART_COLORS[0]
    }];
  }

  return {labels: labels, datasets: datasets};
}

var CHART_COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6'];

function removeChart(id) {
  reportState.charts = reportState.charts.filter(function(c) { return c.id !== id; });
  renderCharts();
}

function saveReport() {
  var queryData = JSON.parse(sessionStorage.getItem('ds3sql_last_query') || '{}');
  var body = {
    title: reportState.title,
    sql: queryData.sql || '',
    project_id: '',
    query_columns: reportState.columns,
    query_rows: reportState.rows,
    analysis: reportState.analysis,
    charts: reportState.charts
  };
  fetch('/api/reports', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body)
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    alert('Report saved! ID: ' + d.id);
    window.history.replaceState(null, '', '/report?id=' + encodeURIComponent(d.id));
  })
  .catch(function(e) { alert('Error saving: ' + e.message); });
}

function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function escAttr(s) { return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;'); }
```

- [ ] **Step 5: Update report.html to use report.js**

Replace the report.html with:

```html
{{define "report"}}
<div class="single-page">
  <div id="report-app">
    <div style="text-align:center;padding:4rem 0;">
      <p style="color:var(--text-muted);">Loading report...</p>
    </div>
  </div>
</div>

<script src="/static/report.js"></script>
<script>
var reportId = new URLSearchParams(window.location.search).get('id');

if (reportId) {
  fetch('/api/reports/' + encodeURIComponent(reportId))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      reportState.title = d.title || 'Untitled Report';
      reportState.columns = d.query_columns || [];
      reportState.rows = d.query_rows || [];
      reportState.sql = d.sql || '';
      reportState.charts = d.charts || [];
      initReport(d.analysis, d);
    })
    .catch(function(e) { document.getElementById('report-app').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
} else {
  var analysisData = JSON.parse(sessionStorage.getItem('ds3sql_last_analysis') || 'null');
  var queryData = JSON.parse(sessionStorage.getItem('ds3sql_last_query') || 'null');
  if (!analysisData) {
    document.getElementById('report-app').innerHTML = '<p style="color:var(--text-muted);">No analysis data. Run a query and click Analyze.</p>';
  } else {
    reportState.sql = queryData ? queryData.sql : '';
    initReport(analysisData, queryData);
  }
}
</script>
{{end}}
```

- [ ] **Step 6: Add report page styles to style.css**

```css
.report-layout { display:flex; flex-direction:column; gap:1rem; }
.report-topbar { display:flex; justify-content:space-between; align-items:center; background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:0.75rem 1rem; }
.report-body { display:flex; gap:1rem; }
.report-sidebar { width:280px; flex-shrink:0; background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:1rem; }
.report-sidebar h3 { font-size:0.95rem; margin-bottom:0.75rem; }
.report-canvas { flex:1; display:flex; flex-direction:column; gap:1rem; }
.report-column-label { display:flex; align-items:center; gap:0.375rem; font-size:0.85rem; color:var(--text); padding:0.25rem 0; cursor:pointer; }
.type-badge { font-size:0.7rem; color:var(--text-muted); background:var(--surface-2); padding:0.1rem 0.4rem; border-radius:0.25rem; }
.chart-types { display:flex; flex-wrap:wrap; gap:0.375rem; margin-bottom:1rem; }
.stats-summary { background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:1rem; }
.stats-summary ul { list-style:disc; padding-left:1.25rem; font-size:0.875rem; color:var(--text); }
.stats-summary li { margin-bottom:0.25rem; }
.chart-card { background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:1rem; }
.chart-card-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:0.5rem; }
.chart-config { display:flex; gap:0.75rem; flex-wrap:wrap; margin-bottom:0.75rem; font-size:0.8rem; }
.chart-config label { display:flex; align-items:center; gap:0.25rem; color:var(--text-muted); }
.chart-config select { background:var(--surface-2); color:var(--text); border:0.0625rem solid var(--border); border-radius:var(--radius); padding:0.2rem 0.4rem; font-size:0.8rem; }
.chart-canvas-wrapper { position:relative; height:300px; }

@media print {
  .sidebar, .report-sidebar, .report-topbar button { display:none !important; }
  .report-body { display:block; }
  .report-canvas { margin:0; }
  .chart-card { break-inside:avoid; }
}
```

- [ ] **Step 7: Verify it compiles**

```bash
go build ./internal/web/ && go build ./cmd/ds3sql-server/
```

- [ ] **Step 8: Commit**

```bash
git add internal/web/
git commit -m "feat: chart builder with Chart.js, report page, analyze button"
```

---

### Task 10: Final build and verify

- [ ] **Step 1: Run full test suite**

```bash
go test -v -race ./internal/analysis/ ./internal/report/ ./internal/query/ ./internal/s3/ ./internal/auth/
```

Expected: all pass

- [ ] **Step 2: Build binaries**

```bash
make build
```

Expected: `ds3sql-server` and `ds3sql` produced

- [ ] **Step 3: Push and commit**

```bash
git add -A && git status
# Verify only expected files changed
git commit -m "feat: statistical analysis, chart builder, and report storage

- Analysis engine creates DuckDB temp tables for per-column stats
- Numeric, categorical, temporal, boolean analysis with histograms
- Column-pair correlations with automatic summary text
- Report store as JSON files on disk with full CRUD API
- Chart.js integration with bar, pie, line, scatter, histogram
- Group/Color by column for multi-series charts
- Temporal bucket aggregation (auto/hour/day/week/month)
- Report page with column selection, save/load, PDF export"
git push origin main
```
