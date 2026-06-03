# Query Result Column Analytics Panel — Design Document

**Date**: 2026-05-30
**Status**: Draft

## 1. Overview

Replace the current "flawed graphics" in query results with meaningful, automatically-generated column profiles. When viewing query results in the browse page, users can click a column header to open a side panel that shows:

- Column identity (name, type badge, quick stats)
- Auto-chosen distribution chart (histogram, top-K bars, temporal bucket, or boolean proportion)
- Representative data rows from the loaded result set
- Multi-column mode: overlaid distributions + correlation matrix

This builds on the existing analysis engine (`internal/analysis/`) but brings its output inline into the query results page rather than requiring a separate report page.

## 2. Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Browse Page (query → results table)                         │
│                                                              │
│  ┌──────────────────────────────────────────┐                │
│  │  Results Table                            │                │
│  │  ┌──────┬──────┬──────┐                   │                │
│  │  │⇓ col1│ col2 │ col3 │ ← click header   │                │
│  │  ├──────┼──────┼──────┤                   │                │
│  │  │ ...  │ ...  │ ...  │                   │                │
│  │  └──────┴──────┴──────┘                   │                │
│  └──────────┬───────────────────────────────┘                │
│             │ click column header                             │
│             ▼                                                 │
│  ┌─────────────────────────────┐ ┌──────────────────────────┐│
│  │  Column Analytics Panel     │ │  (slides in from right)  ││
│  │  ├─ Column name + type      │ │  400px wide              ││
│  │  ├─ Quick stats (instant)   │ │                          ││
│  │  ├─ Distribution chart      │ │                          ││
│  │  ├─ Representative rows     │ │                          ││
│  │  └─ Multi-column mode       │ │                          ││
│  └─────────────────────────────┘ └──────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
```

### Data Sources

| Data | Source | Timing |
|------|--------|--------|
| Column name, type | query result metadata (`lastResult.columns`) | Instant |
| Quick stats (row count, nulls, distinct) | Client-side computation on visible rows | Instant |
| Full histogram / top-values / correlations | Server-side via existing `/analyze` endpoint | Async (~100-500ms) |
| Representative rows | Client-side filtering of `lastResult.rows` | Instant |

### Flow

1. User runs a query → results rendered in table
2. User clicks a column header → panel opens with instant client-side stats
3. Panel shows skeleton/fast render immediately
4. Background `POST /analyze` call fetches server-computed histogram, exact stats, correlations
5. Panel progressively updates when server data arrives
6. Ctrl+click multiple headers → multi-column mode with overlaid charts + correlation grid

## 3. Side Panel Layout

### Single-Column Mode (default)

```
┌──────────────────────────────┐
│ [×] age               INTEGER│  ← Header: column name + type badge
│──────────────────────────────│
│ Count: 1,234  Null: 12 (1%)  │  ← Quick stats bar
│ Distinct: 87                 │
│──────────────────────────────│
│ ┌──────────────────────────┐ │
│ │    Histogram / Bar Chart │ │  ← Distribution chart (auto-chosen)
│ │    ████▌                 │ │
│ │    ████████              │ │
│ │    ██████████▌           │ │
│ │    ██████████████        │ │
│ │    ███████████████████   │ │
│ └──────────────────────────┘ │
│──────────────────────────────│
│ 📋 Representative Rows       │  ← Section header
│ ┌───┬────────┬──────────┐   │
│ │ # │ age    │ income   │   │  ← Selected col + 2 adjacent cols
│ ├───┼────────┼──────────┤   │
│ │ 1 │ NULL   │ 45,000   │   │  ← Nulls first
│ │ 2 │ 142   │ 92,000   │   │  ← Outliers
│ │ 3 │ 68    │ 120,000  │   │
│ │ 4 │ 34    │ 55,000   │   │  ← Random sample
│ │ 5 │ 28    │ 38,000   │   │
│ └───┴────────┴──────────┘   │
│                              │
│ [Close]                      │
└──────────────────────────────┘
```

### Multi-Column Mode (2+ columns selected)

```
┌──────────────────────────────┐
│ [×] age, income, score  (3)  │  ← Shows count of selected cols
│──────────────────────────────│
│ ┌──────────────────────────┐ │
│ │    Overlaid Distributions │ │  ← Normalized histograms overlaid
│ │    ╱￣╲    ╱╲            │ │      or grouped bars for cats
│ │   ╱   ╲  ╱  ╲           │ │
│ │  ╱     ╲╱    ╲          │ │
│ │ ╱                      ╲ │ │
│ └──────────────────────────┘ │
│ ┌──────────────────────────┐ │
│ │    Correlation Matrix     │ │  ← Grid/heatmap of Pearson values
│ │          age inc score    │ │
│ │ age    1.0 0.4  0.2     │ │
│ │ inc    0.4 1.0  0.7     │ │
│ │ score  0.2 0.7  1.0     │ │
│ └──────────────────────────┘ │
│──────────────────────────────│
│ [Column selector sidebar]    │  ← Multi-column list on left
│ ☑ age                        │
│ ☑ income                     │
│ ☐ score                      │
│                              │
│ [Close]                      │
└──────────────────────────────┘
```

## 4. Chart Type Selection Logic

Auto-chosen based on column type (no manual dropdowns needed):

| Column Type | Chart Type | Notes |
|---|---|---|
| Numeric (INT, FLOAT, DOUBLE, DECIMAL) | Histogram | 10-20 auto-bins using equal-width bucketing; y-axis = count. Show mean line + ±1σ markers. |
| Categorical (VARCHAR, TEXT) | Horizontal bar chart | Top 10 values by frequency, sorted desc. Show count + % label on each bar. Remainder grouped as "Other". |
| Temporal (TIMESTAMP, DATE) | Time-series bar chart | Auto-select bucket granularity based on span: <24h → hour, <60d → day, <2y → week, >=2y → month. |
| Boolean | Single horizontal stacked bar | One bar showing true / false / null proportions with % labels. |

### Multi-column overlaid distributions

- **Numeric + Numeric**: Multiple histograms normalized to density (so y-axis is comparable), using semi-transparent fill with distinct colors
- **Categorical + Categorical**: Grouped horizontal bar chart with shared x-axis categories
- **Numeric + Categorical**: Side-by-side box plots per category group
- **Mixed**: Detect common type and show what's comparable, fall back to column list

## 5. Representative Rows Sorting

Rows shown in the panel are a filtered sample from the loaded query results:

1. **Null rows first** — rows where the selected column is null (up to 3)
2. **Outliers** — for numeric columns, rows where `|z-score| > 2` (up to 3)
3. **Median/typical rows** — rows closest to the median value (up to 3)
4. **Random sample** — fill remaining slots randomly across the data

Row selection is purely client-side on the already-loaded result set (no additional server calls).

Each representative row shows:
- Row index (linkable — clicking scrolls the main table to that row by index-matching the `<tr>` via `rows[i]`)
- The selected column's value
- The value of the column immediately to the left and right (for context)

When the selected column is the first or last column, show the first two or last two neighboring columns respectively.

## 6. Quick Stats (Client-Side, Instant)

Before the `/analyze` call completes, compute these from visible rows:

- Row count
- Null count + null percentage
- Distinct count (approximate via Set)
- For numeric: min, max, mean (simple running average)
- For categorical: top 3 values by frequency (approximate)

## 7. Multi-Column Selection Interaction

- Click column header → select that column, open/replace panel
- Ctrl+click additional headers → multi-select, switch panel to multi-column mode
- Click header of already-selected column (no Ctrl) → deselect all others, single-column mode
- If panel is open and user Ctrl+clicks a new header, add to selection; conditionally change chart modes

Multi-column mode shows:
1. **Overlaid distributions** at top (unified chart area)
2. **Correlation matrix** below (for numeric columns), showing a styled grid with Pearson r values and color intensity (red heat or blue diverging)
3. **Column selector** on the left edge within the panel — checkbox list to toggle individual columns on/off

Correlation matrix uses values from the server `/analyze` response (`Correlations` array). Categorical columns show "N/A" or Cramér's V if that's added later.

## 8. Progressive Enhancement Flow

```
User clicks header
  │
  ├─ Panel slides in (CSS transition, 200ms)
  │
  ├─ Client computes quick stats from visible rows
  │  └─ Shows stat bar with count, nulls, distinct
  │
  ├─ Client renders skeleton chart area ("Loading histogram...")
  │
  ├─ POST /analyze (debounced, reuses if same dataset)
  │    │
  │    ├─ Server returns ColumnAnalysis with 
  │    │   histogram / top_values / stats / correlations
  │    │
  │    └─ Panel updates:
  │        ├─ Chart renders with full data
  │        ├─ Stats update to exact values
  │        └─ Summary line appears below chart
  │
  └─ Representative rows rendered (from local data, no wait)
```

The `/analyze` call receives the full result set (all rows in `lastResult`), not just visible pages we rounded the analysis endpoint. This ensures histograms are accurate.

## 9. Column Config Page Enhancements

While the primary focus is query results, the column config page will also get improved visuals:

- Preview lines rendered as a table with column name headers above each column
- After delimiter/fixed-width parsing, each column header shows a small inline distribution bar (using only the preview lines):
  - Frequency of each value per column (compact stacked horizontal bar)
  - Type badge next to column name
- This makes the config preview immediately useful as a data quality check

## 10. File Changes

| File | Action |
|------|--------|
| `internal/web/static/column_config.js` | Modify — add inline distribution bars to preview |
| `internal/web/static/browse.js` | Create (extract from browse.html) — new column analytics panel logic |
| `internal/web/templates/browse.html` | Modify — inline panel JS, CSS, panel container div |
| `internal/web/static/style.css` | Modify — add panel styles, representative row styles, correlation matrix grid |
| `internal/api/analysis_handler.go` | No changes — reuse existing endpoint |
| `internal/analysis/engine.go` | No changes — reuse existing engine |

Note: `browse.js` currently exists inline in `browse.html`. Given the significant new panel logic (~400+ lines), extracting it to a separate JS file is warranted.

## 11. CSS / UI Details

### Panel overlay
- Fixed position, right: 0, top: 0, height: 100vh, width: 400px
- z-index above table, below sidebar
- Background: var(--surface) with left border
- Slides in via `transform: translateX(100%) → translateX(0)` with transition
- Semi-transparent backdrop overlay behind panel (click to close)

### Chart area
- Height: 220px for single-column charts
- Height: 300px for overlaid multi-column charts
- Uses Chart.js (already loaded from CDN)

### Representative rows table
- Compact: font-size 0.75rem, padding 0.25rem 0.5rem
- Alternating row backgrounds for readability
- Row index linked — clicking scrolls main table to that data row (sets scrollTop on the results container)

### Correlation matrix
- CSS Grid layout
- Diagonal cells show column name (bold)
- Off-diagonal cells show correlation value with color intensity:
  - Positive values: blue background, intensity proportional to |r|
  - Negative values: red background, intensity proportional to |r|
  - Near-zero (|r| < 0.1): neutral
  - Null/non-computable: gray with "—"

## 12. Testing

- Click single column header → panel opens, shows quick stats, chart loads
- Ctrl+click multiple columns → multi-column mode, overlaid chart + correlation matrix
- Click outside panel → closes
- Click another single header → panel updates to new column
- Null-filled columns → chart shows "all null" message, representative rows show nulls
- Single-value column → histogram shows single bar with note "only one distinct value"
- Boolean column → stacked bar with true/false/null proportions
- Large query (10,000+ rows) → quick stats and representative rows are instant, histogram arrives async
- Column config preview shows inline distribution bars

## 13. Non-Goals

- Server-side PDF/image export of panel (use browser print)
- Drag-reorder columns in panel
- Save panel state as a report (use existing report page for that)
- Customizable chart aesthetics (labels, colors, axis ranges)
- Real-time updates when query result changes (re-select column to refresh)
