# DS3 SQL Phase 4 (Product Polish) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. For frontend tasks, the server-rendered fragments / JSON endpoints are test-driven with `httptest`; the interactive (browser) behaviour is verified with an explicit MANUAL step.

**Goal:** Make the catalog the primary product surface. Add a catalog browser tree (datasets → tables → columns) to the `/app` Web UI as the primary left-nav, demoting raw bucket browsing to a secondary tab; add a jobs/history panel to the query tab; wire analyze/report to catalog-driven query results; add a production-grade **Postgres** metastore behind the existing `Store` interface; and refine **partition pruning** so `WHERE` predicates over partition columns reduce the scanned object set.

**Architecture:** Incremental in-place refactor (Approach A). No new control-plane topology — Phase 4 is product polish on top of the Phases 1–3 coordinator. New server-side code: a `metastore.PostgresStore` implementing the *full* `Store` interface (Phase 1 + Phase 2 + Phase 3 methods), selected by a new `Metastore.Driver` config; a pure `internal/planner` package whose `Prune` function maps `WHERE` predicates + a table's `[]Partition` stats to a reduced set of reader locations; and a thin catalog hook (`ResolvePruned`) that builds `query.ViewBinding.ReaderSQL` from the pruned list. New frontend code: a `catalog.js` module + template fragments + CSS for the tree; a `jobs.js` module + fragment + CSS for the jobs panel. The query/analyze/report flow is unchanged at the data layer — clicking a catalog table simply seeds the existing SQL editor and `runQuery()` path.

**Tech Stack:** Go 1.26, DuckDB (`github.com/marcboeker/go-duckdb`, CGo), embedded SQLite (`modernc.org/sqlite`, pure Go), Postgres (`github.com/jackc/pgx/v5/stdlib`, `database/sql`-compatible), chi v5 router, Cobra CLI, Go `html/template` + HTMX + vanilla JS (no build step). Module path: `github.com/esignoretti/ds3-sql-server`.

**Spec:** `docs/superpowers/specs/2026-06-07-ds3-sql-bigquery-refactor-design.md` (Phase 4: "Product polish — catalog browser + jobs panel in the Web UI; Postgres metastore option; partition-pruning refinements").

---

## File Structure

New files:

- `internal/metastore/postgres.go` — `PostgresStore` implementing the full `Store` interface (datasets, tables, jobs, cache_index, schedules) against Postgres via `pgx/v5/stdlib`; `OpenPostgres(dsn)` with `CREATE TABLE IF NOT EXISTS` migration mirroring SQLite, JSON columns as `jsonb`.
- `internal/metastore/conformance_test.go` — shared table-driven `testStoreConformance(t, newStore)` exercised against SQLite always and Postgres when `DS3SQL_TEST_POSTGRES_DSN` is set.
- `internal/metastore/postgres_test.go` — thin wrapper that runs the conformance suite against Postgres (skips when the env var is unset) plus a pure-Go unit test of the DSN/placeholder helpers.
- `internal/planner/prune.go` — pure partition-pruning logic: `Partition` type, `ParseWhere`, `Prune`, `ReaderLocations`.
- `internal/planner/prune_test.go` — exhaustive unit tests of predicate → partition selection (no S3, in-memory partitions).
- `internal/web/templates/catalog_tree.html` — server-rendered `{{define "catalog_tree"}}` fragment (datasets list shell + JS bootstrap) used inside `tab_browse`/a new primary panel.
- `internal/web/static/catalog.js` — fetches `/datasets`, `/datasets/{ds}/tables`, `/datasets/{ds}/tables/{t}`, renders the tree, seeds the SQL editor on table click.
- `internal/web/static/jobs.js` — fetches `/jobs` history, renders the jobs panel, re-loads a job's SQL/result on click.
- `internal/api/catalog_fragment_handler.go` — `CatalogFragmentHandler.TreeForProject` returns a server-rendered HTML fragment of the dataset/table tree (the test artifact for the UI; the live tree also uses `catalog.js`).
- `internal/api/catalog_fragment_handler_test.go` — `httptest` tests of the fragment handler.

Modified files:

- `internal/metastore/store.go` — extend `Stats` with `Partitions []Partition` (backward-compatible JSON); add the `Partition` type; (the full Phase 2/3 `Store` method set is assumed already declared here by Phases 2–3 — Phase 4 only implements it for Postgres).
- `internal/config/config.go` — add `Metastore.Driver` and `Metastore.DSN` + `DS3SQL_METASTORE_DRIVER` / `DS3SQL_METASTORE_DSN` env overrides.
- `cmd/ds3sql-server/main.go` — select the store by `cfg.Metastore.Driver`; wire the catalog-fragment handler and a `GET /jobs` history route surfaced to the UI; pass partition-aware resolution into the executor (via `catalog.ResolvePruned`).
- `internal/catalog/service.go` — add `ResolvePruned(ctx, projectID, sql)` that applies `planner.Prune` per referenced partitioned table when building `ReaderSQL`.
- `internal/catalog/service_test.go` — test `ResolvePruned` end-to-end over local Hive-partitioned files.
- `internal/web/templates/layout.html` — rename the primary tab to "Catalog", demote raw browse to a secondary "Buckets" tab; load `catalog.js`/`jobs.js`.
- `internal/web/templates/tab_browse.html` — split into a catalog panel (primary) + the existing bucket browser (secondary).
- `internal/web/templates/tab_query.html` — add the jobs/history panel markup.
- `internal/web/static/tab-manager.js` — register the new `catalog` and `buckets` tab names and default to `catalog`.
- `internal/web/static/style.css` — tree + jobs-panel styling.
- `internal/job/local_executor.go` — call `cat.ResolvePruned` instead of `cat.Resolve` (one-line change, partition pruning on by default; falls back to full scan).
- `go.mod` / `go.sum` — add `github.com/jackc/pgx/v5`.
- `docs/architecture.md`, `docs/configuration.md`, `docs/deployment.md`, `README.md` — document the catalog browser, jobs panel, Postgres option, pruning.

**Conventions to follow (from existing code):**
- API handlers that need credentials use `…ForProject` / `…WithCreds(w, r, projectID, accessKey, secretKey, endpoint)`; `main.go` extracts creds from the session and calls them (mirrors `QueryHandler.QueryWithCreds`, `TableHandler.RegisterForProject`).
- Errors returned to clients are JSON: `{"error":"…"}`.
- Stores live behind the `metastore.Store` interface; `var _ Store = (*T)(nil)` asserts conformance. `ErrNotFound` is the sentinel for missing rows.
- Frontend is `html/template` + HTMX + vanilla JS modules sharing helpers from `tab-manager.js` (`escHtml`, `escJs`, `escAttr`); tab state lives in the global `tabState`; tabs are switched with `switchTab()`/`navigateToTab()` and hash routing.
- Go tests use `t.TempDir()`; Postgres tests gate on `DS3SQL_TEST_POSTGRES_DSN` and `t.Skip()` when unset.

**Assumed-present from Phases 2 & 3 (do NOT re-implement; the SQLite store already has them):** the `Store` interface in `internal/metastore/store.go` already declares `CreateJob/UpdateJob/GetJob/ListJobs`, `PutCacheEntry/LookupCacheEntry/DeleteCacheEntry/ListCacheEntries/DeleteCacheEntriesForTable`, and `CreateSchedule/ListSchedules/GetSchedule/DeleteSchedule/UpdateScheduleRun/GetDueSchedules`, with the types `JobRecord`, `CacheEntry`, `Schedule`. The `internal/api` package already exposes a jobs-history endpoint `GET /jobs` (list). If any signature differs at implementation time, treat `internal/metastore/store.go` as the source of truth and match it exactly.

---

## Task 1: Extend `Stats` with partition info (backward-compatible)

**Files:**
- Modify: `internal/metastore/store.go`
- Test: `internal/metastore/store_test.go` (create)

The pruner needs per-partition info on the table. We add it to `Stats` as an *optional* slice so existing JSON (no `partitions` key) round-trips unchanged.

- [ ] **Step 1: Write the failing test**

Create `internal/metastore/store_test.go`:
```go
package metastore

import (
	"encoding/json"
	"testing"
)

func TestStats_PartitionsBackwardCompatible(t *testing.T) {
	// Old payload with no "partitions" key must unmarshal fine.
	var old Stats
	if err := json.Unmarshal([]byte(`{"row_count":5}`), &old); err != nil {
		t.Fatalf("unmarshal old stats: %v", err)
	}
	if old.RowCount != 5 || old.Partitions != nil {
		t.Fatalf("unexpected old stats: %+v", old)
	}

	// New payload round-trips through marshal/unmarshal.
	s := Stats{
		RowCount: 9,
		Partitions: []Partition{{
			Values:   map[string]string{"dt": "2026-06-07"},
			Location: "s3://b/dt=2026-06-07/",
			RowCount: 9,
			Min:      map[string]string{"id": "1"},
			Max:      map[string]string{"id": "9"},
		}},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Stats
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Partitions) != 1 || got.Partitions[0].Values["dt"] != "2026-06-07" {
		t.Fatalf("partition round-trip failed: %+v", got)
	}
	if got.Partitions[0].Location != "s3://b/dt=2026-06-07/" {
		t.Fatalf("location round-trip failed: %+v", got.Partitions[0])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/metastore/ -run TestStats_PartitionsBackwardCompatible -v`
Expected: FAIL — `undefined: Partition` and `Partitions` field.

- [ ] **Step 3: Add the `Partition` type and extend `Stats`**

In `internal/metastore/store.go`, replace the `Stats` struct:
```go
// Stats holds lightweight table statistics.
type Stats struct {
	RowCount int64 `json:"row_count"`
	// Partitions is the per-partition file/location list used for pruning. It is
	// optional and omitted from JSON when empty, so pre-Phase-4 stats payloads
	// (which have no "partitions" key) round-trip unchanged.
	Partitions []Partition `json:"partitions,omitempty"`
}

// Partition describes one Hive-style partition of a table: the partition-column
// values that select it, the reader location for its files, a row-count estimate,
// and optional per-column min/max bounds for range pruning.
type Partition struct {
	Values   map[string]string `json:"values"`
	Location string            `json:"location"`
	RowCount int64             `json:"row_count"`
	Min      map[string]string `json:"min,omitempty"`
	Max      map[string]string `json:"max,omitempty"`
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/metastore/ -run TestStats_PartitionsBackwardCompatible -v`
Expected: PASS.

- [ ] **Step 5: Run the whole metastore package (SQLite store still round-trips stats)**

Run: `go test ./internal/metastore/`
Expected: PASS (ok) — existing `TestTableCRUD` still passes because `Stats` marshals as before when `Partitions` is nil.

- [ ] **Step 6: Commit**

```bash
git add internal/metastore/store.go internal/metastore/store_test.go
git commit -m "feat(metastore): add Partition type and optional Stats.Partitions"
```

---

## Task 2: Partition pruning — predicate parser (`internal/planner`)

**Files:**
- Create: `internal/planner/prune.go`
- Test: `internal/planner/prune_test.go`

The parser is pure and string-based (no SQL AST dependency): it extracts simple conjunctive `WHERE` predicates over partition columns. **Supported forms** (case-insensitive on keywords; partition columns matched case-insensitively):
- equality: `col = 'v'` or `col = v`
- IN list: `col IN ('a','b')`
- range: `col > 'v'`, `col >= 'v'`, `col < 'v'`, `col <= 'v'`
- predicates combined with `AND` at the top level.

**Unsupported forms** (cause that column's predicate to be ignored → conservative full scan over that dimension): `OR`, `NOT`, functions/expressions on the column (`substr(dt)=…`), `BETWEEN`, parameter placeholders, predicates on non-partition columns, and anything the simple scanner cannot confidently parse. This is correctness-preserving: pruning only ever removes partitions that *cannot* match; ambiguity keeps a partition in.

- [ ] **Step 1: Write the failing test**

Create `internal/planner/prune_test.go`:
```go
package planner

import (
	"reflect"
	"sort"
	"testing"
)

func parts() []Partition {
	return []Partition{
		{Values: map[string]string{"dt": "2026-06-05"}, Location: "s3://b/dt=2026-06-05/"},
		{Values: map[string]string{"dt": "2026-06-06"}, Location: "s3://b/dt=2026-06-06/"},
		{Values: map[string]string{"dt": "2026-06-07"}, Location: "s3://b/dt=2026-06-07/"},
	}
}

func locs(ps []Partition) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Location
	}
	sort.Strings(out)
	return out
}

func TestParseWhere_Equality(t *testing.T) {
	preds := ParseWhere("SELECT * FROM sales.orders WHERE dt = '2026-06-06'", []string{"dt"})
	if len(preds) != 1 {
		t.Fatalf("expected 1 predicate, got %d (%+v)", len(preds), preds)
	}
	p := preds[0]
	if p.Column != "dt" || p.Op != OpEq || len(p.Values) != 1 || p.Values[0] != "2026-06-06" {
		t.Fatalf("unexpected predicate: %+v", p)
	}
}

func TestPrune_Equality(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-06'", []string{"dt"}, parts())
	if want := []string{"s3://b/dt=2026-06-06/"}; !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_In(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt IN ('2026-06-05','2026-06-07')", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-05/", "s3://b/dt=2026-06-07/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_Range(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt >= '2026-06-06'", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-06/", "s3://b/dt=2026-06-07/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_RangeBothSides(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE dt > '2026-06-05' AND dt < '2026-06-07'", []string{"dt"}, parts())
	want := []string{"s3://b/dt=2026-06-06/"}
	if !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestPrune_NoPredicate_ReturnsAll(t *testing.T) {
	got := Prune("SELECT * FROM t", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected all 3 partitions, got %d", len(got))
	}
}

func TestPrune_UnsupportedOr_ReturnsAll(t *testing.T) {
	// OR is unsupported -> conservative full scan.
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-05' OR dt = '2026-06-07'", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected conservative all 3, got %d", len(got))
	}
}

func TestPrune_NonPartitionPredicate_ReturnsAll(t *testing.T) {
	got := Prune("SELECT * FROM t WHERE total > 100", []string{"dt"}, parts())
	if len(got) != 3 {
		t.Fatalf("expected all 3, got %d", len(got))
	}
}

func TestPrune_MultiColumnAnd(t *testing.T) {
	ps := []Partition{
		{Values: map[string]string{"dt": "2026-06-06", "region": "eu"}, Location: "a"},
		{Values: map[string]string{"dt": "2026-06-06", "region": "us"}, Location: "b"},
		{Values: map[string]string{"dt": "2026-06-07", "region": "eu"}, Location: "c"},
	}
	got := Prune("SELECT * FROM t WHERE dt = '2026-06-06' AND region = 'eu'", []string{"dt", "region"}, ps)
	if want := []string{"a"}; !reflect.DeepEqual(locs(got), want) {
		t.Fatalf("got %v want %v", locs(got), want)
	}
}

func TestReaderLocations(t *testing.T) {
	got := ReaderLocations([]Partition{{Location: "s3://b/p1/"}, {Location: "s3://b/p2/"}}, "parquet")
	want := "read_parquet(['s3://b/p1/', 's3://b/p2/'])"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/planner/ -run TestParseWhere_Equality -v`
Expected: FAIL — `undefined: ParseWhere` / `Partition` / `Prune`.

- [ ] **Step 3: Implement the pruner**

Create `internal/planner/prune.go`:
```go
// Package planner implements catalog-side query planning. In Phase 4 it provides
// partition pruning: mapping simple WHERE predicates over a table's partition
// columns to the reduced set of partitions (and thus reader locations) that can
// possibly match. Pruning is correctness-preserving — it only ever removes
// partitions that cannot match; any predicate it cannot confidently parse is
// ignored, falling back to scanning all partitions.
package planner

import (
	"regexp"
	"strings"
)

// Partition mirrors metastore.Partition but is duplicated here to keep the
// planner package free of a metastore import (avoiding an import cycle: catalog
// imports both). The catalog layer converts between the two.
type Partition struct {
	Values   map[string]string
	Location string
	RowCount int64
	Min      map[string]string
	Max      map[string]string
}

// Op is a comparison operator in a parsed predicate.
type Op int

const (
	OpEq Op = iota
	OpIn
	OpGt
	OpGte
	OpLt
	OpLte
)

// Predicate is a single parsed predicate over a partition column.
type Predicate struct {
	Column string
	Op     Op
	Values []string // for OpIn this holds the whole list; otherwise one element
}

var (
	// `col <op> 'value'` or `col <op> value` (value: quoted string or bareword/number).
	cmpRe = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s*(>=|<=|=|>|<)\s*('(?:[^']|'')*'|[a-z0-9_.\-:]+)`)
	// `col IN ( ... )`
	inRe = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+IN\s*\(([^)]*)\)`)
	// quoted item extractor for IN lists / general literals
	quotedRe = regexp.MustCompile(`'((?:[^']|'')*)'`)
)

// whereClause returns the text after the first top-level WHERE, or "" if absent.
func whereClause(sql string) string {
	loc := regexp.MustCompile(`(?i)\bWHERE\b`).FindStringIndex(sql)
	if loc == nil {
		return ""
	}
	clause := sql[loc[1]:]
	// Trim trailing clauses we don't analyze; conservative: keep everything up to
	// the first GROUP/ORDER/LIMIT/HAVING/window/semicolon at top level.
	tail := regexp.MustCompile(`(?i)\b(GROUP\s+BY|ORDER\s+BY|LIMIT|HAVING|WINDOW|QUALIFY)\b`).FindStringIndex(clause)
	if tail != nil {
		clause = clause[:tail[0]]
	}
	if i := strings.Index(clause, ";"); i >= 0 {
		clause = clause[:i]
	}
	return clause
}

func unquote(lit string) string {
	if len(lit) >= 2 && lit[0] == '\'' && lit[len(lit)-1] == '\'' {
		return strings.ReplaceAll(lit[1:len(lit)-1], "''", "'")
	}
	return lit
}

func isPartCol(col string, partCols []string) (string, bool) {
	for _, pc := range partCols {
		if strings.EqualFold(pc, col) {
			return pc, true
		}
	}
	return "", false
}

// ParseWhere extracts supported conjunctive predicates over the given partition
// columns. It returns nil when the WHERE clause contains an unsupported top-level
// construct (OR / NOT), signalling callers to scan all partitions.
func ParseWhere(sql string, partCols []string) []Predicate {
	clause := whereClause(sql)
	if clause == "" {
		return nil
	}
	// Unsupported boolean structure -> conservative full scan.
	if regexp.MustCompile(`(?i)\b(OR|NOT)\b`).MatchString(clause) {
		return nil
	}

	var preds []Predicate

	// IN predicates first (so their `=`-free shape isn't mis-scanned).
	inMatches := inRe.FindAllStringSubmatch(clause, -1)
	for _, m := range inMatches {
		col, ok := isPartCol(m[1], partCols)
		if !ok {
			continue
		}
		var vals []string
		for _, q := range quotedRe.FindAllStringSubmatch(m[2], -1) {
			vals = append(vals, strings.ReplaceAll(q[1], "''", "'"))
		}
		if len(vals) == 0 {
			// bareword list, split on commas
			for _, raw := range strings.Split(m[2], ",") {
				if v := strings.TrimSpace(raw); v != "" {
					vals = append(vals, v)
				}
			}
		}
		if len(vals) > 0 {
			preds = append(preds, Predicate{Column: col, Op: OpIn, Values: vals})
		}
	}
	// Blank out IN regions so cmpRe doesn't double-match inside them.
	clauseForCmp := inRe.ReplaceAllString(clause, " ")

	for _, m := range cmpRe.FindAllStringSubmatch(clauseForCmp, -1) {
		col, ok := isPartCol(m[1], partCols)
		if !ok {
			continue
		}
		var op Op
		switch m[2] {
		case "=":
			op = OpEq
		case ">":
			op = OpGt
		case ">=":
			op = OpGte
		case "<":
			op = OpLt
		case "<=":
			op = OpLte
		}
		preds = append(preds, Predicate{Column: col, Op: op, Values: []string{unquote(m[3])}})
	}
	return preds
}

func matches(p Predicate, partVal string) bool {
	switch p.Op {
	case OpEq:
		return partVal == p.Values[0]
	case OpIn:
		for _, v := range p.Values {
			if partVal == v {
				return true
			}
		}
		return false
	case OpGt:
		return partVal > p.Values[0]
	case OpGte:
		return partVal >= p.Values[0]
	case OpLt:
		return partVal < p.Values[0]
	case OpLte:
		return partVal <= p.Values[0]
	}
	return true
}

// Prune returns the subset of partitions that can possibly satisfy the SQL's
// WHERE predicates over the partition columns. Partitions whose value for a
// constrained column is missing are kept (conservative).
func Prune(sql string, partCols []string, partitions []Partition) []Partition {
	preds := ParseWhere(sql, partCols)
	if len(preds) == 0 {
		return partitions
	}
	var out []Partition
	for _, part := range partitions {
		keep := true
		for _, p := range preds {
			val, ok := part.Values[p.Column]
			if !ok {
				continue // unknown value for this partition -> can't exclude
			}
			if !matches(p, val) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, part)
		}
	}
	return out
}

// ReaderLocations builds a DuckDB reader expression over the given partitions'
// locations for the format, e.g. read_parquet(['s3://…/p1', 's3://…/p2']).
func ReaderLocations(partitions []Partition, format string) string {
	quoted := make([]string, len(partitions))
	for i, p := range partitions {
		quoted[i] = "'" + strings.ReplaceAll(p.Location, "'", "''") + "'"
	}
	list := "[" + strings.Join(quoted, ", ") + "]"
	switch strings.ToLower(format) {
	case "json":
		return "read_json_auto(" + list + ")"
	case "tsv":
		return "read_csv_auto(" + list + ", delim='\t')"
	case "csv":
		return "read_csv_auto(" + list + ")"
	default: // parquet
		return "read_parquet(" + list + ")"
	}
}
```

- [ ] **Step 4: Run the whole planner package**

Run: `go test ./internal/planner/ -v`
Expected: PASS — all ParseWhere/Prune/ReaderLocations tests.

- [ ] **Step 5: Commit**

```bash
git add internal/planner/
git commit -m "feat(planner): pure partition-pruning predicate analysis"
```

---

## Task 3: Catalog `ResolvePruned` — wire pruning into view bindings

**Files:**
- Modify: `internal/catalog/service.go`
- Modify: `internal/job/local_executor.go`
- Test: `internal/catalog/service_test.go`

`ResolvePruned` behaves exactly like `Resolve`, but for a referenced table that has both `PartitionColumns` and `Stats.Partitions`, it builds the `ReaderSQL` from the pruned partition locations rather than the table's base location glob. Unpartitioned tables (or tables with no stored partition list) keep the existing `readerSQL(location, format)` path unchanged.

- [ ] **Step 1: Write the failing test (local Hive-partitioned files)**

Append to `internal/catalog/service_test.go`:
```go
func TestResolvePruned_SelectsMatchingPartitions(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	// Build a Hive-style local layout: <root>/dt=2026-06-06/data.csv etc.
	root := t.TempDir()
	mk := func(dt, body string) string {
		dir := filepath.Join(root, "dt="+dt)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "data.csv")
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p6 := mk("2026-06-06", "id,total\n1,10\n")
	p7 := mk("2026-06-07", "id,total\n2,20\n")

	if err := svc.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	// Register with one base partition glob, then store the partition list directly.
	if _, err := svc.RegisterTable(ctx, RegisterTableInput{
		ProjectID: "p1", Dataset: "sales", Name: "orders",
		Location: p6, Format: "csv", PartitionColumns: []string{"dt"},
	}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Inject the partition list (Phase 3 normally populates this on load/CTAS).
	tbl, err := svc.GetTable(ctx, "p1", "sales", "orders")
	if err != nil {
		t.Fatal(err)
	}
	tbl.Stats.Partitions = []metastore.Partition{
		{Values: map[string]string{"dt": "2026-06-06"}, Location: p6},
		{Values: map[string]string{"dt": "2026-06-07"}, Location: p7},
	}
	if err := svc.SaveTablePartitions(ctx, tbl); err != nil {
		t.Fatal(err)
	}

	// Pruned: only the 2026-06-07 partition.
	bindings, err := svc.ResolvePruned(ctx, "p1",
		"SELECT * FROM sales.orders WHERE dt = '2026-06-07'")
	if err != nil {
		t.Fatalf("ResolvePruned: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !strings.Contains(bindings[0].ReaderSQL, p7) || strings.Contains(bindings[0].ReaderSQL, p6) {
		t.Fatalf("expected reader over only p7, got %q", bindings[0].ReaderSQL)
	}

	// No predicate: both partitions present.
	all, err := svc.ResolvePruned(ctx, "p1", "SELECT * FROM sales.orders")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all[0].ReaderSQL, p6) || !strings.Contains(all[0].ReaderSQL, p7) {
		t.Fatalf("expected reader over both partitions, got %q", all[0].ReaderSQL)
	}
}
```
Ensure the test file imports `"strings"` and `"github.com/esignoretti/ds3-sql-server/internal/metastore"` (add if missing).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestResolvePruned_SelectsMatchingPartitions -v`
Expected: FAIL — `svc.SaveTablePartitions` / `svc.ResolvePruned` undefined.

- [ ] **Step 3: Implement `SaveTablePartitions` and `ResolvePruned`**

In `internal/catalog/service.go`, add `"github.com/esignoretti/ds3-sql-server/internal/planner"` to imports, then append:
```go
// SaveTablePartitions persists an updated partition list (and row count) for a
// table by recreating its catalog row. Phase 3 load/CTAS uses this after writing
// data; tests use it to inject partition layouts.
func (s *Service) SaveTablePartitions(ctx context.Context, t *metastore.Table) error {
	if err := s.store.DeleteTable(ctx, t.ProjectID, t.Dataset, t.Name); err != nil {
		return err
	}
	// Preserve the data version across the rewrite.
	t.CreatedAt = t.CreatedAt // no-op; CreateTable refreshes UpdatedAt
	return s.store.CreateTable(ctx, t)
}

// toPlannerPartitions converts stored metastore partitions to planner partitions.
func toPlannerPartitions(in []metastore.Partition) []planner.Partition {
	out := make([]planner.Partition, len(in))
	for i, p := range in {
		out[i] = planner.Partition{
			Values:   p.Values,
			Location: p.Location,
			RowCount: p.RowCount,
			Min:      p.Min,
			Max:      p.Max,
		}
	}
	return out
}

// ResolvePruned is like Resolve, but for partitioned tables with a stored
// partition list it builds the reader expression from only the partitions that
// can satisfy the query's WHERE predicates (partition pruning). Tables without a
// partition list fall back to the base-location reader.
func (s *Service) ResolvePruned(ctx context.Context, projectID, sql string) ([]query.ViewBinding, error) {
	datasets, err := s.store.ListDatasets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var bindings []query.ViewBinding
	for _, ds := range datasets {
		tables, err := s.store.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if !referencesTable(sql, t.Dataset, t.Name) {
				continue
			}
			reader := readerSQL(t.Location, t.Format)
			if len(t.PartitionColumns) > 0 && len(t.Stats.Partitions) > 0 {
				kept := planner.Prune(sql, t.PartitionColumns, toPlannerPartitions(t.Stats.Partitions))
				if len(kept) > 0 {
					reader = planner.ReaderLocations(kept, t.Format)
				}
			}
			bindings = append(bindings, query.ViewBinding{
				Schema:    t.Dataset,
				Name:      t.Name,
				ReaderSQL: reader,
			})
		}
	}
	return bindings, nil
}
```

- [ ] **Step 4: Switch the executor to pruned resolution**

In `internal/job/local_executor.go`, change the resolve call:
```go
	bindings, err := l.cat.ResolvePruned(ctx, req.ProjectID, req.SQL)
```
(was `l.cat.Resolve(...)`). The signature is identical, so the rest is unchanged.

- [ ] **Step 5: Run catalog + job tests**

Run: `go test ./internal/catalog/ ./internal/job/ -v`
Expected: PASS — including the existing `TestLocalExecutor_EndToEnd` (unpartitioned table → base reader unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/service.go internal/catalog/service_test.go internal/job/local_executor.go
git commit -m "feat(catalog): partition-pruned view bindings via planner"
```

---

## Task 4: Add the Postgres driver dependency and store skeleton

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/metastore/postgres.go`

- [ ] **Step 1: Add the pgx stdlib driver**

Run:
```bash
cd "/Users/esignoretti/Documents/OpenCode/DS3-SQL Server"
go get github.com/jackc/pgx/v5@latest
```
Expected: `go.mod` gains a `github.com/jackc/pgx/v5` require line; `go.sum` updated. (The `database/sql` driver lives at `github.com/jackc/pgx/v5/stdlib`.)

- [ ] **Step 2: Implement the skeleton with schema migration**

Create `internal/metastore/postgres.go`:
```go
package metastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements Store against PostgreSQL. It mirrors the SQLite schema
// using Postgres types (TIMESTAMPTZ, BIGINT, JSONB) and supports an HA coordinator
// set sharing one database.
type PostgresStore struct {
	db *sql.DB
}

// OpenPostgres opens a connection pool to the given DSN and runs migrations.
func OpenPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS datasets (
			project_id TEXT NOT NULL,
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (project_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS tables (
			project_id        TEXT NOT NULL,
			dataset           TEXT NOT NULL,
			name              TEXT NOT NULL,
			kind              TEXT NOT NULL,
			location          TEXT NOT NULL,
			format            TEXT NOT NULL,
			storage_class     TEXT NOT NULL,
			partition_columns JSONB NOT NULL,
			schema_json       JSONB NOT NULL,
			stats_json        JSONB NOT NULL,
			data_version      BIGINT NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL,
			updated_at        TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (project_id, dataset, name)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id              TEXT PRIMARY KEY,
			project_id      TEXT NOT NULL,
			type            TEXT NOT NULL,
			sql             TEXT NOT NULL,
			status          TEXT NOT NULL,
			error           TEXT NOT NULL DEFAULT '',
			row_count       BIGINT NOT NULL DEFAULT 0,
			bytes_scanned   BIGINT NOT NULL DEFAULT 0,
			result_location TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMPTZ NOT NULL,
			started_at      TIMESTAMPTZ,
			finished_at     TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS jobs_project_created ON jobs (project_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS cache_index (
			key            TEXT PRIMARY KEY,
			project_id     TEXT NOT NULL,
			sql_norm       TEXT NOT NULL,
			table_versions TEXT NOT NULL,
			location       TEXT NOT NULL,
			size_bytes     BIGINT NOT NULL DEFAULT 0,
			created_at     TIMESTAMPTZ NOT NULL,
			last_access_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			id          TEXT PRIMARY KEY,
			project_id  TEXT NOT NULL,
			cron        TEXT NOT NULL,
			sql         TEXT NOT NULL,
			into_table  TEXT NOT NULL DEFAULT '',
			owner       TEXT NOT NULL DEFAULT '',
			next_run_at TIMESTAMPTZ,
			last_run_at TIMESTAMPTZ,
			running     BOOLEAN NOT NULL DEFAULT FALSE,
			created_at  TIMESTAMPTZ NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

// nullTime converts a possibly-zero time to a value suitable for a nullable column.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func timeFromNull(nt sql.NullTime) time.Time {
	if nt.Valid {
		return nt.Time.UTC()
	}
	return time.Time{}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ Store = (*PostgresStore)(nil)
```

> Note: every `Store` method must be implemented for `var _ Store = (*PostgresStore)(nil)` to compile. Tasks 5–7 add them. To make the package compile *after this task* while the method bodies are pending, add temporary stubs at the end of `postgres.go` (each `panic("unimplemented")`), which Tasks 5–7 replace. Add the stubs now:
```go
func (s *PostgresStore) CreateDataset(ctx context.Context, ds *Dataset) error { panic("unimplemented") }
func (s *PostgresStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) { panic("unimplemented") }
func (s *PostgresStore) ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error) { panic("unimplemented") }
func (s *PostgresStore) CreateTable(ctx context.Context, t *Table) error { panic("unimplemented") }
func (s *PostgresStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) { panic("unimplemented") }
func (s *PostgresStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error { panic("unimplemented") }
func (s *PostgresStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) { panic("unimplemented") }
func (s *PostgresStore) CreateJob(ctx context.Context, j *JobRecord) error { panic("unimplemented") }
func (s *PostgresStore) UpdateJob(ctx context.Context, j *JobRecord) error { panic("unimplemented") }
func (s *PostgresStore) GetJob(ctx context.Context, id string) (*JobRecord, error) { panic("unimplemented") }
func (s *PostgresStore) ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error) { panic("unimplemented") }
func (s *PostgresStore) PutCacheEntry(ctx context.Context, e *CacheEntry) error { panic("unimplemented") }
func (s *PostgresStore) LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteCacheEntry(ctx context.Context, key string) error { panic("unimplemented") }
func (s *PostgresStore) ListCacheEntries(ctx context.Context) ([]*CacheEntry, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error { panic("unimplemented") }
func (s *PostgresStore) CreateSchedule(ctx context.Context, sc *Schedule) error { panic("unimplemented") }
func (s *PostgresStore) ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error) { panic("unimplemented") }
func (s *PostgresStore) GetSchedule(ctx context.Context, id string) (*Schedule, error) { panic("unimplemented") }
func (s *PostgresStore) DeleteSchedule(ctx context.Context, id string) error { panic("unimplemented") }
func (s *PostgresStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error { panic("unimplemented") }
func (s *PostgresStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error) { panic("unimplemented") }
```

- [ ] **Step 3: Build the package**

Run: `go build ./internal/metastore/`
Expected: builds with no error (stubs satisfy the interface; `var _ Store` compiles).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/metastore/postgres.go
git commit -m "feat(metastore): add pgx dependency and Postgres store skeleton"
```

---

## Task 5: Shared store conformance suite (SQLite always; Postgres opt-in)

**Files:**
- Create: `internal/metastore/conformance_test.go`
- Create: `internal/metastore/postgres_test.go`

The conformance suite is the test artifact proving SQLite and Postgres behave identically. It is table-driven over the Phase-1 surface here; Tasks 6–7 extend it with jobs/cache/schedules as those Postgres methods land. SQLite must pass the suite now (it already implements those methods from Phases 1–3); Postgres will pass incrementally.

- [ ] **Step 1: Write the conformance suite (Phase 1 surface) and the SQLite runner**

Create `internal/metastore/conformance_test.go`:
```go
package metastore

import (
	"context"
	"path/filepath"
	"testing"
)

// storeFactory creates a fresh, isolated Store for one subtest.
type storeFactory func(t *testing.T) Store

// testStoreConformance runs the full behavioural contract against any Store impl.
func testStoreConformance(t *testing.T, newStore storeFactory) {
	t.Run("DatasetCRUD", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err != nil {
			t.Fatalf("CreateDataset: %v", err)
		}
		if err := s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"}); err == nil {
			t.Fatal("expected duplicate dataset error")
		}
		got, err := s.GetDataset(ctx, "p1", "sales")
		if err != nil || got.Name != "sales" {
			t.Fatalf("GetDataset: %v %+v", err, got)
		}
		if _, err := s.GetDataset(ctx, "p1", "nope"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		list, err := s.ListDatasets(ctx, "p1")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListDatasets: %v len=%d", err, len(list))
		}
	})

	t.Run("TableCRUDAndVersion", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		_ = s.CreateDataset(ctx, &Dataset{ProjectID: "p1", Name: "sales"})
		tbl := &Table{
			ProjectID: "p1", Dataset: "sales", Name: "orders", Kind: "external",
			Location: "s3://b/orders/*.parquet", Format: "parquet", StorageClass: "hdd",
			PartitionColumns: []string{"dt"},
			Schema:           []Column{{Name: "id", Type: "BIGINT", Nullable: false}},
			Stats: Stats{RowCount: 3, Partitions: []Partition{
				{Values: map[string]string{"dt": "2026-06-07"}, Location: "s3://b/orders/dt=2026-06-07/", RowCount: 3},
			}},
		}
		if err := s.CreateTable(ctx, tbl); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		got, err := s.GetTable(ctx, "p1", "sales", "orders")
		if err != nil {
			t.Fatalf("GetTable: %v", err)
		}
		if len(got.Schema) != 1 || got.Schema[0].Name != "id" {
			t.Fatalf("schema round-trip: %+v", got.Schema)
		}
		if len(got.PartitionColumns) != 1 || got.PartitionColumns[0] != "dt" {
			t.Fatalf("partition cols round-trip: %+v", got.PartitionColumns)
		}
		if len(got.Stats.Partitions) != 1 || got.Stats.Partitions[0].Values["dt"] != "2026-06-07" {
			t.Fatalf("partition stats round-trip: %+v", got.Stats.Partitions)
		}
		v, err := s.BumpDataVersion(ctx, "p1", "sales", "orders")
		if err != nil || v != 2 {
			t.Fatalf("BumpDataVersion: v=%d err=%v", v, err)
		}
		list, err := s.ListTables(ctx, "p1", "sales")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListTables: %v len=%d", err, len(list))
		}
		if err := s.DeleteTable(ctx, "p1", "sales", "orders"); err != nil {
			t.Fatalf("DeleteTable: %v", err)
		}
		if _, err := s.GetTable(ctx, "p1", "sales", "orders"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})
}

func TestSQLiteConformance(t *testing.T) {
	testStoreConformance(t, func(t *testing.T) Store {
		s, err := OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
```

- [ ] **Step 2: Write the Postgres runner + a pure-Go helper unit test**

Create `internal/metastore/postgres_test.go`:
```go
package metastore

import (
	"os"
	"testing"
	"time"
)

// newPostgresStore returns an isolated Postgres-backed Store, or skips when no
// test DSN is configured. Each invocation drops and recreates the public tables
// so subtests do not interfere.
func newPostgresStore(t *testing.T) Store {
	dsn := os.Getenv("DS3SQL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DS3SQL_TEST_POSTGRES_DSN not set; skipping Postgres conformance")
	}
	s, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	// Clean slate: truncate all tables this store owns.
	for _, tbl := range []string{"datasets", "tables", "jobs", "cache_index", "schedules"} {
		if _, err := s.db.Exec("TRUNCATE TABLE " + tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPostgresConformance(t *testing.T) {
	testStoreConformance(t, func(t *testing.T) Store {
		return newPostgresStore(t)
	})
}

// TestNullTime exercises a pure helper with no live DB.
func TestNullTime(t *testing.T) {
	if nullTime(time.Time{}) != nil {
		t.Fatal("zero time should map to nil")
	}
	now := time.Now()
	if v, ok := nullTime(now).(time.Time); !ok || v.IsZero() {
		t.Fatalf("non-zero time should map to a time.Time, got %T", nullTime(now))
	}
}
```

- [ ] **Step 3: Run SQLite conformance (Postgres skips) + the helper test**

Run: `go test ./internal/metastore/ -run 'TestSQLiteConformance|TestPostgresConformance|TestNullTime' -v`
Expected: `TestSQLiteConformance` PASS; `TestPostgresConformance` SKIP (no DSN); `TestNullTime` PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/metastore/conformance_test.go internal/metastore/postgres_test.go
git commit -m "test(metastore): shared store conformance suite (SQLite + opt-in Postgres)"
```

---

## Task 6: Implement Postgres datasets + tables

**Files:**
- Modify: `internal/metastore/postgres.go`

Postgres uses `$1,$2,…` placeholders (not `?`). JSON columns are `jsonb`; we marshal Go slices/structs to JSON text and let the driver store them.

- [ ] **Step 1: Implement dataset + table methods (replace the matching stubs)**

In `internal/metastore/postgres.go`, replace the eight dataset/table stubs:
```go
func (s *PostgresStore) CreateDataset(ctx context.Context, ds *Dataset) error {
	if ds.CreatedAt.IsZero() {
		ds.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO datasets (project_id, name, created_at) VALUES ($1, $2, $3)`,
		ds.ProjectID, ds.Name, ds.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create dataset: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetDataset(ctx context.Context, projectID, name string) (*Dataset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id, name, created_at FROM datasets WHERE project_id = $1 AND name = $2`,
		projectID, name)
	var d Dataset
	switch err := row.Scan(&d.ProjectID, &d.Name, &d.CreatedAt); err {
	case nil:
		d.CreatedAt = d.CreatedAt.UTC()
		return &d, nil
	case sql.ErrNoRows:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("get dataset: %w", err)
	}
}

func (s *PostgresStore) ListDatasets(ctx context.Context, projectID string) ([]*Dataset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, name, created_at FROM datasets WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}
	defer rows.Close()
	out := []*Dataset{}
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ProjectID, &d.Name, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.CreatedAt = d.CreatedAt.UTC()
		out = append(out, &d)
	}
	return out, rows.Err()
}

const pgTableCols = `project_id, dataset, name, kind, location, format, storage_class,
	partition_columns, schema_json, stats_json, data_version, created_at, updated_at`

func (s *PostgresStore) CreateTable(ctx context.Context, t *Table) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.DataVersion == 0 {
		t.DataVersion = 1
	}
	parts := t.PartitionColumns
	if parts == nil {
		parts = []string{}
	}
	schema := t.Schema
	if schema == nil {
		schema = []Column{}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tables (`+pgTableCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		t.ProjectID, t.Dataset, t.Name, t.Kind, t.Location, t.Format, t.StorageClass,
		mustJSON(parts), mustJSON(schema), mustJSON(t.Stats), t.DataVersion,
		t.CreatedAt.UTC(), t.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

func scanPgTable(row interface{ Scan(...any) error }) (*Table, error) {
	var t Table
	var parts, schema, stats string
	err := row.Scan(&t.ProjectID, &t.Dataset, &t.Name, &t.Kind, &t.Location, &t.Format,
		&t.StorageClass, &parts, &schema, &stats, &t.DataVersion, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(parts), &t.PartitionColumns)
	_ = json.Unmarshal([]byte(schema), &t.Schema)
	_ = json.Unmarshal([]byte(stats), &t.Stats)
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	return &t, nil
}

func (s *PostgresStore) GetTable(ctx context.Context, projectID, dataset, name string) (*Table, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+pgTableCols+` FROM tables WHERE project_id = $1 AND dataset = $2 AND name = $3`,
		projectID, dataset, name)
	t, err := scanPgTable(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get table: %w", err)
	}
	return t, nil
}

func (s *PostgresStore) ListTables(ctx context.Context, projectID, dataset string) ([]*Table, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgTableCols+` FROM tables WHERE project_id = $1 AND dataset = $2 ORDER BY name`,
		projectID, dataset)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	out := []*Table{}
	for rows.Next() {
		t, err := scanPgTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteTable(ctx context.Context, projectID, dataset, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tables WHERE project_id = $1 AND dataset = $2 AND name = $3`,
		projectID, dataset, name)
	if err != nil {
		return fmt.Errorf("delete table: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) BumpDataVersion(ctx context.Context, projectID, dataset, name string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE tables SET data_version = data_version + 1, updated_at = $1
		 WHERE project_id = $2 AND dataset = $3 AND name = $4
		 RETURNING data_version`,
		time.Now().UTC(), projectID, dataset, name).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("bump data version: %w", err)
	}
	return v, nil
}
```

- [ ] **Step 2: Build the package**

Run: `go build ./internal/metastore/`
Expected: builds (remaining jobs/cache/schedule stubs still satisfy the interface).

- [ ] **Step 3: Run SQLite conformance (unchanged) — sanity**

Run: `go test ./internal/metastore/ -run TestSQLiteConformance -v`
Expected: PASS. (Postgres conformance still skips unless a DSN is set; when set, `DatasetCRUD` and `TableCRUDAndVersion` now pass against Postgres too.)

- [ ] **Step 4: Commit**

```bash
git add internal/metastore/postgres.go
git commit -m "feat(metastore): Postgres datasets and tables"
```

---

## Task 7: Implement Postgres jobs, cache index, and schedules

**Files:**
- Modify: `internal/metastore/postgres.go`
- Modify: `internal/metastore/conformance_test.go` (extend the suite)

> If the exact field set of `JobRecord`/`CacheEntry`/`Schedule` differs from the spec contract at implementation time, match `internal/metastore/store.go` exactly. The contract assumed here: `JobRecord{ID,ProjectID,Type,SQL,Status,Error,RowCount,BytesScanned,ResultLocation,CreatedAt,StartedAt,FinishedAt}`; `CacheEntry{Key,ProjectID,SQLNorm,TableVersions,Location,SizeBytes,CreatedAt,LastAccessAt}`; `Schedule{ID,ProjectID,Cron,SQL,IntoTable,Owner,NextRunAt,LastRunAt,Running,CreatedAt}`.

- [ ] **Step 1: Implement the remaining methods (replace the matching stubs)**

In `internal/metastore/postgres.go`, replace the jobs/cache/schedule stubs:
```go
func (s *PostgresStore) CreateJob(ctx context.Context, j *JobRecord) error {
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, project_id, type, sql, status, error, row_count,
			bytes_scanned, result_location, created_at, started_at, finished_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		j.ID, j.ProjectID, j.Type, j.SQL, j.Status, j.Error, j.RowCount,
		j.BytesScanned, j.ResultLocation, j.CreatedAt.UTC(), nullTime(j.StartedAt), nullTime(j.FinishedAt))
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateJob(ctx context.Context, j *JobRecord) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status=$1, error=$2, row_count=$3, bytes_scanned=$4,
			result_location=$5, started_at=$6, finished_at=$7 WHERE id=$8`,
		j.Status, j.Error, j.RowCount, j.BytesScanned, j.ResultLocation,
		nullTime(j.StartedAt), nullTime(j.FinishedAt), j.ID)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPgJob(row interface{ Scan(...any) error }) (*JobRecord, error) {
	var j JobRecord
	var started, finished sql.NullTime
	err := row.Scan(&j.ID, &j.ProjectID, &j.Type, &j.SQL, &j.Status, &j.Error,
		&j.RowCount, &j.BytesScanned, &j.ResultLocation, &j.CreatedAt, &started, &finished)
	if err != nil {
		return nil, err
	}
	j.CreatedAt = j.CreatedAt.UTC()
	j.StartedAt = timeFromNull(started)
	j.FinishedAt = timeFromNull(finished)
	return &j, nil
}

const pgJobCols = `id, project_id, type, sql, status, error, row_count,
	bytes_scanned, result_location, created_at, started_at, finished_at`

func (s *PostgresStore) GetJob(ctx context.Context, id string) (*JobRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgJobCols+` FROM jobs WHERE id = $1`, id)
	j, err := scanPgJob(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return j, nil
}

func (s *PostgresStore) ListJobs(ctx context.Context, projectID string, limit int) ([]*JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgJobCols+` FROM jobs WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2`,
		projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	out := []*JobRecord{}
	for rows.Next() {
		j, err := scanPgJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *PostgresStore) PutCacheEntry(ctx context.Context, e *CacheEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.LastAccessAt.IsZero() {
		e.LastAccessAt = e.CreatedAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cache_index (key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (key) DO UPDATE SET
			project_id=EXCLUDED.project_id, sql_norm=EXCLUDED.sql_norm,
			table_versions=EXCLUDED.table_versions, location=EXCLUDED.location,
			size_bytes=EXCLUDED.size_bytes, last_access_at=EXCLUDED.last_access_at`,
		e.Key, e.ProjectID, e.SQLNorm, e.TableVersions, e.Location, e.SizeBytes,
		e.CreatedAt.UTC(), e.LastAccessAt.UTC())
	if err != nil {
		return fmt.Errorf("put cache entry: %w", err)
	}
	return nil
}

func scanPgCache(row interface{ Scan(...any) error }) (*CacheEntry, error) {
	var e CacheEntry
	err := row.Scan(&e.Key, &e.ProjectID, &e.SQLNorm, &e.TableVersions, &e.Location,
		&e.SizeBytes, &e.CreatedAt, &e.LastAccessAt)
	if err != nil {
		return nil, err
	}
	e.CreatedAt = e.CreatedAt.UTC()
	e.LastAccessAt = e.LastAccessAt.UTC()
	return &e, nil
}

const pgCacheCols = `key, project_id, sql_norm, table_versions, location, size_bytes, created_at, last_access_at`

func (s *PostgresStore) LookupCacheEntry(ctx context.Context, key string) (*CacheEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgCacheCols+` FROM cache_index WHERE key = $1`, key)
	e, err := scanPgCache(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup cache entry: %w", err)
	}
	return e, nil
}

func (s *PostgresStore) DeleteCacheEntry(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_index WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("delete cache entry: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListCacheEntries(ctx context.Context) ([]*CacheEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pgCacheCols+` FROM cache_index ORDER BY last_access_at`)
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	defer rows.Close()
	out := []*CacheEntry{}
	for rows.Next() {
		e, err := scanPgCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteCacheEntriesForTable(ctx context.Context, projectID, dataset, table string) error {
	// table_versions encodes referenced tables; match by qualified name substring.
	pattern := "%" + dataset + "." + table + "%"
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM cache_index WHERE project_id = $1 AND table_versions LIKE $2`,
		projectID, pattern)
	if err != nil {
		return fmt.Errorf("delete cache entries for table: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateSchedule(ctx context.Context, sc *Schedule) error {
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedules (id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		sc.ID, sc.ProjectID, sc.Cron, sc.SQL, sc.IntoTable, sc.Owner,
		nullTime(sc.NextRunAt), nullTime(sc.LastRunAt), sc.Running, sc.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}
	return nil
}

func scanPgSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	var sc Schedule
	var next, last sql.NullTime
	err := row.Scan(&sc.ID, &sc.ProjectID, &sc.Cron, &sc.SQL, &sc.IntoTable, &sc.Owner,
		&next, &last, &sc.Running, &sc.CreatedAt)
	if err != nil {
		return nil, err
	}
	sc.NextRunAt = timeFromNull(next)
	sc.LastRunAt = timeFromNull(last)
	sc.CreatedAt = sc.CreatedAt.UTC()
	return &sc, nil
}

const pgScheduleCols = `id, project_id, cron, sql, into_table, owner, next_run_at, last_run_at, running, created_at`

func (s *PostgresStore) ListSchedules(ctx context.Context, projectID string) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgScheduleCols+` FROM schedules WHERE project_id = $1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sc, err := scanPgSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+pgScheduleCols+` FROM schedules WHERE id = $1`, id)
	sc, err := scanPgSchedule(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	return sc, nil
}

func (s *PostgresStore) DeleteSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) UpdateScheduleRun(ctx context.Context, id string, lastRun time.Time, running bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at = $1, running = $2 WHERE id = $3`,
		nullTime(lastRun), running, id)
	if err != nil {
		return fmt.Errorf("update schedule run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetDueSchedules(ctx context.Context, now time.Time) ([]*Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pgScheduleCols+` FROM schedules
		 WHERE running = FALSE AND next_run_at IS NOT NULL AND next_run_at <= $1`,
		now.UTC())
	if err != nil {
		return nil, fmt.Errorf("get due schedules: %w", err)
	}
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		sc, err := scanPgSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Extend the conformance suite with jobs, cache, schedules**

Append the new subtests inside `testStoreConformance` in `internal/metastore/conformance_test.go` (add `"time"` to the file imports):
```go
	t.Run("JobLifecycle", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		j := &JobRecord{ID: "j1", ProjectID: "p1", Type: "query", SQL: "SELECT 1", Status: "running"}
		if err := s.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		j.Status = "done"
		j.RowCount = 5
		j.FinishedAt = time.Now()
		if err := s.UpdateJob(ctx, j); err != nil {
			t.Fatalf("UpdateJob: %v", err)
		}
		got, err := s.GetJob(ctx, "j1")
		if err != nil || got.Status != "done" || got.RowCount != 5 {
			t.Fatalf("GetJob: %v %+v", err, got)
		}
		list, err := s.ListJobs(ctx, "p1", 10)
		if err != nil || len(list) != 1 {
			t.Fatalf("ListJobs: %v len=%d", err, len(list))
		}
		if _, err := s.GetJob(ctx, "missing"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("CacheIndex", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		e := &CacheEntry{Key: "k1", ProjectID: "p1", SQLNorm: "select 1",
			TableVersions: "sales.orders@2", Location: "s3://fast/k1", SizeBytes: 100}
		if err := s.PutCacheEntry(ctx, e); err != nil {
			t.Fatalf("PutCacheEntry: %v", err)
		}
		got, err := s.LookupCacheEntry(ctx, "k1")
		if err != nil || got.Location != "s3://fast/k1" {
			t.Fatalf("LookupCacheEntry: %v %+v", err, got)
		}
		all, err := s.ListCacheEntries(ctx)
		if err != nil || len(all) != 1 {
			t.Fatalf("ListCacheEntries: %v len=%d", err, len(all))
		}
		if err := s.DeleteCacheEntriesForTable(ctx, "p1", "sales", "orders"); err != nil {
			t.Fatalf("DeleteCacheEntriesForTable: %v", err)
		}
		if _, err := s.LookupCacheEntry(ctx, "k1"); err != ErrNotFound {
			t.Fatalf("expected entry deleted by table invalidation, got %v", err)
		}
	})

	t.Run("Schedules", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		sc := &Schedule{ID: "s1", ProjectID: "p1", Cron: "0 * * * *",
			SQL: "SELECT 1", NextRunAt: time.Now().Add(-time.Minute)}
		if err := s.CreateSchedule(ctx, sc); err != nil {
			t.Fatalf("CreateSchedule: %v", err)
		}
		due, err := s.GetDueSchedules(ctx, time.Now())
		if err != nil || len(due) != 1 {
			t.Fatalf("GetDueSchedules: %v len=%d", err, len(due))
		}
		if err := s.UpdateScheduleRun(ctx, "s1", time.Now(), true); err != nil {
			t.Fatalf("UpdateScheduleRun: %v", err)
		}
		// Running schedules are no longer due.
		due2, _ := s.GetDueSchedules(ctx, time.Now())
		if len(due2) != 0 {
			t.Fatalf("expected 0 due after marking running, got %d", len(due2))
		}
		got, err := s.GetSchedule(ctx, "s1")
		if err != nil || !got.Running {
			t.Fatalf("GetSchedule: %v %+v", err, got)
		}
		list, err := s.ListSchedules(ctx, "p1")
		if err != nil || len(list) != 1 {
			t.Fatalf("ListSchedules: %v len=%d", err, len(list))
		}
		if err := s.DeleteSchedule(ctx, "s1"); err != nil {
			t.Fatalf("DeleteSchedule: %v", err)
		}
		if _, err := s.GetSchedule(ctx, "s1"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound after delete, got %v", err)
		}
	})
```

- [ ] **Step 3: Run the full conformance suite against SQLite**

Run: `go test ./internal/metastore/ -run TestSQLiteConformance -v`
Expected: PASS for all subtests (Dataset, Table, Job, Cache, Schedules) — the SQLite store implements all of these from Phases 1–3.

- [ ] **Step 4 (optional, requires a DB): Run against Postgres**

Run:
```bash
DS3SQL_TEST_POSTGRES_DSN='postgres://ds3:ds3@localhost:5432/ds3sql_test?sslmode=disable' \
  go test ./internal/metastore/ -run TestPostgresConformance -v
```
Expected: PASS for all subtests. (Without the env var the test SKIPs — that is the default CI behaviour.)

- [ ] **Step 5: Commit**

```bash
git add internal/metastore/postgres.go internal/metastore/conformance_test.go
git commit -m "feat(metastore): Postgres jobs/cache/schedules + full conformance suite"
```

---

## Task 8: Config — metastore driver + DSN; server store selection

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `cmd/ds3sql-server/main.go`

- [ ] **Step 1: Write the failing config test**

Append to `internal/config/config_test.go` (create the file if absent, with `package config` and imports `"testing"`):
```go
func TestDefault_MetastoreDriver(t *testing.T) {
	c := Default()
	if c.Metastore.Driver != "sqlite" {
		t.Fatalf("expected default driver 'sqlite', got %q", c.Metastore.Driver)
	}
}

func TestLoad_MetastoreDriverAndDSNEnv(t *testing.T) {
	t.Setenv("DS3SQL_METASTORE_DRIVER", "postgres")
	t.Setenv("DS3SQL_METASTORE_DSN", "postgres://u:p@h/db")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Metastore.Driver != "postgres" {
		t.Fatalf("driver env override not applied: %q", c.Metastore.Driver)
	}
	if c.Metastore.DSN != "postgres://u:p@h/db" {
		t.Fatalf("dsn env override not applied: %q", c.Metastore.DSN)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/config/ -run 'TestDefault_MetastoreDriver|TestLoad_MetastoreDriverAndDSNEnv' -v`
Expected: FAIL — `Driver`/`DSN` undefined.

- [ ] **Step 3: Implement config additions**

In `internal/config/config.go`, replace `MetastoreConfig`:
```go
// MetastoreConfig holds settings for the metadata store. Driver selects the
// backend: "sqlite" (embedded, default) or "postgres" (opt-in, HA-capable).
type MetastoreConfig struct {
	Driver string `yaml:"driver"`
	Path   string `yaml:"path"` // used when driver == "sqlite"
	DSN    string `yaml:"dsn"`  // used when driver == "postgres"
}
```
In `Default()`, set the driver in the `Metastore` block:
```go
		Metastore: MetastoreConfig{
			Driver: "sqlite",
			Path:   defaultMetastorePath(),
		},
```
In `Load`, add env overrides next to the existing metastore one:
```go
	if v := os.Getenv("DS3SQL_METASTORE_DRIVER"); v != "" {
		cfg.Metastore.Driver = v
	}
	if v := os.Getenv("DS3SQL_METASTORE_DSN"); v != "" {
		cfg.Metastore.DSN = v
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS (including the Phase 1 metastore path tests).

- [ ] **Step 5: Select the store by driver in `main.go`**

In `cmd/ds3sql-server/main.go`, replace the metastore-open block:
```go
	// Metastore, catalog service, and job manager
	metaStore, err := metastore.OpenSQLite(cfg.Metastore.Path)
	if err != nil {
		log.Fatalf("failed to init metastore: %v", err)
	}
	defer metaStore.Close()
```
with:
```go
	// Metastore: embedded SQLite (default) or external Postgres (opt-in).
	var metaStore metastore.Store
	switch cfg.Metastore.Driver {
	case "", "sqlite":
		s, err := metastore.OpenSQLite(cfg.Metastore.Path)
		if err != nil {
			log.Fatalf("failed to init sqlite metastore: %v", err)
		}
		metaStore = s
	case "postgres":
		if cfg.Metastore.DSN == "" {
			log.Fatalf("metastore driver 'postgres' requires metastore.dsn (DS3SQL_METASTORE_DSN)")
		}
		s, err := metastore.OpenPostgres(cfg.Metastore.DSN)
		if err != nil {
			log.Fatalf("failed to init postgres metastore: %v", err)
		}
		metaStore = s
	default:
		log.Fatalf("unknown metastore driver %q (want sqlite|postgres)", cfg.Metastore.Driver)
	}
	defer metaStore.Close()
```
`catalog.NewService(metaStore, queryEngine)` already takes a `metastore.Store`, so the rest is unchanged.

- [ ] **Step 6: Build the server**

Run: `go build ./cmd/ds3sql-server/`
Expected: builds with no error.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/ds3sql-server/main.go
git commit -m "feat(config): metastore driver/dsn; select store backend in server"
```

---

## Task 9: Catalog tree fragment handler (server-rendered, test-driven)

**Files:**
- Create: `internal/api/catalog_fragment_handler.go`
- Test: `internal/api/catalog_fragment_handler_test.go`

The interactive tree is rendered by `catalog.js` (Task 11), but we provide a server-rendered HTML fragment endpoint so the catalog rendering logic is unit-testable with `httptest` and usable as a progressive-enhancement / HTMX fallback. It returns an `<ul>` of datasets, each with its tables.

- [ ] **Step 1: Write the failing test**

Create `internal/api/catalog_fragment_handler_test.go`:
```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogFragmentHandler_Tree(t *testing.T) {
	cat := newTestCatalog(t)
	ctx := context.Background()
	if err := cat.CreateDataset(ctx, "p1", "sales"); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "orders.csv")
	if err := os.WriteFile(csv, []byte("id,total\n1,10\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.RegisterTable(ctx, registerOrdersInput(csv), "", "", ""); err != nil {
		t.Fatal(err)
	}

	h := NewCatalogFragmentHandler(cat)
	req := httptest.NewRequest("GET", "/ui/catalog", nil)
	w := httptest.NewRecorder()
	h.TreeForProject(w, req, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "sales") || !strings.Contains(body, "orders") {
		t.Fatalf("fragment missing dataset/table: %s", body)
	}
	// Must carry the data attributes catalog.js / onclick uses.
	if !strings.Contains(body, `data-dataset="sales"`) || !strings.Contains(body, `data-table="orders"`) {
		t.Fatalf("fragment missing data attributes: %s", body)
	}
	// Must be HTML, not JSON.
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html, got %q", ct)
	}
}

func TestCatalogFragmentHandler_Empty(t *testing.T) {
	cat := newTestCatalog(t)
	h := NewCatalogFragmentHandler(cat)
	req := httptest.NewRequest("GET", "/ui/catalog", nil)
	w := httptest.NewRecorder()
	h.TreeForProject(w, req, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No datasets") {
		t.Fatalf("expected empty-state text, got %s", w.Body.String())
	}
}

// registerOrdersInput keeps the test self-contained without importing catalog
// types twice; defined here once and reused.
func registerOrdersInput(csv string) catalogRegisterInput {
	return catalogRegisterInput{Dataset: "sales", Name: "orders", Location: csv, Format: "csv"}
}
```

> The test references `catalogRegisterInput` and `cat.RegisterTable(...)`. To avoid a second import alias, define a tiny local adapter in the test using the real `catalog.RegisterTableInput`. Replace the helper with the real type:
```go
import "github.com/esignoretti/ds3-sql-server/internal/catalog"

func registerOrdersInput(csv string) catalog.RegisterTableInput {
	return catalog.RegisterTableInput{ProjectID: "p1", Dataset: "sales", Name: "orders", Location: csv, Format: "csv"}
}
```
and delete the `catalogRegisterInput` reference. (`newTestCatalog` already exists in `dataset_handler_test.go`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestCatalogFragmentHandler_Tree -v`
Expected: FAIL — `undefined: NewCatalogFragmentHandler`.

- [ ] **Step 3: Implement the handler**

Create `internal/api/catalog_fragment_handler.go`:
```go
package api

import (
	"html/template"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// CatalogFragmentHandler renders an HTML fragment of the dataset/table tree for
// the Web UI catalog browser. It is the testable, progressive-enhancement
// rendering path; catalog.js consumes the JSON /datasets endpoints for the live
// interactive tree.
type CatalogFragmentHandler struct {
	cat  *catalog.Service
	tmpl *template.Template
}

type catalogTreeData struct {
	Datasets []datasetNode
}

type datasetNode struct {
	Name   string
	Tables []*metastore.Table
}

const catalogTreeTmpl = `{{if not .Datasets}}<p class="catalog-empty">No datasets. Create one to get started.</p>{{else}}<ul class="catalog-tree">{{range .Datasets}}<li class="catalog-ds"><div class="catalog-ds-name" data-dataset="{{.Name}}">{{.Name}}</div><ul class="catalog-tables">{{range .Tables}}<li class="catalog-table" data-dataset="{{.Dataset}}" data-table="{{.Name}}" onclick="selectCatalogTable('{{.Dataset}}','{{.Name}}')"><span class="catalog-table-name">{{.Name}}</span> <span class="catalog-table-meta">{{.Format}} · {{.Stats.RowCount}} rows</span></li>{{else}}<li class="catalog-table-empty">no tables</li>{{end}}</ul></li>{{end}}</ul>{{end}}`

func NewCatalogFragmentHandler(cat *catalog.Service) *CatalogFragmentHandler {
	return &CatalogFragmentHandler{
		cat:  cat,
		tmpl: template.Must(template.New("catalogTree").Parse(catalogTreeTmpl)),
	}
}

func (h *CatalogFragmentHandler) TreeForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	ctx := r.Context()
	datasets, err := h.cat.ListDatasets(ctx, projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	data := catalogTreeData{}
	for _, ds := range datasets {
		tables, err := h.cat.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		data.Datasets = append(data.Datasets, datasetNode{Name: ds.Name, Tables: tables})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'TestCatalogFragmentHandler_Tree|TestCatalogFragmentHandler_Empty' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole api package**

Run: `go test ./internal/api/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
git add internal/api/catalog_fragment_handler.go internal/api/catalog_fragment_handler_test.go
git commit -m "feat(api): server-rendered catalog tree fragment"
```

---

## Task 10: Wire the catalog-fragment route and `GET /jobs` history

**Files:**
- Modify: `cmd/ds3sql-server/main.go`

The UI tree (Task 11) fetches the JSON `/datasets…` endpoints, but we also mount the HTML fragment endpoint (`GET /ui/catalog`) and ensure the jobs-history list endpoint `GET /jobs` (added by Phase 2) is reachable for the jobs panel. If Phase 2 already mounts `GET /jobs`, this task only adds `/ui/catalog`; verify and avoid duplicate registration.

- [ ] **Step 1: Construct the fragment handler**

In `cmd/ds3sql-server/main.go`, after `jobHandler := api.NewJobHandler(jobManager)`, add:
```go
	catalogFragmentHandler := api.NewCatalogFragmentHandler(catService)
```

- [ ] **Step 2: Mount the fragment route in the first protected group**

Inside the protected group that already mounts `/datasets`, add:
```go
		r.Get("/ui/catalog", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					catalogFragmentHandler.TreeForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
```

- [ ] **Step 3: Ensure `GET /jobs` history is mounted (Phase 2)**

Verify the first protected group contains a `r.Get("/jobs", …)` route surfacing `jobHandler`'s list (Phase 2). If absent (e.g. running Phase 4 against a tree where Phase 2 did not add it), add:
```go
		r.Get("/jobs", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					jobHandler.ListForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
```
> If `jobHandler.ListForProject` does not exist (Phase 2 used a different method name), use the existing list method. Do not duplicate an already-mounted `GET /jobs`.

- [ ] **Step 4: Build the server**

Run: `go build ./cmd/ds3sql-server/`
Expected: builds with no error.

- [ ] **Step 5: Commit**

```bash
git add cmd/ds3sql-server/main.go
git commit -m "feat(server): mount catalog tree fragment and jobs history routes"
```

---

## Task 11: Catalog browser in the Web UI (primary nav)

**Files:**
- Create: `internal/web/static/catalog.js`
- Create: `internal/web/templates/catalog_tree.html`
- Modify: `internal/web/templates/layout.html`
- Modify: `internal/web/templates/tab_browse.html`
- Modify: `internal/web/static/tab-manager.js`
- Modify: `internal/web/static/style.css`

This makes the catalog the primary left-nav. The existing raw bucket browser is preserved as a secondary "Buckets" tab. Server logic is already covered by Task 9; this task is template/JS/CSS plus a MANUAL verification.

- [ ] **Step 1: Add the catalog tab and tree template**

Create `internal/web/templates/catalog_tree.html`:
```html
{{define "tab_catalog"}}
<div class="single-page">
  <div class="top-bar">
    <div class="form-group" style="margin-bottom:0;">
      <label for="catalog-project-select">Project</label>
      <select id="catalog-project-select" class="input" onchange="switchCatalogProject(this.value)">
        <option value="">Select project...</option>
        {{range .Projects}}
        <option value="{{.ProjectID}}">{{.ProjectName}}</option>
        {{end}}
      </select>
    </div>
  </div>

  <div class="catalog-layout">
    <div class="catalog-nav">
      <div class="panel-header">
        <span>Catalog</span>
        <button class="btn btn-secondary" style="font-size:0.75rem;padding:0.15rem 0.5rem;" onclick="newDatasetPrompt()">+ Dataset</button>
      </div>
      <div id="catalog-tree-content" class="panel-body">
        <p style="color:var(--text-muted);font-size:0.85rem;">Select a project to browse the catalog.</p>
      </div>
    </div>
    <div class="catalog-detail">
      <div class="panel-header"><span id="catalog-detail-title">Table details</span></div>
      <div id="catalog-detail-content" class="panel-body">
        <p style="color:var(--text-muted);font-size:0.85rem;">Click a table to see its schema and start a query.</p>
      </div>
    </div>
  </div>
</div>
<script src="/static/catalog.js"></script>
{{end}}
```

- [ ] **Step 2: Implement `catalog.js`**

Create `internal/web/static/catalog.js`:
```javascript
// Catalog browser — DS3 SQL Server. Renders datasets -> tables -> schema, and
// seeds the query editor when a table is clicked.
function switchCatalogProject(id) {
  tabState.browse.project = id;
  // keep the buckets tab's project selector in sync if present
  var bsel = document.getElementById('project-select');
  if (bsel) bsel.value = id;
  loadCatalogTree();
}

function loadCatalogTree() {
  var content = document.getElementById('catalog-tree-content');
  if (!content) return;
  if (!tabState.browse.project) {
    content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project first.</p>';
    return;
  }
  content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Loading…</p>';
  fetch('/datasets?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { content.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var datasets = d.datasets || [];
      if (!datasets.length) {
        content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No datasets. Click “+ Dataset”.</p>';
        return;
      }
      var html = '<ul class="catalog-tree">';
      datasets.forEach(function(ds) {
        var dsId = 'ds-' + escAttr(ds.name);
        html += '<li class="catalog-ds">' +
          '<div class="catalog-ds-name" onclick="toggleDataset(\'' + escJs(ds.name) + '\')">▸ ' + escHtml(ds.name) + '</div>' +
          '<ul class="catalog-tables" id="' + dsId + '" style="display:none;"></ul></li>';
      });
      html += '</ul>';
      content.innerHTML = html;
    })
    .catch(function(e) { content.innerHTML = '<p style="color:var(--red);">Error: ' + escHtml(e.message) + '</p>'; });
}

function toggleDataset(ds) {
  var ul = document.getElementById('ds-' + ds);
  if (!ul) return;
  if (ul.style.display === 'none') {
    ul.style.display = 'block';
    if (!ul.dataset.loaded) loadTablesForDataset(ds, ul);
  } else {
    ul.style.display = 'none';
  }
}

function loadTablesForDataset(ds, ul) {
  ul.innerHTML = '<li class="catalog-table-empty">Loading…</li>';
  fetch('/datasets/' + encodeURIComponent(ds) + '/tables?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { ul.innerHTML = '<li class="catalog-table-empty">' + escHtml(d.error) + '</li>'; return; }
      var tables = d.tables || [];
      if (!tables.length) { ul.innerHTML = '<li class="catalog-table-empty">no tables</li>'; return; }
      var html = '';
      tables.forEach(function(t) {
        html += '<li class="catalog-table" data-dataset="' + escAttr(ds) + '" data-table="' + escAttr(t.name) + '" ' +
          'onclick="selectCatalogTable(\'' + escJs(ds) + '\',\'' + escJs(t.name) + '\')">' +
          '<span class="catalog-table-name">' + escHtml(t.name) + '</span> ' +
          '<span class="catalog-table-meta">' + escHtml(t.format || '') + ' · ' + ((t.stats && t.stats.row_count) || 0) + ' rows</span></li>';
      });
      ul.innerHTML = html;
      ul.dataset.loaded = '1';
    })
    .catch(function(e) { ul.innerHTML = '<li class="catalog-table-empty">Error: ' + escHtml(e.message) + '</li>'; });
}

function selectCatalogTable(ds, table) {
  // Highlight selection
  document.querySelectorAll('.catalog-table.selected').forEach(function(el) { el.classList.remove('selected'); });
  var el = document.querySelector('.catalog-table[data-dataset="' + ds + '"][data-table="' + table + '"]');
  if (el) el.classList.add('selected');

  fetch('/datasets/' + encodeURIComponent(ds) + '/tables/' + encodeURIComponent(table) +
        '?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(t) {
      if (t.error) { return; }
      renderTableDetail(t);
      seedQueryEditor(ds, table);
    });
}

function renderTableDetail(t) {
  var title = document.getElementById('catalog-detail-title');
  if (title) title.textContent = t.dataset + '.' + t.name;
  var c = document.getElementById('catalog-detail-content');
  if (!c) return;
  var html = '<div style="font-size:0.8rem;color:var(--text-muted);margin-bottom:0.5rem;">' +
    escHtml(t.format || '') + ' · ' + escHtml(t.storage_class || '') + ' · ' + ((t.stats && t.stats.row_count) || 0) + ' rows</div>';
  html += '<table class="catalog-schema"><thead><tr><th>Column</th><th>Type</th></tr></thead><tbody>';
  (t.schema || []).forEach(function(col) {
    html += '<tr><td>' + escHtml(col.name) + '</td><td>' + escHtml(col.type) + '</td></tr>';
  });
  html += '</tbody></table>';
  html += '<button class="btn" style="margin-top:0.75rem;" onclick="seedQueryEditor(\'' + escJs(t.dataset) + '\',\'' + escJs(t.name) + '\');navigateToTab(\'query\');">▶ Query this table</button>';
  c.innerHTML = html;
}

function seedQueryEditor(ds, table) {
  var ed = document.getElementById('sql-editor');
  if (ed) ed.value = 'SELECT * FROM ' + ds + '.' + table + ' LIMIT 100';
  var badge = document.getElementById('query-source-badge');
  if (badge) badge.textContent = ds + '.' + table;
}

function newDatasetPrompt() {
  if (!tabState.browse.project) { alert('Select a project first'); return; }
  var name = prompt('New dataset name (letters, digits, underscore):');
  if (!name) return;
  fetch('/datasets?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({name: name})
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { alert(d.error); return; }
    loadCatalogTree();
  });
}
```

- [ ] **Step 3: Register the new tabs in `tab-manager.js`**

In `internal/web/static/tab-manager.js`:

Add catalog/buckets keys to `tabState`:
```javascript
var tabState = {
  catalog: { project: null, selectedTable: null },
  browse: { project: null, bucket: null, prefix: '', selectedFiles: [] },
  transform: { configs: {}, activeFile: null, pendingBucket: null, pendingFile: null },
  query: { sql: '', results: null, currentPage: 0, pageSize: 100 },
  analyze: { analysisCache: null, selectedCols: [] },
  report: { title: '', charts: [], savedId: null }
};
```
In `switchTab`, add a render hook for catalog after the existing hooks:
```javascript
  if (tabName === 'catalog' && typeof loadCatalogTree === 'function') loadCatalogTree();
  if (tabName === 'query' && typeof loadJobsPanel === 'function') loadJobsPanel();
```
Update the two tab-name allow-lists and the default tab (in the `hashchange` listener and `DOMContentLoaded`) to include `catalog` and `buckets` and default to `catalog`:
```javascript
window.addEventListener('hashchange', function() {
  var tab = window.location.hash.replace('#', '') || 'catalog';
  if (['catalog','buckets','transform','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  }
});

document.addEventListener('DOMContentLoaded', function() {
  var tab = window.location.hash.replace('#', '') || 'catalog';
  if (['catalog','buckets','transform','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  } else {
    switchTab('catalog');
  }
});
```
> The existing `browse` tab is renamed `buckets` at the markup level (Step 5). Keep `tabState.browse` as the state key (browse.js, query.js, tab-manager helpers reference it heavily); only the *tab id / nav label* changes to `buckets`. To keep `switchProject` (buckets) and `switchCatalogProject` in sync, both write `tabState.browse.project`.

- [ ] **Step 4: Split `tab_browse.html` so the bucket browser lives under id `buckets`**

In `internal/web/templates/tab_browse.html`, change the wrapper define and keep the body. Replace the first line `{{define "tab_browse"}}` with `{{define "tab_buckets"}}` and the last `{{end}}` stays. (The body — project selector + browser/selection panels — is unchanged; raw bucket browsing is preserved verbatim as the secondary tab.)

- [ ] **Step 5: Update `layout.html` — Catalog primary, Buckets secondary, load new scripts**

In `internal/web/templates/layout.html`:

Replace the Browse tab nav item and add a Buckets item (catalog first/primary):
```html
                <div class="tab" data-tab="catalog" onclick="navigateToTab('catalog')">
                    Catalog <span class="tab-badge"></span>
                </div>
                <div class="tab" data-tab="buckets" onclick="navigateToTab('buckets')">
                    Buckets <span class="tab-badge"></span>
                </div>
```
(Delete the old `data-tab="browse"` nav item; keep Transform/Query/Analyze/Report.)

In the `{{if eq .Page "app"}}` content block, add the catalog content first and rename the browse content id to `buckets`:
```html
            <div class="tab-content active" id="tab-catalog">{{template "tab_catalog" .}}</div>
            <div class="tab-content" id="tab-buckets">{{template "tab_buckets" .}}</div>
            <div class="tab-content" id="tab-transform">{{template "tab_transform" .}}</div>
            <div class="tab-content" id="tab-query">{{template "tab_query" .}}</div>
            <div class="tab-content" id="tab-analyze">{{template "tab_analyze" .}}</div>
            <div class="tab-content" id="tab-report">{{template "tab_report" .}}</div>
```
(The `catalog_tree.html` template is parsed automatically by `ParseFS(templateFS, "templates/*.html")` — no handler change needed.)

- [ ] **Step 6: Add catalog CSS**

Append to `internal/web/static/style.css`:
```css
/* Catalog browser */
.catalog-layout { display:flex; gap:0.75rem; }
.catalog-nav { flex:1; max-width:360px; background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); display:flex; flex-direction:column; min-height:320px; }
.catalog-detail { flex:2; background:var(--surface); border:0.0625rem solid var(--border); border-radius:var(--radius); display:flex; flex-direction:column; min-height:320px; }
.catalog-tree { list-style:none; margin:0; padding:0; }
.catalog-ds-name { cursor:pointer; font-weight:600; padding:0.3rem 0.25rem; font-size:0.9rem; }
.catalog-ds-name:hover { color:var(--primary); }
.catalog-tables { list-style:none; margin:0 0 0.25rem 0.75rem; padding:0; }
.catalog-table { cursor:pointer; padding:0.25rem 0.4rem; border-radius:var(--radius); font-size:0.85rem; display:flex; justify-content:space-between; gap:0.5rem; }
.catalog-table:hover { background:var(--surface-2); }
.catalog-table.selected { background:var(--surface-2); outline:0.0625rem solid var(--primary); }
.catalog-table-meta { color:var(--text-muted); font-size:0.75rem; }
.catalog-table-empty { color:var(--text-muted); font-size:0.8rem; padding:0.2rem 0.4rem; list-style:none; }
.catalog-empty { color:var(--text-muted); font-size:0.85rem; }
.catalog-schema { width:100%; border-collapse:collapse; font-size:0.85rem; }
.catalog-schema th, .catalog-schema td { text-align:left; padding:0.25rem 0.5rem; border-bottom:0.0625rem solid var(--border); }
```

- [ ] **Step 7: Build the server (embeds templates/static) and run existing web-adjacent tests**

Run:
```bash
go build ./...
go test ./internal/api/ ./internal/web/
```
Expected: builds (the new templates embed cleanly); api tests PASS. (`internal/web` has no Go tests beyond compilation; the fragment handler test in `internal/api` covers server rendering.)

- [ ] **Step 8: MANUAL verification (interactive tree)**

1. Build and start the server against an empty temp metastore:
   ```bash
   go build -o /tmp/ds3sql-server ./cmd/ds3sql-server/
   DS3SQL_METASTORE_PATH=/tmp/ds3sql-ui-meta.db DS3SQL_ROLE=all /tmp/ds3sql-server --port 18080
   ```
2. Log in via the browser at `http://localhost:18080/login` with valid Cubbit IAM credentials, landing on `/app`.
3. **Expected:** the leftmost/active tab is **Catalog** (not Browse); a **Buckets** tab is present to its right.
4. Select a project in the Catalog tab's Project dropdown.
   **Expected:** the catalog tree loads. With no datasets it shows "No datasets. Click "+ Dataset"." Click **+ Dataset**, enter `sales`. The tree refreshes and shows `sales`.
5. Register a table (CLI in another terminal): `ds3sql tables register sales.orders --location 's3://<bucket>/orders/*.parquet' --format parquet`. Back in the UI, click `sales` to expand.
   **Expected:** `orders` appears with its format and row count. Clicking `orders` shows its schema (Column/Type) in the right detail panel and seeds the SQL editor.
6. Click **▶ Query this table** (or open the Query tab).
   **Expected:** the SQL editor contains `SELECT * FROM sales.orders LIMIT 100`; clicking **▶ Run** returns rows.
7. Open the **Buckets** tab.
   **Expected:** the original raw bucket browser still works (lists buckets/objects, file selection, convert) — unchanged.

- [ ] **Step 9: Commit**

```bash
git add internal/web/ 
git commit -m "feat(web): catalog browser as primary nav; demote raw browse to Buckets tab"
```

---

## Task 12: Jobs / history panel in the query tab

**Files:**
- Create: `internal/web/static/jobs.js`
- Modify: `internal/web/templates/tab_query.html`
- Modify: `internal/web/static/style.css`

Lists recent jobs from `GET /jobs`; clicking a job re-loads its SQL into the editor (and its result if available via `GET /jobs/{id}`). The list endpoint's server logic is Phase 2's; here we add the panel + a MANUAL step. The `GET /jobs/{id}` shape is the Phase 1 `job.Job` envelope (`{id,type,sql,status,result,error,created_at}`); the list shape is Phase 2's `JobRecord` list (`{jobs:[{id,type,sql,status,row_count,created_at,...}]}`). `jobs.js` tolerates both via optional chaining.

- [ ] **Step 1: Add the jobs panel markup to `tab_query.html`**

In `internal/web/templates/tab_query.html`, inside the outer `<div style="display:flex;flex-direction:column;gap:0.75rem;">`, add a third card after the Results card (before its closing `</div>` that ends the flex container):
```html
  <div class="card" style="margin:0;">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:0.5rem;">
      <span style="font-weight:600;">Recent Jobs</span>
      <button class="btn btn-secondary" style="font-size:0.75rem;padding:0.15rem 0.5rem;" onclick="loadJobsPanel()">↻ Refresh</button>
    </div>
    <div id="jobs-panel-content" style="overflow-x:auto;">
      <p style="color:var(--text-muted);font-size:0.85rem;">No jobs yet.</p>
    </div>
  </div>
```
Add the script include next to the existing `query.js` include at the bottom of the define:
```html
<script src="/static/jobs.js"></script>
```

- [ ] **Step 2: Implement `jobs.js`**

Create `internal/web/static/jobs.js`:
```javascript
// Jobs / history panel — DS3 SQL Server.
function loadJobsPanel() {
  var c = document.getElementById('jobs-panel-content');
  if (!c) return;
  if (!tabState.browse.project) {
    c.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project to see job history.</p>';
    return;
  }
  fetch('/jobs?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { c.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var jobs = d.jobs || [];
      if (!jobs.length) { c.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No jobs yet.</p>'; return; }
      var html = '<table class="jobs-table"><thead><tr><th>Status</th><th>Type</th><th>Rows</th><th>SQL</th><th>When</th></tr></thead><tbody>';
      jobs.forEach(function(j) {
        var rows = (j.row_count != null) ? j.row_count : (j.result && j.result.row_count) || '';
        var sql = (j.sql || '').slice(0, 80);
        var when = j.created_at ? new Date(j.created_at).toLocaleString() : '';
        html += '<tr class="jobs-row" onclick="loadJob(\'' + escJs(j.id) + '\')">' +
          '<td><span class="job-status job-' + escAttr(j.status) + '">' + escHtml(j.status) + '</span></td>' +
          '<td>' + escHtml(j.type || 'query') + '</td>' +
          '<td>' + escHtml(String(rows)) + '</td>' +
          '<td class="jobs-sql">' + escHtml(sql) + '</td>' +
          '<td class="jobs-when">' + escHtml(when) + '</td></tr>';
      });
      html += '</tbody></table>';
      c.innerHTML = html;
    })
    .catch(function(e) { c.innerHTML = '<p style="color:var(--red);">Error: ' + escHtml(e.message) + '</p>'; });
}

function loadJob(id) {
  fetch('/jobs/' + encodeURIComponent(id) + '?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(j) {
      if (j.error && !j.sql) { alert(j.error); return; }
      var ed = document.getElementById('sql-editor');
      if (ed && j.sql) ed.value = j.sql;
      // If the job carries an inline result (sync fast-path), render it.
      if (j.result && j.result.columns) {
        tabState.query.results = j.result;
        tabState.query.currentPage = 0;
        var status = document.getElementById('query-status');
        if (status) status.innerHTML = (j.result.row_count || 0) + ' rows (from job ' + escHtml(id) + ')';
        if (typeof renderPage === 'function' && j.result.row_count) {
          document.getElementById('export-bar').style.display = 'flex';
          renderPage();
        }
      }
    });
}
```

- [ ] **Step 3: Add jobs-panel CSS**

Append to `internal/web/static/style.css`:
```css
/* Jobs panel */
.jobs-table { width:100%; border-collapse:collapse; font-size:0.82rem; }
.jobs-table th, .jobs-table td { text-align:left; padding:0.3rem 0.5rem; border-bottom:0.0625rem solid var(--border); }
.jobs-row { cursor:pointer; }
.jobs-row:hover { background:var(--surface-2); }
.jobs-sql { font-family:monospace; color:var(--text-muted); max-width:380px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.jobs-when { color:var(--text-muted); white-space:nowrap; }
.job-status { padding:0.05rem 0.4rem; border-radius:var(--radius); font-size:0.72rem; }
.job-done { background:rgba(39,182,129,0.18); color:#27B681; }
.job-failed { background:rgba(248,113,113,0.18); color:#f87171; }
.job-running, .job-queued { background:rgba(243,179,86,0.18); color:#F3B356; }
```

- [ ] **Step 4: Build and run tests**

Run:
```bash
go build ./...
go test ./internal/api/
```
Expected: builds (new template/static embed cleanly); api tests PASS.

- [ ] **Step 5: MANUAL verification (jobs panel)**

1. Start the server (as in Task 11 Step 8) and log in; select a project.
2. Open the **Query** tab. Run a query (e.g. seeded `SELECT * FROM sales.orders LIMIT 100`, or any `SELECT 1`).
3. **Expected:** below the Results card, the **Recent Jobs** panel lists the just-run job with a green `done` status, its type, row count, a truncated SQL preview, and a timestamp.
4. Click the job row.
   **Expected:** the SQL editor is repopulated with that job's SQL; if the job carried an inline result, the Results table re-renders and the status reads "… rows (from job …)".
5. Click **↻ Refresh**.
   **Expected:** the list reloads without error. Switching away to Catalog and back to Query auto-refreshes the panel (the `switchTab('query')` hook calls `loadJobsPanel()`).

- [ ] **Step 6: Commit**

```bash
git add internal/web/static/jobs.js internal/web/templates/tab_query.html internal/web/static/style.css
git commit -m "feat(web): jobs/history panel in the query tab"
```

---

## Task 13: Bind analyze & report to catalog-driven results (wiring)

**Files:**
- Modify: `internal/web/static/catalog.js` (reset downstream tabs on table select)
- Modify: `internal/web/static/tab-manager.js` (ensure `getNextStep`/badges work from catalog)

Analyze and report already operate on `tabState.query.results` (see `analyzeResults()` in `query.js` and `renderReportTab`). A catalog-driven query populates `tabState.query.results` exactly like a bucket-driven one, so analyze/report work unchanged. The only wiring needed is to reset stale downstream state when a *new* catalog table is selected, so an old analysis/report doesn't linger.

- [ ] **Step 1: Reset downstream tabs when seeding from the catalog**

In `internal/web/static/catalog.js`, at the end of `seedQueryEditor`, add:
```javascript
  // A fresh table selection invalidates any prior query/analysis/report.
  if (typeof resetDownstreamTabs === 'function') resetDownstreamTabs('browse');
```
(`resetDownstreamTabs('browse')` clears query/analyze/report state — defined in `tab-manager.js`.)

- [ ] **Step 2: Confirm analyze/report reachability from a catalog query (no code change expected)**

Verify in `tab-manager.js` that `switchTab('analyze')` calls `renderAnalyzeTab()` and `switchTab('report')` calls `renderReportTab()`, and both read `tabState.query.results`. No change required if so; if `renderAnalyzeTab`/`renderReportTab` gate on `tabState.browse.selectedFiles` (bucket-specific), relax that gate to also accept `tabState.query.results` being present. (Inspect `query.js`/`report.js`; if they only check `tabState.query.results`, leave as is.)

- [ ] **Step 3: MANUAL verification (analyze/report from catalog)**

1. Start the server, log in, select a project (Task 11).
2. In **Catalog**, click a table → **▶ Query this table** → **▶ Run**.
3. Click **📊 Analyze** (or the **Analyze** tab).
   **Expected:** the Analyze tab renders column profiles for the catalog-table query results (identical behaviour to a bucket-sourced query).
4. Open the **Report** tab and add a chart, then **Save**.
   **Expected:** the report saves and appears under **Saved Reports**, sourced from the catalog query.
5. Select a *different* catalog table.
   **Expected:** prior analyze/report state is cleared (downstream reset), so stale results don't carry over.

- [ ] **Step 4: Build + full test run**

Run:
```bash
go build ./...
go test ./...
```
Expected: all build; all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/static/catalog.js internal/web/static/tab-manager.js
git commit -m "feat(web): bind analyze/report to catalog-driven query results"
```

---

## Task 14: Docs + final verification

**Files:**
- Modify: `docs/architecture.md`, `docs/configuration.md`, `docs/deployment.md`, `README.md`

- [ ] **Step 1: Full build, vet, race tests**

Run:
```bash
go build ./...
go vet ./...
go test -race ./...
```
Expected: no build/vet errors; all tests PASS (Postgres conformance SKIPs without `DS3SQL_TEST_POSTGRES_DSN`).

- [ ] **Step 2: Update `docs/architecture.md`**

Add a "Phase 4: Product Polish" subsection covering:
- **Web catalog browser**: primary left-nav (datasets → tables → columns) backed by `/datasets…` JSON + the server-rendered `/ui/catalog` fragment (`internal/api/catalog_fragment_handler.go`, `internal/web/static/catalog.js`); raw bucket browsing demoted to the secondary **Buckets** tab. Jobs/history panel in the query tab (`jobs.js`, `GET /jobs`).
- **Postgres metastore**: `internal/metastore/postgres.go` implements the full `Store` interface (datasets, tables, jobs, cache_index, schedules) using `pgx/v5/stdlib`; schema mirrors SQLite with `TIMESTAMPTZ`/`BIGINT`/`JSONB`; selected by `metastore.driver`. A shared `testStoreConformance` suite runs against both backends.
- **Partition pruning**: `internal/planner/prune.go` is a pure predicate analyzer; `catalog.ResolvePruned` builds `ReaderSQL` from only the matching partitions. Document supported predicates (`=`, `IN`, `>`,`>=`,`<`,`<=`, combined with `AND`) and the conservative fallback for unsupported forms (`OR`, `NOT`, expressions, non-partition predicates → scan all).

- [ ] **Step 3: Update `docs/configuration.md`**

Document the new metastore settings:
- `metastore.driver` (`DS3SQL_METASTORE_DRIVER`) — `sqlite` (default) | `postgres`.
- `metastore.path` (`DS3SQL_METASTORE_PATH`) — used when `driver=sqlite`; default `~/.ds3sql/metastore.db`.
- `metastore.dsn` (`DS3SQL_METASTORE_DSN`) — required when `driver=postgres`, e.g. `postgres://user:pass@host:5432/ds3sql?sslmode=require`.
Include a YAML example:
```yaml
metastore:
  driver: postgres
  dsn: "postgres://ds3:secret@db.internal:5432/ds3sql?sslmode=require"
```

- [ ] **Step 4: Update `docs/deployment.md`**

Add a "High-availability coordinator (Postgres)" section: run multiple coordinator processes (`--role=coordinator`) sharing one Postgres metastore via `DS3SQL_METASTORE_DRIVER=postgres` + `DS3SQL_METASTORE_DSN=…`; SQLite remains the single-process default for dev/small deployments. Note the test DSN env var `DS3SQL_TEST_POSTGRES_DSN` for running the conformance suite against a real DB in CI.

- [ ] **Step 5: Update `README.md`**

Add to the feature list / Quick Start: the **Catalog browser** and **Jobs panel** in the Web UI, the optional **Postgres** metastore (`DS3SQL_METASTORE_DRIVER=postgres`), and that catalog queries automatically **prune partitions** for `WHERE` filters on partition columns.

- [ ] **Step 6: Final build/test and commit**

Run:
```bash
go build ./...
go test ./...
```
Expected: all PASS.

```bash
git add docs/ README.md
git commit -m "docs: catalog browser, jobs panel, Postgres metastore, partition pruning"
```

---

## Self-Review

**Phase 4 spec coverage (each requirement → task):**
- Catalog browser as primary Web UI nav (datasets → tables → columns); raw browse demoted → Tasks 9 (server fragment, tested), 11 (catalog.js + templates + CSS + MANUAL). ✓
- Clicking a table seeds `SELECT * FROM dataset.table LIMIT 100` and shows schema → Task 11 (`selectCatalogTable`/`seedQueryEditor`/`renderTableDetail`). ✓
- Server endpoints wired (HTMX/JS over existing `/datasets…`) → Tasks 9–11 (JSON endpoints reused; `/ui/catalog` fragment added). ✓
- Jobs/history panel listing `GET /jobs`, click re-loads SQL/result → Task 12 (`jobs.js`, tested list shape tolerance; MANUAL). ✓
- Analyze/report bind to catalog tables → Task 13 (wiring + downstream reset; MANUAL). ✓
- Postgres metastore implementing the FULL Store interface → Tasks 4 (skeleton+migration), 6 (datasets/tables), 7 (jobs/cache/schedules); `var _ Store = (*PostgresStore)(nil)` in Task 4. ✓
- `OpenPostgres(dsn)` with `CREATE TABLE IF NOT EXISTS`, JSON as `jsonb` → Task 4. ✓
- Config `Metastore.Driver`/`Metastore.DSN` + env vars; server picks store by driver → Task 8. ✓
- Shared conformance suite: SQLite always, Postgres gated on `DS3SQL_TEST_POSTGRES_DSN` with `t.Skip()`; table-driven `testStoreConformance(t, store)` → Tasks 5, 7. ✓
- Partition pruning: `Partition{Values,Location,RowCount,Min,Max}`, stored as `Stats.Partitions` (backward-compatible JSON) → Task 1; pure predicate→selection logic, thoroughly unit-tested → Task 2; `ReaderSQL` from pruned list (`read_parquet([...])`) and wired into resolution/executor → Task 3. ✓
- Supported vs unsupported predicate forms documented and tested → Task 2 (tests for `=`, `IN`, range, `AND`, multi-column; conservative fallback for `OR`/non-partition/no-WHERE) + Task 14 docs. ✓
- Docs: architecture, configuration, deployment, README → Task 14. ✓

**Type-consistency check:**
- `metastore.Partition` (Task 1) ↔ `planner.Partition` (Task 2): distinct types (no import cycle, since `catalog` imports both); converted by `toPlannerPartitions` in `catalog.ResolvePruned` (Task 3). ✓
- `Stats.Partitions []Partition` round-trips through both SQLite (existing JSON `stats_json`) and Postgres (`jsonb stats_json`, marshalled via `mustJSON`) — covered by `TableCRUDAndVersion` conformance subtest (Task 5). ✓
- `PostgresStore` method signatures (Tasks 4/6/7) exactly match the `Store` interface declared in `internal/metastore/store.go` (Phase 1 methods) plus the Phase 2/3 method set from the contract; `JobRecord`/`CacheEntry`/`Schedule` field names match the stated contract, with an explicit instruction to defer to `store.go` if they differ. ✓
- `catalog.ResolvePruned` returns `[]query.ViewBinding` (same type as `Resolve`); `job.LocalExecutor` swaps `Resolve`→`ResolvePruned` with identical signature (Task 3). ✓
- `CatalogFragmentHandler.TreeForProject(w, r, projectID)` follows the `…ForProject` convention; reuses `catalog.Service.ListDatasets/ListTables` returning `*metastore.Table` (Task 9), rendered via `html/template` with `metastore.Table.Stats.RowCount`/`.Format`/`.Dataset`/`.Name` (all existing fields). ✓
- `config.MetastoreConfig{Driver,Path,DSN}` (Task 8) consumed by `main.go`'s switch; `catalog.NewService` still takes `metastore.Store` (now possibly `*PostgresStore`). ✓
- Frontend: `catalog.js`/`jobs.js` use shared `escHtml`/`escJs`/`escAttr` from `tab-manager.js`; both read/write `tabState.browse.project` so the Catalog and Buckets project selectors stay in sync; `seedQueryEditor` writes `#sql-editor` consumed by `runQuery()` in `query.js`. ✓

**Placeholder scan:** No TBD/TODO/"handle errors appropriately"/"similar to" placeholders. Every Go step contains complete code; templates and JS modules are complete and reference only existing IDs/functions. The only deliberately-temporary code (Postgres `panic("unimplemented")` stubs in Task 4) is fully replaced in Tasks 6–7, and the plan says so explicitly. ✓

**Explicitly DEFERRED (out of Phase 4 scope, per spec Non-Goals):**
- **Dry-run cost estimation** (bytes-to-scan preview) — NOT implemented; feasible later atop the metadata cache.
- **Materialized views** (incremental refresh, staleness management) — NOT implemented.
- **Distributed single-query execution** (Dremel/Trino-style shuffle across workers) — NOT implemented; one query still executes entirely on one worker. The `job.Executor` seam and `query.ViewBinding` plan remain the clean boundary for adding this later without changing the client API.
- Partition pruning here is **predicate-based over partition-column values** (and optional min/max for range columns); it does not implement DuckDB-side statistics-driven row-group skipping beyond what DuckDB already does within the selected files — that is left to the engine.
