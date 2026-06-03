# Query Result Column Analytics Panel — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace "flawed graphics" in query results with meaningful, automatically-generated column profiles: click a column header to open a side panel with distribution charts, representative rows, and multi-column correlation + overlaid distributions.

**Architecture:** Client-side JS (`browse.js`) manages panel state and renders Chart.js charts. Quick stats and representative rows computed from loaded `lastResult`. Full histograms/top-values/correlations fetched async from existing `POST /analyze` endpoint. No backend changes needed. Column config preview enhanced with inline distribution bars.

**Tech Stack:** Chart.js 4.x (already loaded), vanilla JS (no build step), Go html/template, CSS custom properties

---

### Task 1: Create side panel container + overlay in browse.html

**Files:**
- Modify: `internal/web/templates/browse.html` — add panel container div, backdrop, and panel div after query results area
- Modify: `internal/web/static/style.css` — add panel and overlay styles

- [ ] **Step 1: Add panel HTML structure to browse.html**

Insert after the `</table>` logic in the query results area, before the closing `</div>` of panel-body:

```html
<!-- Column Analytics Panel -->
<div id="analytics-backdrop" class="analytics-backdrop" onclick="closeAnalyticsPanel()"></div>
<div id="analytics-panel" class="analytics-panel">
  <div id="analytics-panel-content">
    <p style="color:var(--text-muted);font-size:0.85rem;">Click a column header to see analytics</p>
  </div>
</div>
```

Place this right before `</div>` (closing panel-body) near line 165 area.

- [ ] **Step 2: Add CSS for panel and backdrop**

Append to `internal/web/static/style.css` before `@media print`:

```css
/* Column Analytics Panel */
.analytics-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.4);
  z-index: 99;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.2s ease;
}
.analytics-backdrop.visible {
  opacity: 1;
  pointer-events: auto;
}
.analytics-panel {
  position: fixed;
  top: 0;
  right: 0;
  width: 440px;
  height: 100vh;
  background: var(--surface);
  border-left: 1px solid var(--border);
  z-index: 100;
  transform: translateX(100%);
  transition: transform 0.2s ease;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: -4px 0 20px rgba(0,0,0,0.3);
}
.analytics-panel.open {
  transform: translateX(0);
}
#analytics-panel-content {
  flex: 1;
  overflow-y: auto;
  padding: 0;
}

/* Panel header */
.apanel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.apanel-header h3 {
  font-size: 1rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.apanel-header .type-badge {
  font-size: 0.7rem;
  color: var(--text-muted);
  background: var(--surface-2);
  padding: 0.15rem 0.5rem;
  border-radius: 0.25rem;
  font-weight: 400;
}
.apanel-close {
  cursor: pointer;
  color: var(--text-muted);
  font-size: 1.25rem;
  line-height: 1;
  background: none;
  border: none;
  padding: 0.25rem;
}
.apanel-close:hover { color: var(--text); }

/* Quick stats bar */
.apanel-stats {
  display: flex;
  gap:  fa1rem;
  padding: 0.75rem 1rem;
  background: var(--surface-2);
  font-size: 0.8rem;
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
}
.apanel-stat {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}
.apanel-stat-label {
  color: var(--text-muted);
}
.apanel-stat-value {
  color: var(--text);
  font-weight: 600;
  font-family: monospace;
}
.apanel-stat-value.null { color: var(--red); }
.apanel-stat-value.distinct { color: var(--green-500); }

/* Chart area */
.apanel-chart-wrap {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--border);
}
.apanel-chart-wrap .chart-canvas-wrapper {
  position: relative;
  height: 220px;
}
.apanel-chart-wrap.multi .chart-canvas-wrapper {
  height: 300px;
}
.apanel-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 220px;
  color: var(--text-muted);
  font-size: 0.85rem;
}

/* Summary line */
.apanel-summary {
  padding: 0.5rem 1rem;
  font-size: 0.8rem;
  color: var(--text-muted);
  font-style: italic;
  border-bottom: 1px solid var(--border);
}

/* Representative rows */
.apanel-repr {
  padding: 0.75rem 1rem;
}
.apanel-repr h4 {
  font-size: 0.85rem;
  margin-bottom: 0.5rem;
  color: var(--text-muted);
}
.apanel-repr table {
  font-size: 0.75rem;
  width: 100%;
}
.apanel-repr th {
  padding: 0.25rem 0.5rem;
  font-weight: 600;
  color: var(--text-muted);
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.apanel-repr td {
  padding: 0.25rem 0.5rem;
  border-bottom: 1px solid var(--border);
}
.apanel-repr tr:hover td {
  background: var(--surface-2);
}
.apanel-repr .row-idx {
  color: var(--text-muted);
  cursor: pointer;
  text-decoration: underline;
  text-decoration-style: dotted;
}
.apanel-repr .null-val {
  color: var(--text-muted);
  font-style: italic;
}

/* Multi-column mode */
.apanel-col-list {
  padding: 0.5rem 1rem;
  border-bottom: 1px solid var(--border);
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}
.apanel-col-tag {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-size: 0.75rem;
  background: var(--surface-2);
  padding: 0.15rem 0.5rem;
  border-radius: 0.25rem;
  cursor: pointer;
  user-select: none;
}
.apanel-col-tag.active {
  background: var(--primary);
  color: white;
}
.apanel-col-tag .remove-col {
  cursor: pointer;
  color: inherit;
  opacity: 0.6;
}
.apanel-col-tag .remove-col:hover { opacity: 1; }

/* Correlation matrix */
.apanel-corr {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--border);
}
.apanel-corr h4 {
  font-size: 0.85rem;
  margin-bottom: 0.5rem;
  color: var(--text-muted);
}
.corr-grid {
  display: grid;
  font-size: 0.75rem;
  font-family: monospace;
}
.corr-cell {
  padding: 0.3rem 0.5rem;
  text-align: center;
  border-radius: 0.25rem;
}
.corr-cell.header {
  font-weight: 600;
  color: var(--text);
  background: transparent;
}
.corr-cell.diag {
  font-weight: 600;
  color: var(--text);
  background: var(--surface-2);
}
.corr-cell.pos { color: #fff; }
.corr-cell.neg { color: #fff; }
.corr-cell.neutral { color: var(--text-muted); background: transparent; }
.corr-cell.na { color: var(--text-muted); background: transparent; font-style: italic; }
```

- [ ] **Step 3: Add Chart.js color palette and helper vars**

Append to the end of the inline `<script>` in browse.html (before the `</script>` tag), or add to the top of the new analytics section:

```javascript
// --- Analytics Panel State ---
var panelState = {
  selectedCols: [],
  analysisCache: null  // cached /analyze response
};
var PANEL_COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6'];
```

- [ ] **Step 4: Commit**

```bash
git add internal/web/templates/browse.html internal/web/static/style.css
git commit -m "feat: add analytics panel container, backdrop, and styles"
```

---

### Task 2: Implement panel open/close and column header click handlers

**Files:**
- Modify: `internal/web/templates/browse.html` — add panel JS functions

- [ ] **Step 1: Add click handlers on column headers in renderPage()**

In `browse.html`, find the `renderPage()` function where headers are rendered (around line 240):
```javascript
d.columns.forEach(function(c) {
  h += '<th onclick="openAnalyticsPanel(\'' + escAttr(c.name) + '\',' + /* index */ + ')" style="cursor:pointer;">' + c.name + '<br><span style="font-weight:400;color:var(--text-muted);font-size:0.75rem;">' + c.type + '</span></th>';
});
```

Replace that block with:

```javascript
d.columns.forEach(function(c, ci) {
  h += '<th onclick="openAnalyticsPanel(\'' + escAttr(c.name) + '\',' + ci + ')" style="cursor:pointer;user-select:none;" title="Click for column analytics">' + c.name + '<br><span style="font-weight:400;color:var(--text-muted);font-size:0.75rem;">' + c.type + '</span></th>';
});
```

- [ ] **Step 2: Add panel open/close functions**

Add after the `escHtml`, `escJs`, `escAttr` helper functions (around line 420) and before the keyboard listener:

```javascript
function openAnalyticsPanel(colName, colIdx) {
  if (!lastResult || !lastResult.columns) return té;
  var isMulti = window.event && window.event.ctrlKey;

  if (!isMulti) {
    panelState.selectedCols = [{name: colName, idx: colIdx}];
  } else {
    var existing = panelState.selectedCols.findIndex(function(c) { return c.name === colName; });
    if (existing >= 0) {
      panelState.selectedCols.splice(existing, 1);
      if (!panelState.selectedCols.length) { closeAnalyticsPanel(); return; }
    } else {
      panelState.selectedCols.push({name: colName, idx: colIdx});
    }
  }

  renderAnalyticsPanel();
  document.getElementById('analytics-backdrop').classList.add('visible');
  document.getElementById('analytics-panel').classList.add('open');

  // Fetch server analysis if not cached
  if (!panelState.analysisCache) {
    fetchAnalysis();
  }
}

function closeAnalyticsPanel() {
  document.getElementById('analytics-backdrop').classList.remove('visible');
  document.getElementById('analytics-panel').classList.remove('open');
  panelState.selectedCols = [];
}

function fetchAnalysis() {
  if (!lastResult) return;
  var statusEl = document.getElementById('query-status');
  var chartWrap = document.getElementById('apanel-chart');
  if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading">Loading histogram...</div>';

  fetch('/analyze', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      columns: lastResult.columns,
      rows: lastResult.rows
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) {
      if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading" style="color:var(--red);">Analysis error: ' + escHtml(d.error) + '</div>';
      return;
    }
    panelState.analysisCache = d;
    renderAnalyticsPanel();
  })
  .catch(function(e) {
    if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading" style="color:var(--red);">Error: ' + e.message + '</div>';
  });
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add column header click handler and panel open/close logic"
```

---

### Task 3: Implement quick stats computation (client-side)

**Files:**
- Modify: `internal/web/templates/browse.html` — add computeQuickStats function

- [ ] **Step 1: Add computeQuickStats function**

Add after `closeAnalyticsPanel`:

```javascript
function computeQuickStats(colIdx) {
  var rows = lastResult.rows;
  var total = rows.length;
  var nullCount = 0;
  var distinct = new Set();
  var sum = 0;
  var numericCount = 0;
  var min = Infinity;
  var max = -Infinity;
  var topFreq = {};

  for (var i = 0; i < rows.length; i++) {
    var v = rows[i][colIdx];
    if (v === null || v === undefined) {
      nullCount++;
      continue;
    }
    var sv = String(v);
    distinct.add(sv);
    topFreq[sv] = (topFreq[sv] || 0) + 1;

    if (typeof v === 'number') {
      sum += v;
      numericCount++;
      if (v < min) min = v;
      if (v > max) max = v;
    } else if (!isNaN(parseFloat(v))) {
      var n = parseFloat(v);
      sum += n;
      numericCount++;
      if (n < min) min = n;
      if (n > max) max = n;
    }
  }

  // Top 3 values
  var topEntries = Object.entries(topFreq).sort(function(a,b) { return b[1] - a[1]; }).slice(0, 3);
  var topVals = topEntries.map(function(e) { return e[0] + ' (' + e[1] + ')'; });

  return {
    total: total,
    nullCount: nullCount,
    nullPct: total > 0 ? (nullCount / total * 100).toFixed(1) : '0.0',
    distinct: distinct.size,
    isNumeric: numericCount > 0,
    min: numericCount > 0 ? min : null,
    max: numericCount > 0 ? max : null,
    mean: numericCount > 0 ? (sum / numericCount).toFixed(2) : null,
    topValues: topVals
  };
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add client-side quick stats computation"
```

---

### Task 4: Implement representative row selection

**Files:**
- Modify: `internal/web/templates/browse.html` — add selectRepresentativeRows function

- [ ] **Step 1: Add selectRepresentativeRows function**

Add after `computeQuickStats`:

```javascript
function selectRepresentativeRows(colIdx, maxRows) {
  maxRows = maxRows || 8;
  var rows = lastResult.rows;
  var indices = [];
  var selected = [];
  var used = new Set();

  // 1. Null rows (up to 3)
  for (var i = 0; i < rows.length && selected.length < maxRows; i++) {
    if (rows[i][colIdx] === null || rows[i][colIdx] === undefined) {
      if (!used.has(i)) { used.add(i); selected.push(i); }
    }
  }

  // 2. Outliers for numeric columns (z-score > 2)
  var vals = [];
  for (var i = 0; i < rows.length; i++) {
    var v = rows[i][colIdx];
    if (v !== null && v !== undefined && !isNaN(parseFloat(v))) {
      vals.push({idx: i, val: parseFloat(v)});
    }
  }
  if (vals.length > 3) {
    var sum = vals.reduce(function(s, x) { return s + x.val; }, 0);
    var mean = sum / vals.length;
    var sqDiff = vals.reduce(function(s, x) { return s + (x.val - mean) * (x.val - mean); }, 0);
    var stddev = Math.sqrt(sqDiff / vals.length);
    if (stddev > 0) {
      for (var i = 0; i < vals.length && selected.length < maxRows; i++) {
        var z = Math.abs((vals[i].val - mean) / stddev);
        if (z > 2 && !used.has(vals[i].idx)) {
          used.add(vals[i].idx);
          selected.push(vals[i].idx);
        }
      }
    }
  }

  // 3. Random fill
  var remaining = [];
  for (var i = 0; i < rows.length; i++) {
    if (!used.has(i)) remaining.push(i);
  }
  // Shuffle
  for (var i = remaining.length - 1; i > 0; i--) {
    var j = Math.floor(Math.random() * (i + 1));
    var tmp = remaining[i]; remaining[i] = remaining[j]; remaining[j] = tmp;
  }
  for (var i = 0; i < remaining.length && selected.length < maxRows; i++) {
    selected.push(remaining[i]);
  }

  return selected.sort(function(a,b) { return a - b; });
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add representative row selection with nulls, outliers, random sample"
```

---

### Task 5: Implement panel renderer — single column mode with chart + representative rows

**Files:**
- Modify: `internal/web/templates/browse.html` — add renderAnalyticsPanel function

- [ ] **Step 1: Add renderAnalyticsPanel function**

Add after `selectRepresentativeRows`:

```javascript
function renderAnalyticsPanel() {
  var content = document.getElementById('analytics-panel-content');
  if (!content || !panelState.selectedCols.length) return;

  var isMulti = panelState.selectedCols.length > 1;

  var html = '';

  if (isMulti) {
    html += renderMultiColumnPanel();
  } else {
    html += renderSingleColumnPanel();
  }

  content.innerHTML = html;
  renderPanelCharts();
}

function renderSingleColumnPanel() {
  var col = panelState.selectedCols[0];
  var colDef = lastResult.columns[col.idx];
  var stats = computeQuickStats(col.idx);
  var reprRows = selectRepresentativeRows(col.idx);
  var analysis = panelState.analysisCache ? panelState.analysisCache.columns[col.name] : null;

  var html = '';

  // Header
  html += '<div class="apanel-header">';
  html += '<h3>' + escHtml(col.name) + ' <span class="type-badge">' + escHtml(colDef.type) + '</span></h3>';
  html += '<button class="apanel-close" onclick="closeAnalyticsPanel()">\u00D7</button>';
  html += '</div>';

  // Quick stats
  html += '<div class="apanel-stats">';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Rows:</span> <span class="apanel-stat-value">' + stats.total + '</span></span>';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Null:</span> <span class="apanel-stat-value null">' + stats.nullCount + ' (' + stats.nullPct + '%)</span></span>';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Distinct:</span> <span class="apanel-stat-value distinct">' + stats.distinct + '</span></span>';
  if (stats.isNumeric) {
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Min:</span> <span class="apanel-stat-value">' + (stats.min !== null ? stats.min.toFixed(2) : '—') + '</span></span>';
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Max:</span> <span class="apanel-stat-value">' + (stats.max !== null ? stats.max.toFixed(2) : '—') + '</span></span>';
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Mean:</span> <span class="apanel-stat-value">' + (stats.mean || '—') + '</span></span>';
  }
  if (stats.topValues.length) {
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Top:</span> <span class="apanel-stat-value" style="font-weight:400;font-size:0.75rem;">' + escHtml(stats.topValues.join(', ')) + '</span></span>';
  }
  html += '</div>';

  // Chart area
  html += '<div id="apanel-chart" class="apanel-chart-wrap">';
  if (analysis) {
    html += '<div class="chart-canvas-wrapper"><canvas id="apanel-canvas"></canvas></div>';
  } else {
    html += '<div class="apanel-loading">Loading histogram...</div>';
  }
  html += '</div>';

  // Summary line
  if (analysis && panelState.analysisCache) {
    var summary = findColumnSummary(col.name);
    if (summary) {
      html += '<div class="apanel-summary">' + escHtml(summary) + '</div>';
    }
  }

  // Representative rows
  html += '<div class="apanel-repr">';
  html += '<h4>Representative Rows</h4>';

  // Determine neighbor columns
  var leftIdx = col.idx > 0 ? col.idx - 1 : (col.idx + 1 < lastResult.columns.length ? col.idx + 1 : -1);
  var rightIdx = col.idx + 1 < lastResult.columns.length ? col.idx + 1 : (col.idx > 0 ? col.idx - 1 : -1);
  // Ensure left/right are different and not same as col
  if (leftIdx === col.idx) leftIdx = -1;
  if (rightIdx === col.idx) rightIdx = -1;
  if (leftIdx === rightIdx) rightIdx = -1;

  html += '<table><thead><tr><th>#</th>';
  if (leftIdx >= 0) html += '<th>' + escHtml(lastResult.columns[leftIdx].name) + '</th>';
  html += '<th>' + escHtml(col.name) + '</th>';
  if (rightIdx >= 0) html += '<th>' + escHtml(lastResult.columns[rightIdx].name) + '</th>';
  html += '</tr></thead><tbody>';

  for (var ri = 0; ri < reprRows.length; ri++) {
    var rowIdx = reprRows[ri];
    html += '<tr>';
    html += '<td><span class="row-idx" onclick="scrollToRow(' + rowIdx + ')">' + (rowIdx + 1) + '</span></td>';
    if (leftIdx >= 0) html += '<td>' + formatCell(lastResult.rows[rowIdx][leftIdx]) + '</td>';
    html += '<td>' + formatCell(lastResult.rows[rowIdx][col.idx]) + '</td>';
    if (rightIdx >= 0) html += '<td>' + formatCell(lastResult.rows[rowIdx][rightIdx]) + '</td>';
    html += '</tr>';
  }
  html += '</tbody></table>';
  html += '</div>';

  return html;
}

function formatCell(val) {
  if (val === null || val === undefined) return '<span class="null-val">NULL</span>';
  return escHtml(String(val));
}

function scrollToRow(rowIdx) {
  var table = document.querySelector('#query-results table');
  if (!table) return;
  var rows = table.querySelectorAll('tbody tr');
  if (rows[rowIdx]) {
    rows[rowIdx].scrollIntoView({block: 'center', behavior: 'smooth'});
    rows[rowIdx].style.background = 'var(--primary)';
    rows[rowIdx].style.color = '#fff';
    setTimeout(function() {
      rows[rowIdx].style.background = '';
      rows[rowIdx].style.color = '';
    }, 2000);
  }
}

function findColumnSummary(colName) {
  if (!panelState.analysisCache || !panelState.analysisCache.summary) return null;
  for (var i = 0; i < panelState.analysisCache.summary.length; i++) {
    if (panelState.analysisCache.summary[i].startsWith(colName + ' ')) {
      return panelState.analysisCache.summary[i];
    }
    if (panelState.analysisCache.summary[i].startsWith(colName + ':')) {
      return panelState.analysisCache.summary[i];
    }
  }
  return null;
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add single-column panel renderer with stats, chart, representative rows"
```

---

### Task 6: Implement chart rendering (single column — histogram, bar, boolean, temporal)

**Files:**
- Modify: `internal/web/templates/browse.html` — add renderPanelCharts function

- [ ] **Step 1: Add renderPanelCharts function**

Add after `findColumnSummary`:

```javascript
function renderPanelCharts() {
  if (!panelState.analysisCache) return;

  if (panelState.selectedCols.length === 1) {
    renderSingleColumnChart();
  } else {
    renderMultiColumnCharts();
  }
}

function getColType(colName) {
  for (var i = 0; i < lastResult.columns.length; i++) {
    if (lastResult.columns[i].name === colName) return lastResult.columns[i].type;
  }
  return 'VARCHAR';
}

function renderSingleColumnChart() {
  var canvas = document.getElementById('apanel-canvas');
  if (!canvas) return;

  var col = panelState.selectedCols[0];
  var analysis = panelState.analysisCache.columns[col.name];
  if (!analysis) {
    var ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    return;
  }

  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }

  var chartConfig = buildSingleChartConfig(analysis, col);
  if (chartConfig) {
    canvas._chart = new Chart(ctx, chartConfig);
  }
}

function buildSingleChartConfig(analysis, col) {
  var colType = getColType(col.name);
  var isNumeric = /INT|FLOAT|DOUBLE|DECIMAL|NUMERIC/.test(colType.toUpperCase());
  var isTemporal = /TIMESTAMP|DATE|TIME/.test(colType.toUpperCase());
  var isBoolean = /BOOL/.test(colType.toUpperCase());

  if (isBoolean || analysis.type === 'boolean') {
    return buildBooleanChart(analysis, col);
  }
  if (isNumeric || analysis.type === 'numeric') {
    if (analysis.histogram && analysis.histogram.length) {
      return buildHistogramChart(analysis, col);
    }
    return buildNumericStatsChart(analysis, col);
  }
  if (isTemporal || analysis.type === 'temporal') {
    return buildTemporalChart(analysis, col);
  }
  // Default: categorical
  if (analysis.top_values && analysis.top_values.length) {
    return buildTopValuesChart(analysis, col);
  }
  return null;
}

function buildHistogramChart(analysis, col) {
  var bins = analysis.histogram;
  var labels = bins.map(function(b) {
    var s = b.bin_start.toFixed(1);
    var e = b.bin_end.toFixed(1);
    return s + '-' + e;
  });
  var data = bins.map(function(b) { return b.count; });
  var stats = analysis.stats || {};

  return {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Count',
        data: data,
        backgroundColor: 'rgba(0,101,255,0.6)',
        borderColor: 'rgba(0,101,255,1)',
        borderWidth: 1
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            afterLabel: function(ctx) {
              var bin = bins[ctx.dataIndex];
              return 'Range: ' + bin.bin_start.toFixed(2) + ' - ' + bin.bin_end.toFixed(2);
            }
          }
        }
      },
      scales: {
        x: {
          ticks: { color: '#9099A1', font: { size: 9 }, maxRotation: 45 },
          grid: { color: '#31393F' }
        },
        y: {
          ticks: { color: '#9099A1', font: { size: 9 } },
          grid: { color: '#31393F' },
          beginAtZero: true
        }
      }
    }
  };
}

function buildTopValuesChart(analysis, col) {
  var topVals = analysis.top_values;
  if (!topVals || !topVals.length) return null;

  // Limit to top 10
  topVals = topVals.slice(0, 10.);

  var labels = topVals.map(function(v) { return v.value; });
  var data = topVals.map(function(v) { return v.count; });
  var pcts = topVals.map(function(v) { return v.pct.toFixed(1); });

  return {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Frequency',
        data: data,
        backgroundColor: PANEL_COLORS.slice(0, labels.length),
        borderWidth: 0
      }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: function(ctx) {
              return ctx.parsed.x + ' (' + pcts[ctx.dataIndex] + '%)';
            }
          }
        }
      },
      scales: {
        x: {
          ticks: { color: '#9099A1', font: { size: 9 } },
          grid: { color: '#31393F' },
          beginAtZero: true
        },
        y: {
          ticks: { color: '#9099A1', font: { size: 9 } },
          grid: { display: false }
        }
      }
    }
  };
}

function buildBooleanChart(analysis, col) {
  var stats = analysis.stats || {};
  var trueCount = stats.true_count || 0;
  var falseCount = (stats.count || 0) - trueCount - (stats.null_count || 0);
  var nullCount = stats.null_count || 0;

  return {
    type: 'bar',
    data: {
      labels: ['Boolean'],
      datasets: [
        {
          label: 'True (' + (stats.true_pct || 0).toFixed(1) + '%)',
          data: [trueCount],
          backgroundColor: '#27B681'
        },
        {
          label: 'False (' + ((falseCount / (stats.count || 1) * 100) || 0).toFixed(1) + '%)',
          data: [falseCount],
          backgroundColor: '#f87171'
        },
        {
          label: 'Null (' + ((nullCount / (stats.count || 1) * 100) || 0).toFixed(1) + '%)',
          data: [nullCount],
          backgroundColor: '#596773'
        }
      ]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { labels: { color: '#DEE4EA', font: { size: 9 } } } },
      scales: {
        x: { stacked: true, ticks: { color: '#9099A1' }, grid: { color: '#31393F' }, beginAtZero: true },
        y: { stacked: true, ticks: { color: '#9099A1' }, grid: { display: false } }
      }
    }
  };
}

function buildNumericStatsChart(analysis, col) {
  var stats = analysis.stats || {};
  // Show mini summary as a simple bar chart of mean +/- stddev
  var mean = stats.mean || 0;
  var stddev = stats.stddev || 0;
  var min = stats.min || 0;
  var max = stats.max || 0;

  return {
    type: 'bar',
    data: {
      labels: ['Min', 'Mean\u00B1\u03C3', 'Max'],
      datasets: [{
        label: 'Value',
        data: [min, mean, max],
        backgroundColor: ['#8739B1', '#0065FF', '#27B681']
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            afterLabel: function(ctx) {
              if (ctx.dataIndex === 1) return '\u03C3 = ' + stddev.toFixed(2);
              return '';
            }
          }
        }
      },
      scales: {
        x: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { display: false } },
        y: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { color: '#31393F' } }
      }
    }
  };
}

function buildTemporalChart(analysis, col) {
  // Use histogram if available (binned by time), otherwise show min/max
  if (analysis.histogram && analysis.histogram.length) {
    return buildHistogramChart(analysis, col);
  }
  var stats = analysis.stats || {};
  return {
    type: 'bar',
    data: {
      labels: ['Min', 'Max'],
      datasets: [{
        label: 'Date',
        data: [1, 1],
        backgroundColor: ['#0065FF', '#27B681']
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: function(ctx) {
              return ctx.dataIndex === 0 ? 'Min: ' + (stats.min || '') : 'Max: ' + (stats.max || '');
            }
          }
        }
      },
      scales: {
        x: { ticks: { color: '#9099A1' }, grid: { display: false } },
        y: { display: false }
      }
    }
  };
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add chart rendering for histogram, top-values, boolean, temporal, numeric"
```

---

### Task 7: Implement multi-column mode — overlaid distributions + correlation matrix

**Files:**
- Modify: `internal/web/templates/browse.html` — add renderMultiColumnPanel and renderMultiColumnCharts

- [ ] **Step 1: Add renderMultiColumnPanel function**

Add before `renderSingleColumnPanel`:

```javascript
function renderMultiColumnPanel() {
  var html = '';

  // Header
  html += '<div class="apanel-header">';
  var names = panelState.selectedCols.map(function(c) { return c.name; });
  html += '<h3>' + names.length + ' columns selected</h3>';
  html += '<button class="apanel-close" onclick="closeAnalyticsPanel()">\u00D7</button>';
  html += '</div>';

  // Column tag selector
  html += '<div class="apanel-col-list">';
  for (var i = 0; i < panelState.selectedCols.length; i++) {
    var c = panelState.selectedCols[i];
    html += '<span class="apanel-col-tag active" onclick="toggleMultiCol(' + i + ')">' + escHtml(c.name) + ' <span class="remove-col" onclick="event.stopPropagation(); removeMultiCol(' + i + ')">\u00D7</span></span>';
  }
  html += '</div>';

  // Overlaid distributions chart
  html += '<div id="apanel-chart" class="apanel-chart-wrap multi">';
  if (panelState.analysisCache) {
    var hasData = false;
    for (var i = 0; i < panelState.selectedCols.length; i++) {
      if (panelState.analysisCache.columns[panelState.selectedCols[i].name]) hasData = true;
    }
    if (hasData) {
      html += '<div class="chart-canvas-wrapper"><canvas id="apanel-canvas-multi"></canvas></div>';
    } else {
      html += '<div class="apanel-loading">Loading overlaid distributions...</div>';
    }
  } else {
    html += '<div class="apanel-loading">Loading overlaid distributions...</div>';
  }
  html += '</div>';

  // Correlation matrix
  var numCols = panelState.selectedCols.filter(function(c) {
    var t = getColType(c.name).toUpperCase();
    return /INT|FLOAT|DOUBLE|DECIMAL|NUMERIC/.test(t);
  });
  if (numCols.length >= 2 && panelState.analysisCache && panelState.analysisCache.correlations) {
    html += '<div class="apanel-corr">';
    html += '<h4>Correlation Matrix (Pearson)</h4>';
    html += buildCorrelationMatrix(numCols);
    html += '</div>';
  }

  return html;
}

function toggleMultiCol(idx) {
  // Toggle visibility of a column in multi-mode (re-render chart)
  // We track active status via CSS class; re-render chart
  renderMultiColumnCharts();
}

function removeMultiCol(idx) {
  panelState.selectedCols.splice(idx, 1);
  if (panelState.selectedCols.length <= 1) {
    if (panelState.selectedCols.length === 1) {
      renderAnalyticsPanel();
    } else {
      closeAnalyticsPanel();
    }
    return;
  }
  renderAnalyticsPanel();
}

function buildCorrelationMatrix(cols) {
  var names = cols.map(function(c) { return c.name; });
  var n = names.length;
  var html = '<div class="corr-grid" style="grid-template-columns:auto repeat(' + n + ', 1fr);gap:2px;">';

  // Header row
  html += '<div class="corr-cell header"></div>';
  for (var j = 0; j < n; j++) {
    html += '<div class="corr-cell header" style="font-size:0.7rem;">' + escHtml(names[j]) + '</div>';
  }

  // Data rows
  for (var i = 0; i < n; i++) {
    html += '<div class="corr-cell header" style="font-size:0.7rem;">' + escHtml(names[i]) + '</div>';
    for (var j = 0; j < n; j++) {
      if (i === j) {
        html += '<div class="corr-cell diag">1.00</div>';
      } else {
        var corr = findCorrelation(names[i], names[j]);
        if (corr !== null) {
          var cls = corr > 0.1 ? 'pos' : (corr < -0.1 ? 'neg' : 'neutral');
          var intensity = Math.min(Math.abs(corr) * 0.8 + 0.1, 0.9);
          var bg = '';
          if (corr > 0.1) {
            bg = 'background:rgba(0,101,255,' + intensity + ');';
          } else if (corr < -0.1) {
            bg = 'background:rgba(248,113,113,' + intensity + ');';
          }
          html += '<div class="corr-cell ' + cls + '" style="' + bg + '">' + corr.toFixed(2) + '</div>';
        } else {
          html += '<div class="corr-cell na">\u2014</div>';
        }
      }
    }
  }

  html += '</div>';
  return html;
}

function findCorrelation(colA, colB) {
  if (!panelState.analysisCache || !panelState.analysisCache.correlations) return null;
  for (var i = 0; i < panelState.analysisCache.correlations.length; i++) {
    var c = panelState.analysisCache.correlations[i];
    if ((c.col_a === colA && c.col_b === colB) || (c.col_a === colB && c.col_b === colA)) {
      return c.value;
    }
  }
  return null;
}
```

- [ ] **Step 2: Add renderMultiColumnCharts function**

Add after `buildSingleChartConfig`:

```javascript
function renderMultiColumnCharts() {
  var canvas = document.getElementById('apanel-canvas-multi');
  if (!canvas || !panelState.analysisCache) return;

  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }

  var activeCols = panelState.selectedCols;
  var datasets = [];
  var allLabels = [];

  // Determine if all selected cols are numeric (for histograms) or mixed
  var allNumeric = activeCols.every(function(c) {
    var t = getColType(c.name).toUpperCase();
    return /INT|FLOAT|DOUBLE|DECIMAL|NUMERIC/.test(t);
  });

  if (allNumeric) {
    // Overlaid normalized histograms
    var allBins = [];
    var maxCount = 0;
    for (var i = 0; i < activeCols.length; i++) {
      var analysis = panelState.analysisCache.columns[activeCols[i].name];
      if (!analysis || !analysis.histogram || !analysis.histogram.length) continue;
      var bins = analysis.histogram;
      var total = bins.reduce(function(s, b) { return s + b.count; }, 0);
      var normalized = bins.map(function(b) { return total > 0 ? b.count / total : 0; });
      var labels = bins.map(function(b) { return b.bin_start.toFixed(1) + '-' + b.bin_end.toFixed(1); });
      if (labels.length > allLabels.length) allLabels = labels;
      allBins.push({name: activeCols[i].name, data: normalized, bins: bins});
      normalized.forEach(function(v) { if (v > maxCount) maxCount = v; });
    }

    datasets = allBins.map(function(b, i) {
      // Pad data to match allLabels length
      var padded = new Array(allLabels.length).fill(0);
      for (var j = 0; j < b.data.length && j < allLabels.length; j++) {
        padded[j] = b.data[j];
      }
      return {
        label: b.name,
        data: padded,
        backgroundColor: PANEL_COLORS[i % PANEL_COLORS.length] + '66',
        borderColor: PANEL_COLORS[i % PANEL_COLORS.length],
        borderWidth: 1,
        fill: true,
        tension: 0.3,
        pointRadius: 0
      };
    });

    canvas._chart = new Chart(ctx, {
      type: 'line',
      data: { labels: allLabels, datasets: datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { labels: { color: '#DEE4EA', font: { size: 9 } } },
          tooltip: {
            callbacks: {
              label: function(ctx) {
                return ctx.dataset.label + ': ' + (ctx.parsed.y * 100).toFixed(1) + '%';
              }
            }
          }
        },
        scales: {
          x: { ticks: { color: '#9099A1', font: { size: 8 } }, grid: { color: '#31393F' } },
          y: {
            ticks: { color: '#9099A1', font: { size: 8 }, callback: function(v) { return (v * 100).toFixed(0) + '%'; } },
            grid: { color: '#31393F' },
            beginAtZero: true
          }
        },
        elements: { point: { radius: 0 } }
      }
    });
  } else {
    // Mixed or categorical: grouped bar chart
    var categories = {};
    for (var i = 0; i < activeCols.length; i++) {
      var analysis = panelState.analysisCache.columns[activeCols[i].name];
      if (!analysis || !analysis.top_values) continue;
      var topVals = analysis.top_values.slice(0, 5);
      topVals.forEach(function(v) {
        if (!categories[v.value]) categories[v.value] = {};
        categories[v.value][activeCols[i].name] = v.count;
      });
    }

    allLabels = Object.keys(categories).slice(0, 10);
    datasets = activeCols.map(function(c, i) {
      return {
        label: c.name,
        data: allLabels.map(function(l) { return categories[l] && categories[l][c.name] ? categories[l][c.name] : 0; }),
        backgroundColor: PANEL_COLORS[i % PANEL_COLORS.length]
      };
    });

    canvas._chart = new Chart(ctx, {
      type: 'bar',
      data: { labels: allLabels, datasets: datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: { legend: { labels: { color: '#DEE4EA', font: { size: 9 } } } },
        scales: {
          x: { ticks: { color: '#9099A1', font: { size: 8 } }, grid: { color: '#31393F' } },
          y: { ticks: { color: '#9099A1', font: { size: 8 } }, grid: { color: '#31393F' }, beginAtZero: true }
        }
      }
    });
  }
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add multi-column mode with overlaid distributions and correlation matrix"
```

---

### Task 8: Extend column config preview with labeled columns and inline distribution bars

**Files:**
- Modify: `internal/web/static/column_config.js` — enhance preview with column labels and inline distribution

- [ ] **Step 1: Update updateDelimiterPreview to add column headers and inline bars**

Replace the `updateDelimiterPreview` function in `column_config.js`:

```javascript
function updateDelimiterPreview(container) {
  var delim = currentConfig.delimiter;
  var numCols = cachedPreviewLines.length > 0 ? cachedPreviewLines[0].split(delim).length : 0 nations;

  // Sync columns
  if (currentConfig.columns.length !== numCols) {
    currentConfig.columns = [];
    for (var i = 0; i < numCols; i++) {
      currentConfig.columns.push({name: 'col' + i, type: 'VARCHAR'});
    }
  }

  // Compute inline distribution data from preview lines
  var distData = [];
  for (var ci = 0; ci < numCols; ci++) {
    var freq = {};
    var total = 0;
    cachedPreviewLines.forEach(function(line) {
      var cells = line.split(delim);
      if (cells[ci] !== undefined) {
        var v = cells[ci].trim();
        freq[v] = (freq[v] || 0) + 1;
        total++;
      }
    });
    var entries = Object.entries(freq).sort(function(a,b) { return b[1] - a[1]; }).slice(0, 5);
    var topTotal = entries.reduce(function(s, e) { return s + e[1]; }, 0);
    distData.push({entries: entries, total: total, topPct: total > 0 ? topTotal / total * 100 : 0});
  }

  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';

  // Column headers with inline distribution bars
  html += '<div style="display:flex;gap:2px;margin-bottom:0.25rem;">';
  for (var ci = 0; ci < numCols; ci++) {
    var col = currentConfig.columns[ci];
    html += '<div style="flex:1;min-width:80px;padding:0 0.25rem;">';
    // Column name + type badge
    html += '<div style="font-size:0.7rem;font-weight:600;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + escHtml(col.name) + ' <span style="color:var(--text-muted);font-weight:400;">' + col.type + '</span></div>';
    // Distribution bar
    if (distData[ci] && distData[ci].entries.length) {
      html += '<div style="display:flex;gap:1px;height:6px;margin-top:2px;border-radius:2px;overflow:hidden;">';
      distData[ci].entries.forEach(function(e) {
        var pct = distData[ci].total > 0 ? (e[1] / distData[ci].total * 100) : 0;
        html += '<div style="height:100%;width:' + pct + '%;background:' + PANEL_COLORS[ci % PANEL_COLORS.length] + ';" title="' + escAttr(e[0]) + ': ' + e[1] + '"></div>';
      });
      html += '</div>';
    }
    html += '</div>';
  }
  html += '</div>';

  // Data rows
  cachedPreviewLines.forEach(function(line) {
    var cells = line.split(delim);
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    cells.forEach(function(cell, ci) {
      html += '<span style="flex:1;min-width:80px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
    });
    html += '</div>';
  });
  html += '</div>';

  // Column editor below
  html += '<div style="margin-top:0.75rem;"><h4 style="font-size:0.85rem;margin-bottom:0.5rem;">Column Names & Types</h4>';
  html += '<div style="display:flex;gap:0.5rem;flex-wrap:wrap;">';
  currentConfig.columns.forEach(function(col, idx) {
    html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
    html += '<input type="text" value="' + escHtml(col.name) + '" style="width:100px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="updateColName(' + idx + ', this.value)">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="updateColType(' + idx + ', this.value)">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    html += '</div>';
  });
  html += '</div></div>';

  container.innerHTML = html;
}
```

- [ ] **Step 2: Add PANEL_COLORS to column_config.js if not present**

Find near the top of `column_config.js` after `escHtml` and add:

```javascript
var PANEL_COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6'];
```

- [ ] **Step 3: Similarly update updateFixedWidthPreview**

Replace `updateFixedWidthPreview` to add column name headers above each column:

```javascript
function updateFixedWidthPreview(container) {
  if (!currentConfig.columns.length || currentConfig.columns[0].start === undefined) {
    container.innerHTML = '<p style="color:var(--text-muted);">Click on the preview line above to define column positions.</p>';
    return;
  }

  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';

  // Column headers
  html += '<div style="display:flex;gap:2px;margin-bottom:0.25rem;">';
  for (var ci = 0; ci < currentConfig.columns.length; ci++) {
    var col = currentConfig.columns[ci];
    html += '<div style="flex:1;min-width:60px;padding:0 0.25rem;">';
    html += '<div style="font-size:0.7rem;font-weight:600;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + escHtml(col.name) + ' <span style="color:var(--text-muted);font-weight:400;">' + col.type + '</span></div>';
    html += '</div>';
  }
  html += '</div>';

  // Data rows
  cachedPreviewLines.forEach(function(line) {
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    for (var i = 0; i < currentConfig.columns.length; i++) {
      var col = currentConfig.columns[i];
      var start = col.start !== undefined && col.start !== null ? col.start : 0;
      var end = col.end !== undefined && col.end !== null ? col.end : line.length;
      var cell = line.slice(start, end);
      html += '<span style="flex:1;min-width:60px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
    }
    html += '</div>';
  });
  html += '</div>';
  container.innerHTML = html;
}
```

- [ ] **Step 4: Commit**

```bash
git add internal/web/static/column_config.js
git commit -m "feat: add column name headers and inline distribution bars to config preview"
```

---

### Task 9: Add keyboard shortcut and UI polish

**Files:**
- Modify: `internal/web/templates/browse.html` — add Escape key to close panel

- [ ] **Step 1: Add Escape key handler**

Add near the existing keyboard listener (around line 423):

```javascript
document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    closeAnalyticsPanel();
  }
});
```

- [ ] **Step 2: Commit**

```bash
git add internal/web/templates/browse.html
git commit -m "feat: add Escape key to close analytics panel"
```

---

### Self-Review Checklist

**1. Spec coverage:**
- Click column header → panel opens: Task 2
- Quick stats (client-side): Task 3
- Distribution charts (histogram, bar, boolean, temporal): Task 6
- Representative rows: Task 4
- Multi-column mode (overlaid distributions + correlation matrix): Task 7
- Column config preview labels + inline bars: Task 8
- Escape key to close: Task 9
- Panel slides in from right: Task 1 (CSS)

**2. Placeholder scan:** No TBD, TODOs, or vague steps. All code is complete.

**3. Type consistency:** `panelState.selectedCols` array of `{name, idx}` objects used consistently across all functions. `PANEL_COLORS` defined once and referenced in column config as well. Function names match across call sites.
