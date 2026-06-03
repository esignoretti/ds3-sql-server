# Multi-Tab Workflow UI — Design Document

**Date**: 2026-06-02
**Status**: Draft

## 1. Overview

Replace the current flat "Console" page with a **5-tab single-page workflow** that guides the user through a logical data pipeline: **Browse → Transform → Query → Analyze → Report**. Each tab is always visible in a horizontal bar. Clicking any tab navigates to that step, providing instant backward/forward navigation.

## 2. Current vs Proposed

### Current
- Sidebar has Console and Reports links
- Console page merges bucket browsing, SQL editor, and query results in one view
- Column Config is a separate page reached via links
- Report is a separate page
- Navigation requires full page loads

### Proposed
- Simplified sidebar (logo + logout)
- Horizontal tab bar with 5 tabs always visible
- Each tab is a self-contained view within the same page
- Tab state preserved when switching
- Smart routing: queryable files → Query tab, convertible files → Transform tab first

## 3. Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Sidebar                                                │
│  ├─ Logo                                                │
│  └─ Logout                                              │
├─────────────────────────────────────────────────────────┤
│  Tab Bar                                                │
│  Browse | Transform | Query | Analyze | Report          │
│  (active highlighted, completed show checkmark)         │
├─────────────────────────────────────────────────────────┤
│  Tab Content Area (switches via JS, state preserved)    │
├─────────────────────────────────────────────────────────┤
│  Navigation Bar: ← Back to [step]    [next step] →      │
└─────────────────────────────────────────────────────────┘
```

### Tab State

Each tab's state is stored in a `tabState` object. State persists when switching tabs. Only a new query (in Query tab) resets Analyze and Report.

```javascript
var tabState = {
  browse: { project: null, bucket: null, prefix: '', selectedFiles: [], convertibleFiles: [] },
  transform: { configs: {}, activeFile: null, previewLines: [] },
  query: { sql: '', results: null, currentPage: 0, pageSize: 100 },
  analyze: { analysisCache: null, selectedCols: [] },
  report: { title: '', charts: [], savedId: null }
};
```

### Pipeline Flow

```
Browse ──→ Transform (if convertible files)
  │           │
  │           └──→ Query (after conversion)
  │                │
  │                └──→ Analyze (after query run)
  │                     │
  │                     └──→ Report (build charts, export)
```

## 4. Tab Details

### 4.1 Browse Tab
**Purpose:** Select project → browse buckets → pick files as data sources.

**Layout (3-column drill-down):**
- Left: Project list from session
- Middle: Buckets in selected project
- Right: Files and prefixes in selected bucket/prefix (queryable files section + convertible files section)

**Smart routing (client-side):**
- Only queryable files (.parquet, .csv, .json, .tsv) → next step is Query
- Convertible files included (.log, .txt, .syslog, .out, .err) → next step is Transform

**Buttons:** "Run Query →" or "Configure & Convert →"

**State preserved:** Selected project, current bucket/prefix, checked files.

### 4.2 Transform Tab
**Purpose:** Configure column parsing for convertible files and convert to Parquet.

**Layout:**
- File queue with status indicators
- Column config editor (delimiter/fixed-width picker, header toggle, column name/type editor, preview with distribution bars)
- Conversion progress with per-file status

**Smart behavior:** If no convertible files selected, shows "No files need conversion" with link back to Browse. After conversion, "Proceed to Query →" appears.

**State preserved:** Column configs per file, conversion job statuses.

### 4.3 Query Tab
**Purpose:** Write SQL against selected files and preview results.

**Layout:**
- Source files badge at top
- SQL editor textarea with Build SQL / Run / Clear buttons
- Results table with pagination, CSV/JSON export
- Column header click → opens analytics side panel (reuse existing)

**State preserved:** SQL text, query results.

### 4.4 Analyze Tab
**Purpose:** Automated column profiling with distributions, top values, correlations.

**Layout:**
- Left sidebar: column checklist with type badges
- Main area: selected column's profile (distribution chart + representative rows)
- Multi-column mode: Ctrl+click for overlaid distributions + correlation matrix
- Summary section: auto-generated text findings

**Smart behavior:** If no query results exist, shows "Run a query first" with link to Query tab.

### 4.5 Report Tab
**Purpose:** Build visual reports, save for later, export data.

**Layout:**
- Editable title + Save / Export CSV / Export JSON / Export PDF buttons
- Chart cards: chart type selector, X/Y/Group dropdowns
- Add chart buttons: Bar, Line, Pie, Scatter, Histogram
- Data table showing query results

**Smart behavior:** If no analysis data, shows "Analyze your data first" with link to Analyze tab.

## 5. Tab Switching Implementation

Tabs use a single-page pattern within Go html/template:

```
layout.html (sidebar + tab bar)
  └─ Tab container
       ├─ #tab-browse
       ├─ #tab-transform
       ├─ #tab-query
       ├─ #tab-analyze
       └─ #tab-report
```

Each tab is a template block rendered on initial load. Switching tabs uses `display:none`/`display:block` — no server round-trip.

### URL Hash Routing
Active tab stored in `window.location.hash`:
- Browser back/forward navigates tabs
- Bookmarkable URLs
- Restored on page reload via `hashchange` event

### Tab Bar UI
```html
<div class="tab-bar">
  <div class="tab active" data-tab="browse" onclick="switchTab('browse')">📁 Browse</div>
  <div class="tab" data-tab="transform" onclick="switchTab('transform')">⚙️ Transform</div>
  <div class="tab" data-tab="query" onclick="switchTab('query')">🔍 Query</div>
  <div class="tab" data-tab="analyze" onclick="switchTab('analyze')">📊 Analyze</div>
  <div class="tab" data-tab="report" onclick="switchTab('report')">📄 Report</div>
</div>
```

### switchTab() Implementation
```javascript
function switchTab(tabName) {
  document.querySelectorAll('.tab-content').forEach(function(el) { el.style.display = 'none'; });
  document.getElementById('tab-' + tabName).style.display = 'block';
  document.querySelectorAll('.tab-bar .tab').forEach(function(t) { t.classList.remove('active'); });
  document.querySelector('.tab-bar .tab[data-tab="' + tabName + '"]').classList.add('active');
  window.location.hash = tabName;
  updateTabBadges();
  if (tabName === 'analyze') renderAnalyzeTab();
  if (tabName === 'report') renderReportTab();
}
```

## 6. Smart Routing

```javascript
function getNextStep() {
  var browse = tabState.browse;
  var hasConvertible = browse.selectedFiles.some(isConvertibleFile);
  if (browse.selectedFiles.length === 0) return null;
  if (hasConvertible && !allConverted()) return 'transform';
  if (!tabState.query.results) return 'query';
  if (!tabState.analyze.analysisCache) return 'analyze';
  return 'report';
}
```

Tab badges show contextual status: count of files, conversion status, checkmarks for completed steps.

## 7. File Changes

| File | Action |
|------|--------|
| `internal/web/templates/layout.html` | Modify — add tab bar, tab content container, update sidebar |
| `internal/web/templates/browse-tab.html` | Create — extracted browse template block |
| `internal/web/templates/query-tab.html` | Create — extracted query template block |
| `internal/web/templates/transform-tab.html` | Create — extracted from column_config |
| `internal/web/templates/analyze-tab.html` | Create — extracted analytics panel |
| `internal/web/templates/report-tab.html` | Create — extracted from report.html |
| `internal/web/templates/column_config.html` | Remove (subsumed by Transform tab) |
| `internal/web/templates/query.html` | Remove (dead page) |
| `internal/web/templates/browse.html` | Remove (split into tab templates) |
| `internal/web/templates/report.html` | Remove (subsumed by Report tab) |
| `internal/web/static/style.css` | Modify — add tab bar, tab content, badge CSS |
| `internal/web/static/tab-manager.js` | Create — tab switching, state, hash routing |
| `internal/web/static/browse.js` | Create — file browsing JS extracted from browse.html |
| `internal/web/static/query.js` | Create — query JS extracted from browse.html |
| `internal/web/static/analyze.js` | Create — analytics JS extracted from browse.html |
| `internal/web/static/report.js` | Modify — adapt to tab context |
| `internal/web/handler.go` | Modify — simplify to single `/app` route |
| `cmd/ds3sql-server/main.go` | Modify — update routes |

## 8. JS Extraction Plan

Current `browse.html` (~1430 lines inline) splits into:

- **browse.js:** `switchProject`, `loadPrefix`, `manualBucket`, `togglePath`, `updateBadge`, `buildSQL`, `reader`, `updateConvertBtn`, `startConvert`, `pollConvertStatus`, `fmtSize`, `download`, `exportCSV`, `exportJSON`

- **query.js:** `runQuery`, `renderPage`, `prevPage`, `nextPage`, `clearQuery`, `analyzeResults`, all panel/chart functions, `openAnalyticsPanel`, `closeAnalyticsPanel`, `fetchAnalysis`, `computeQuickStats`, `selectRepresentativeRows`, `renderAnalyticsPanel`, `renderSingleColumnPanel`, `renderMultiColumnPanel`, all chart builders, multi-column functions, `scrollToRow`, `formatCell`, `findColumnSummary`, `findCorrelation`, `detectColumnCategory`, `getColType`, `panelState`, `PANEL_COLORS`

- **tab-manager.js:** `switchTab`, `getNextStep`, `updateTabBadges`, hash routing, tab state initialization

## 9. CSS Additions

```css
.tab-bar { background: var(--surface); border-bottom: 1px solid var(--border); padding: 0 1rem; display: flex; }
.tab-bar .tab { padding: 0.75rem 1.25rem; font-size: 0.85rem; color: var(--text-muted); cursor: pointer; border-bottom: 2px solid transparent; display: flex; align-items: center; gap: 0.375rem; }
.tab-bar .tab:hover { color: var(--text); background: var(--surface-2); }
.tab-bar .tab.active { color: var(--text); border-bottom-color: var(--primary); font-weight: 600; }
.tab-content { display: none; flex: 1; overflow-y: auto; padding: 1.5rem; }
.tab-content.active { display: block; }
.tab-badge { font-size: 0.65rem; background: var(--surface-2); padding: 0.1rem 0.4rem; border-radius: 1rem; }
```

## 10. Route Changes

| Old | New | Purpose |
|-----|-----|---------|
| `/browse` | `/app` | Main app page with 5 tabs |
| `/query` | — | Removed |
| `/column-config` | — | Subsumed by Transform tab |
| `/report` | `/app#report` | Report tab via hash |
| `/reports` | Keep | Saved reports list |
| `/login` | Keep | Login |

Single `/app` route serves the entire multi-tab app. Tab state via URL hash.

## 11. Backward/Forward Navigation

Two levels:
1. **Tab clicks** — click any tab to jump directly
2. **Back/Next buttons** — at bottom of each tab, calls `switchTab()` with adjacent tab name
3. **Browser history** — `hashchange` event calls `switchTab()`

## 12. Tab Reset Triggers

- Selecting new files in Browse → resets Transform, Query, Analyze, Report
- Running a new query → resets Analyze, Report

## 13. Non-Goals

- Drag-reorder tabs
- Tab pinning or customization
- Server-side tab state
- Real-time collaboration
- Mobile-responsive sidebar
