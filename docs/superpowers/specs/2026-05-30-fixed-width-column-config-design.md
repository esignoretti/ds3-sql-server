# Fixed-Width Column Configuration

## Summary

Add fixed-width field parsing as a second mode alongside delimiter-based parsing in column configs. Users define column boundaries by character position with a clickable preview ruler. Gaps between columns are discarded. Conversion uses DuckDB `read_text()` + `substr()`.

## Data Model

### ColumnConfig changes

```go
type ColumnConfig struct {
    Bucket    string       `json:"bucket"`
    Pattern   string       `json:"pattern"`
    Mode      string       `json:"mode"`       // "delimiter" | "fixed_width"
    Delimiter string       `json:"delimiter"`  // used only in delimiter mode
    Quote     string       `json:"quote"`      // used only in delimiter mode
    HeaderRow bool         `json:"header_row"` // used only in delimiter mode
    Columns   []ColumnDef  `json:"columns"`
    CreatedAt time.Time    `json:"created_at"`
    UpdatedAt time.Time    `json:"updated_at"`
}
```

Default `Mode` is `"delimiter"` for backward compatibility with existing saved configs.

### ColumnDef changes

```go
type ColumnDef struct {
    Name  string `json:"name"`
    Type  string `json:"type"`
    Start *int   `json:"start,omitempty"` // 0-based character position (fixed_width mode)
    End   *int   `json:"end,omitempty"`   // exclusive, nil = rest of line (fixed_width mode)
}
```

- `Start=0, End=16` → chars 0–15 inclusive
- `Start=17, End=30` → chars 17–29 inclusive
- `Start=31, End=nil` → from char 31 to end of line
- Characters not covered by any `[Start, End)` range are discarded
- `Start`/`End` are ignored in delimiter mode

## Conversion Engine

For fixed-width mode, replace `read_csv(...)` with:

```sql
SELECT
  CAST(substr(line, 0+1, 16-0) AS VARCHAR) AS "col0",
  CAST(substr(line, 17+1, 30-17) AS VARCHAR) AS "col1",
  CAST(substr(line, 31+1)       AS VARCHAR) AS "col2"
FROM read_text('s3://bucket/file')
```

- `substr()` is 1-based, so `start+1`
- For `End == nil`, omit length argument: `substr(line, start+1)`
- Each column casts to the user's chosen type (VARCHAR, INTEGER, BIGINT, DOUBLE, BOOLEAN, TIMESTAMP)

## UI

### Mode Toggle

Pills at the top of the config page: **"Delimiter"** | **"Fixed Width"**. Default is Delimiter. Switching mode updates `currentConfig.mode` and re-renders.

In Delimiter mode, the existing delimiter/quote/header controls appear unchanged.

### Fixed-Width Mode — Step 1: Column Positions

A preview line from the file (first row) with:

- **Character ruler** — 0-indexed, marks at 0, 10, 20, 30... with the position number displayed below each tick
- **Column segments** — each column colored with a distinct background (CSS class cycle: `seg-0`..`seg-7`, repeating, defined in the shared stylesheet)
- **Split markers** — vertical lines between columns
  - Click on a character position to **add a split** at that position (creates two new column boundaries, splitting the clicked column in two)
  - Click within **2 characters** (absolute distance `|clickPos - splitPos| ≤ 2`) of an existing split to **remove it** (merges the two adjacent columns)
  - (Drag to adjust is deferred to a future iteration — click + number inputs cover the use case for now)
- Below the ruler, a row of **number inputs** for each column: Start, End, plus a **delete column** button (×)
- Below the number row: the **column name** and **type** dropdowns from the delimiter mode (unchanged)
- A **"+ Column"** button that inserts a new column (default width 10 chars) after the last existing column, or at position 0 if no columns exist

### Fixed-Width Mode — Step 2: Preview Table

Renders the first 25 lines of the file, split by the current positions using `line.slice(start, end)` in JS. Same visual table as delimiter preview.

### Step 3: Save

Unchanged — pattern input + Save / Save & Convert buttons.

## Backward Compatibility

- Existing saved configs have no `Mode` field, which defaults to `"delimiter"` during JSON unmarshal
- Existing column defs have no `Start`/`End` fields, which default to nil
- `Match()` logic unchanged — same bucket+pattern matching

## Files Changed

| File | Change |
|------|--------|
| `internal/column/config.go` | Add `Mode` field to `ColumnConfig`, `Start`/`End` to `ColumnDef` |
| `internal/convert/engine.go` | Add fixed-width branch in `convertFile()` using `read_text()` + `substr()` |
| `internal/web/static/column_config.js` | Add mode toggle, clickable ruler, position editors, client-side slice preview |
| `internal/web/templates/column_config.html` | No change (template delegates to JS) |
