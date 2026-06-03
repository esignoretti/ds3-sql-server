// Analytics Panel State
var panelState = {
  selectedCols: [],
  analysisCache: null,
  fetchingAnalysis: false,
  activeMultiCols: {}
};
var PANEL_COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6'];

function runQuery() {
  var sql = document.getElementById('sql-editor').value;
  if (!sql) { alert('Write a query or click Build SQL'); return; }
  if (!tabState.browse.project) { alert('Select a project'); return; }
  var status = document.getElementById('query-status');
  var results = document.getElementById('query-results');
  status.innerHTML = 'Running...';
  results.innerHTML = '';
  document.getElementById('page-controls').style.display = 'none';
  document.getElementById('export-bar').style.display = 'none';
  fetch('/query?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({sql: sql})
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { status.innerHTML = '<span class="error">' + d.error + '</span>'; return; }
    tabState.query.results = d;
    panelState.analysisCache = null;
    panelState.fetchingAnalysis = false;
    tabState.query.currentPage = 0;
    status.innerHTML = d.row_count + ' rows in ' + d.elapsed_ms + 'ms';
    // Update source badge
    var badge = document.getElementById('query-source-badge');
    if (badge && tabState.browse.selectedFiles.length) {
      badge.textContent = tabState.browse.selectedFiles.length + ' file(s)';
    }
    document.getElementById('export-bar').style.display = d.row_count ? 'flex' : 'none';
    if (!d.row_count) { results.innerHTML = '<p style="color:var(--text-muted);">No rows</p>'; return; }
    renderPage();
  })
  .catch(function(e) { status.innerHTML = '<span class="error">Error: ' + e.message + '</span>'; });
}

function renderPage() {
  var results = document.getElementById('query-results');
  var d = tabState.query.results;
  if (!d) return;
  var input = document.getElementById('page-size-input');
  var pageSize = parseInt(input.value);
  if (!pageSize || pageSize < 1) pageSize = d.rows.length;
  var totalPages = Math.ceil(d.rows.length / pageSize);
  if (totalPages < 1) totalPages = 1;
  if (tabState.query.currentPage >= totalPages) tabState.query.currentPage = totalPages - 1;
  var start = tabState.query.currentPage * pageSize;
  var end = Math.min(start + pageSize, d.rows.length);

  var h = '<table><thead><tr>';
  d.columns.forEach(function(c, ci) {
    h += '<th onclick="openAnalyticsPanel(\'' + escJs(c.name) + '\',' + ci + ', event)" style="cursor:pointer;user-select:none;" title="Click for column analytics">' + escHtml(c.name) + '<br><span style="font-weight:400;color:var(--text-muted);font-size:0.75rem;">' + escHtml(c.type) + '</span></th>';
  });
  h += '</tr></thead><tbody>';
  for (var i = start; i < end; i++) {
    h += '<tr>';
    d.rows[i].forEach(function(v) { h += '<td>' + (v === null ? '<span style="color:var(--text-muted);font-style:italic;">NULL</span>' : escHtml(String(v))) + '</td>'; });
    h += '</tr>';
  }
  h += '</tbody></table>';
  results.innerHTML = h;

  var ctrl = document.getElementById('page-controls');
  var info = document.getElementById('page-info');
  var prevBtn = document.getElementById('prev-page-btn');
  var nextBtn = document.getElementById('next-page-btn');
  info.textContent = 'Page ' + (tabState.query.currentPage + 1) + '/' + totalPages + ' (' + d.row_count + ' rows)';
  prevBtn.disabled = totalPages <= 1 || tabState.query.currentPage === 0;
  nextBtn.disabled = totalPages <= 1 || tabState.query.currentPage >= totalPages - 1;
  ctrl.style.display = 'flex';
}

function prevPage() {
  if (tabState.query.currentPage > 0) { tabState.query.currentPage--; renderPage(); }
}

function nextPage() {
  var ps = parseInt(document.getElementById('page-size-input').value);
  if (!ps || ps < 1) ps = tabState.query.results.rows.length;
  var totalPages = Math.ceil(tabState.query.results.rows.length / ps);
  if (tabState.query.currentPage < totalPages - 1) { tabState.query.currentPage++; renderPage(); }
}

function clearQuery() {
  document.getElementById('sql-editor').value = '';
  document.getElementById('query-status').innerHTML = '';
  document.getElementById('query-results').innerHTML = '';
  document.getElementById('export-bar').style.display = 'none';
  document.getElementById('page-controls').style.display = 'none';
  tabState.query.results = null;
}

function analyzeResults() {
  if (!tabState.query.results) { alert('Run a query first'); return; }
  var status = document.getElementById('query-status');
  status.innerHTML = 'Analyzing...';
  fetch('/analyze', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      columns: tabState.query.results.columns,
      rows: tabState.query.results.rows
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(analysis) {
    if (analysis.error) { status.innerHTML = '<span class="error">' + analysis.error + '</span>'; return; }
    tabState.analyze.analysisCache = analysis;
    tabState.analyze.selectedCols = tabState.query.results.columns.map(function(c) {
      return {name: c.name, idx: tabState.query.results.columns.indexOf(c)};
    });
    updateTabBadges();
    switchTab('analyze');
  })
  .catch(function(e) { status.innerHTML = '<span class="error">Error: ' + e.message + '</span>'; });
}

function exportCSV() {
  if (!tabState.query.results) return;
  var cols = tabState.query.results.columns.map(function(c) { return c.name; });
  var lines = [cols.map(escCsv).join(',')];
  tabState.query.results.rows.forEach(function(r) {
    lines.push(r.map(function(v) { return escCsv(v === null ? '' : String(v)); }).join(','));
  });
  download(lines.join('\n'), 'query-result.csv', 'text/csv');
}

function exportJSON() {
  if (!tabState.query.results) return;
  var arr = tabState.query.results.rows.map(function(r) {
    var o = {};
    tabState.query.results.columns.forEach(function(c, i) { o[c.name] = r[i]; });
    return o;
  });
  download(JSON.stringify(arr, null, 2), 'query-result.json', 'application/json');
}

function download(content, filename, mime) {
  var blob = new Blob([content], {type: mime});
  var a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}

function escCsv(s) {
  if (s.indexOf(',') >= 0 || s.indexOf('"') >= 0 || s.indexOf('\n') >= 0) {
    return '"' + s.replace(/"/g, '""') + '"';
  }
  return s;
}

function openAnalyticsPanel(colName, colIdx, evt) {
  if (!tabState.query.results || !tabState.query.results.columns) return;
  var isMulti = evt && evt.ctrlKey;

  if (!isMulti) {
    panelState.selectedCols = [{name: colName, idx: colIdx}];
    panelState.activeMultiCols = {};
  } else {
    if (!Object.keys(panelState.activeMultiCols).length) {
      for (var i = 0; i < panelState.selectedCols.length; i++) {
        panelState.activeMultiCols[panelState.selectedCols[i].name] = true;
      }
    }
    var existing = panelState.selectedCols.findIndex(function(c) { return c.name === colName; });
    if (existing >= 0) {
      panelState.selectedCols.splice(existing, 1);
      if (!panelState.selectedCols.length) { closeAnalyticsPanel(); return; }
    } else {
      panelState.selectedCols.push({name: colName, idx: colIdx});
      panelState.activeMultiCols[colName] = true;
    }
  }

  renderAnalyticsPanel();
  document.getElementById('analytics-backdrop').classList.add('visible');
  document.getElementById('analytics-panel').classList.add('open');

  if (!panelState.analysisCache) {
    fetchAnalysis();
  }
}

function closeAnalyticsPanel() {
  document.getElementById('analytics-backdrop').classList.remove('visible');
  document.getElementById('analytics-panel').classList.remove('open');
  panelState.selectedCols = [];
}

function computeQuickStats(colIdx) {
  var rows = tabState.query.results.rows;
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

function selectRepresentativeRows(colIdx, maxRows) {
  maxRows = maxRows || 8;
  var rows = tabState.query.results.rows;
  var selected = [];
  var used = new Set();
  var nullCount = 0;

  for (var i = 0; i < rows.length && nullCount < 3; i++) {
    if (rows[i][colIdx] === null || rows[i][colIdx] === undefined) {
      if (!used.has(i)) { used.add(i); selected.push(i); nullCount++; }
    }
  }

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
      var outlierCount = 0;
      for (var vi = 0; vi < vals.length && outlierCount < 3 && selected.length < maxRows; vi++) {
        var z = Math.abs((vals[vi].val - mean) / stddev);
        if (z > 2 && !used.has(vals[vi].idx)) {
          used.add(vals[vi].idx);
          selected.push(vals[vi].idx);
          outlierCount++;
        }
      }
    }
  }

  var remaining = [];
  for (var i = 0; i < rows.length; i++) {
    if (!used.has(i)) remaining.push(i);
  }
  for (var i = remaining.length - 1; i > 0; i--) {
    var j = Math.floor(Math.random() * (i + 1));
    var tmp = remaining[i]; remaining[i] = remaining[j]; remaining[j] = tmp;
  }
  for (var i = 0; i < remaining.length && selected.length < maxRows; i++) {
    selected.push(remaining[i]);
  }

  return selected.sort(function(a,b) { return a - b; });
}

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

function renderMultiColumnPanel() {
  var html = '';

  html += '<div class="apanel-header">';
  var names = panelState.selectedCols.map(function(c) { return c.name; });
  html += '<h3>' + names.length + ' columns selected</h3>';
  html += '<div style="display:flex;gap:0.25rem;">';
  html += '<button class="apanel-close" onclick="refreshChart()" title="Refresh chart">\u21BB</button>';
  html += '<button class="apanel-close" onclick="closeAnalyticsPanel()">\u00D7</button>';
  html += '</div>';
  html += '</div>';

  html += '<div class="apanel-col-list">';
  for (var i = 0; i < panelState.selectedCols.length; i++) {
    var c = panelState.selectedCols[i];
    var isActive = panelState.activeMultiCols[c.name] !== false;
    html += '<span class="apanel-col-tag' + (isActive ? ' active' : '') + '" onclick="toggleMultiCol(' + i + ')">' + escHtml(c.name) + ' <span class="remove-col" onclick="event.stopPropagation(); removeMultiCol(' + i + ')">\u00D7</span></span>';
  }
  html += '</div>';

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
  var c = panelState.selectedCols[idx];
  if (!c) return;
  var current = panelState.activeMultiCols[c.name];
  panelState.activeMultiCols[c.name] = current === false ? true : false;
  renderAnalyticsPanel();
}

function removeMultiCol(idx) {
  var removed = panelState.selectedCols[idx];
  if (removed) delete panelState.activeMultiCols[removed.name];
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

  html += '<div class="corr-cell header"></div>';
  for (var j = 0; j < n; j++) {
    html += '<div class="corr-cell header" style="font-size:0.7rem;">' + escHtml(names[j]) + '</div>';
  }

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

function renderSingleColumnPanel() {
  var col = panelState.selectedCols[0];
  var colDef = tabState.query.results.columns[col.idx];
  var stats = computeQuickStats(col.idx);
  var reprRows = selectRepresentativeRows(col.idx);
  var analysis = panelState.analysisCache ? panelState.analysisCache.columns[col.name] : null;

  var html = '';

  html += '<div class="apanel-header">';
  html += '<h3>' + escHtml(col.name) + ' <span class="type-badge">' + escHtml(colDef.type) + '</span></h3>';
  html += '<div style="display:flex;gap:0.25rem;">';
  html += '<button class="apanel-close" onclick="refreshChart()" title="Refresh chart">\u21BB</button>';
  html += '<button class="apanel-close" onclick="closeAnalyticsPanel()">\u00D7</button>';
  html += '</div>';
  html += '</div>';

  html += '<div class="apanel-stats">';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Rows:</span> <span class="apanel-stat-value">' + stats.total + '</span></span>';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Null:</span> <span class="apanel-stat-value null">' + stats.nullCount + ' (' + stats.nullPct + '%)</span></span>';
  html += '<span class="apanel-stat"><span class="apanel-stat-label">Distinct:</span> <span class="apanel-stat-value distinct">' + stats.distinct + '</span></span>';
  if (stats.isNumeric) {
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Min:</span> <span class="apanel-stat-value">' + (stats.min !== null ? stats.min.toFixed(2) : '\u2014') + '</span></span>';
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Max:</span> <span class="apanel-stat-value">' + (stats.max !== null ? stats.max.toFixed(2) : '\u2014') + '</span></span>';
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Mean:</span> <span class="apanel-stat-value">' + (stats.mean || '\u2014') + '</span></span>';
  }
  if (stats.topValues.length) {
    html += '<span class="apanel-stat"><span class="apanel-stat-label">Top:</span> <span class="apanel-stat-value" style="font-weight:400;font-size:0.75rem;">' + escHtml(stats.topValues.join(', ')) + '</span></span>';
  }
  html += '</div>';

  html += '<div id="apanel-chart" class="apanel-chart-wrap">';
  if (analysis) {
    html += '<div class="chart-canvas-wrapper"><canvas id="apanel-canvas"></canvas></div>';
  } else {
    html += '<div class="apanel-loading">Loading histogram...</div>';
  }
  html += '</div>';

  if (analysis && panelState.analysisCache) {
    var summary = findColumnSummary(col.name);
    if (summary) {
      html += '<div class="apanel-summary">' + escHtml(summary) + '</div>';
    }
  }

  html += '<div class="apanel-repr">';
  html += '<h4>Representative Rows</h4>';

  var leftIdx = col.idx > 0 ? col.idx - 1 : (col.idx + 1 < tabState.query.results.columns.length ? col.idx + 1 : -1);
  var rightIdx = col.idx + 1 < tabState.query.results.columns.length ? col.idx + 1 : (col.idx > 0 ? col.idx - 1 : -1);
  if (leftIdx === col.idx) leftIdx = -1;
  if (rightIdx === col.idx) rightIdx = -1;
  if (leftIdx === rightIdx) rightIdx = -1;

  html += '<table><thead><tr><th>#</th>';
  if (leftIdx >= 0) html += '<th>' + escHtml(tabState.query.results.columns[leftIdx].name) + '</th>';
  html += '<th>' + escHtml(col.name) + '</th>';
  if (rightIdx >= 0) html += '<th>' + escHtml(tabState.query.results.columns[rightIdx].name) + '</th>';
  html += '</tr></thead><tbody>';

  for (var ri = 0; ri < reprRows.length; ri++) {
    var rowIdx = reprRows[ri];
    html += '<tr>';
    html += '<td><span class="row-idx" onclick="scrollToRow(' + rowIdx + ')">' + (rowIdx + 1) + '</span></td>';
    if (leftIdx >= 0) html += '<td>' + formatCell(tabState.query.results.rows[rowIdx][leftIdx]) + '</td>';
    html += '<td>' + formatCell(tabState.query.results.rows[rowIdx][col.idx]) + '</td>';
    if (rightIdx >= 0) html += '<td>' + formatCell(tabState.query.results.rows[rowIdx][rightIdx]) + '</td>';
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

function renderPanelCharts() {
  if (panelState.selectedCols.length === 1) {
    renderSingleColumnChart();
  } else {
    renderMultiColumnCharts();
  }
}

function getColType(colName) {
  for (var i = 0; i < tabState.query.results.columns.length; i++) {
    if (tabState.query.results.columns[i].name === colName) return tabState.query.results.columns[i].type;
  }
  return 'VARCHAR';
}

function detectColumnCategory(colIdx) {
  var type = getColType(tabState.query.results.columns[colIdx].name).toUpperCase();
  if (/INT|FLOAT|DOUBLE|DECIMAL|NUMERIC/.test(type)) return 'numeric';
  if (/TIMESTAMP|DATE|TIME/.test(type)) return 'temporal';
  if (/BOOL/.test(type)) return 'boolean';
  var rows = tabState.query.results.rows;
  var numericCount = 0;
  var nonNumericCount = 0;
  for (var i = 0; i < Math.min(rows.length, 100); i++) {
    var v = rows[i][colIdx];
    if (v === null || v === undefined) continue;
    if (typeof v === 'number' || (!isNaN(parseFloat(v)) && isFinite(v))) {
      numericCount++;
    } else {
      nonNumericCount++;
    }
  }
  if (numericCount > nonNumericCount && nonNumericCount === 0) return 'numeric';
  return 'categorical';
}

function renderSingleColumnChart() {
  var canvas = document.getElementById('apanel-canvas');
  if (!canvas) return;

  var col = panelState.selectedCols[0];
  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }

  if (panelState.analysisCache) {
    var analysis = panelState.analysisCache.columns[col.name];
    if (analysis) {
      var chartConfig = buildServerChartConfig(analysis, col);
      if (chartConfig) {
        try {
          canvas._chart = new Chart(ctx, chartConfig);
          return;
        } catch (e) {
        }
      }
    }
  }

  var chartConfig = buildClientChartConfig(col);
  if (chartConfig) {
    try {
      canvas._chart = new Chart(ctx, chartConfig);
    } catch (e) {
      var statusEl = document.getElementById('query-status');
      if (statusEl) statusEl.innerHTML = '<span class="error">Chart error: ' + e.message + '</span>';
    }
  }
}

function buildServerChartConfig(analysis, col) {
  var category = detectColumnCategory(col.idx);

  switch (category) {
    case 'numeric':
      if (analysis.histogram && analysis.histogram.length) {
        return buildHistogramChart(analysis, col);
      }
      if (analysis.type === 'numeric' && analysis.stats) {
        return buildNumericStatsChart(analysis, col);
      }
      return null;
    case 'boolean':
      if (analysis.type === 'boolean' || analysis.stats) {
        return buildBooleanChart(analysis, col);
      }
      return null;
    case 'temporal':
      if (analysis.histogram && analysis.histogram.length) {
        return buildHistogramChart(analysis, col);
      }
      return null;
    default:
      if (analysis.top_values && analysis.top_values.length) {
        return buildTopValuesChart(analysis, col);
      }
      return null;
  }
}

function buildClientChartConfig(col) {
  var category = detectColumnCategory(col.idx);
  var stats = computeQuickStats(col.idx);
  var allRows = tabState.query.results.rows;

  if (category === 'numeric') {
    var numericVals = [];
    for (var i = 0; i < allRows.length; i++) {
      var v = allRows[i][col.idx];
      if (v !== null && v !== undefined && !isNaN(parseFloat(v))) {
        numericVals.push(parseFloat(v));
      }
    }
    if (numericVals.length > 1) {
      var min = Math.min.apply(null, numericVals);
      var max = Math.max.apply(null, numericVals);
      if (min < max) {
        var binCount = Math.min(20, Math.max(5, Math.floor(numericVals.length / 10)));
        var binWidth = (max - min) / binCount;
        var bins = new Array(binCount).fill(0);
        for (var i = 0; i < numericVals.length; i++) {
          var idx = Math.min(binCount - 1, Math.floor((numericVals[i] - min) / binWidth));
          bins[idx]++;
        }
        var labels = bins.map(function(_, i) {
          return (min + i * binWidth).toFixed(1) + '-' + (min + (i + 1) * binWidth).toFixed(1);
        });
        return {
          type: 'bar',
          data: { labels: labels, datasets: [{ label: 'Count', data: bins, backgroundColor: 'rgba(0,101,255,0.6)', borderColor: 'rgba(0,101,255,1)', borderWidth: 1 }] },
          options: {
            responsive: true, maintainAspectRatio: false,
            plugins: { legend: { display: false } },
            scales: { x: { ticks: { color: '#9099A1', font: { size: 9 }, maxRotation: 45 }, grid: { color: '#31393F' } }, y: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { color: '#31393F' }, beginAtZero: true } }
          }
        };
      }
    }
    return {
      type: 'bar',
      data: { labels: ['Min', 'Mean', 'Max'], datasets: [{ label: 'Value', data: [stats.min || 0, parseFloat(stats.mean || 0), stats.max || 0], backgroundColor: ['#8739B1', '#0065FF', '#27B681'] }] },
      options: {
        responsive: true, maintainAspectRatio: false,
        plugins: { legend: { display: false } },
        scales: { x: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { display: false } }, y: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { color: '#31393F' } } }
      }
    };
  }

  if (category === 'boolean') {
    var trueCount = 0, falseCount = 0, nullBoolCount = 0;
    for (var i = 0; i < allRows.length; i++) {
      var v = allRows[i][col.idx];
      if (v === null || v === undefined) { nullBoolCount++; }
      else if (v === true || v === 'true' || v === 'TRUE' || v === '1' || v === 1) { trueCount++; }
      else { falseCount++; }
    }
    var boolTotal = trueCount + falseCount + nullBoolCount || 1;
    return {
      type: 'bar',
      data: {
        labels: ['Boolean'],
        datasets: [
          { label: 'True (' + (trueCount / boolTotal * 100).toFixed(1) + '%)', data: [trueCount], backgroundColor: '#27B681' },
          { label: 'False (' + (falseCount / boolTotal * 100).toFixed(1) + '%)', data: [falseCount], backgroundColor: '#f87171' },
          { label: 'Null (' + (nullBoolCount / boolTotal * 100).toFixed(1) + '%)', data: [nullBoolCount], backgroundColor: '#596773' }
        ]
      },
      options: {
        indexAxis: 'y', responsive: true, maintainAspectRatio: false,
        plugins: { legend: { labels: { color: '#DEE4EA', font: { size: 9 } } } },
        scales: { x: { stacked: true, ticks: { color: '#9099A1' }, grid: { color: '#31393F' }, beginAtZero: true }, y: { stacked: true, ticks: { color: '#9099A1' }, grid: { display: false } } }
      }
    };
  }

  var freq = {};
  for (var i = 0; i < allRows.length; i++) {
    var v = allRows[i][col.idx];
    var sv = String(v === null || v === undefined ? 'NULL' : v);
    freq[sv] = (freq[sv] || 0) + 1;
  }
  var entries = Object.entries(freq).sort(function(a, b) { return b[1] - a[1]; }).slice(0, 15);
  var topVals = entries.map(function(e) { return { value: e[0], count: e[1], pct: allRows.length > 0 ? (e[1] / allRows.length * 100) : 0 }; });

  var labels = topVals.map(function(v) { return v.value; });
  var data = topVals.map(function(v) { return v.count; });
  var pcts = topVals.map(function(v) { return v.pct.toFixed(1); });

  return {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{ label: 'Frequency', data: data, backgroundColor: PANEL_COLORS.slice(0, Math.min(labels.length, PANEL_COLORS.length)), borderWidth: 0 }]
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: { callbacks: { label: function(ctx) { return ctx.parsed.x + ' (' + pcts[ctx.dataIndex] + '%)'; } } }
      },
      scales: {
        x: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { color: '#31393F' }, beginAtZero: true },
        y: { ticks: { color: '#9099A1', font: { size: 9 } }, grid: { display: false } }
      }
    }
  };
}

function buildHistogramChart(analysis, col) {
  var bins = analysis.histogram;
  var labels = bins.map(function(b) {
    var s = b.bin_start.toFixed(1);
    var e = b.bin_end.toFixed(1);
    return s + '-' + e;
  });
  var data = bins.map(function(b) { return b.count; });

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

  topVals = topVals.slice(0, 10);

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
  var total = stats.count || 1;

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
          label: 'False (' + (falseCount / total * 100).toFixed(1) + '%)',
          data: [falseCount],
          backgroundColor: '#f87171'
        },
        {
          label: 'Null (' + (nullCount / total * 100).toFixed(1) + '%)',
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
  if (analysis.histogram && analysis.histogram.length) {
    return buildHistogramChart(analysis, col);
  }
  return null;
}

function renderMultiColumnCharts() {
  var canvas = document.getElementById('apanel-canvas-multi');
  if (!canvas || !panelState.analysisCache) return;

  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }

  var activeCols = panelState.selectedCols.filter(function(c) {
    return panelState.activeMultiCols[c.name] !== false;
  });
  if (!activeCols.length) return;
  var datasets = [];
  var allLabels = [];

  var allNumeric = activeCols.every(function(c) {
    var t = getColType(c.name).toUpperCase();
    return /INT|FLOAT|DOUBLE|DECIMAL|NUMERIC/.test(t);
  });

  if (allNumeric) {
    var allBins = [];
    for (var i = 0; i < activeCols.length; i++) {
      var analysis = panelState.analysisCache.columns[activeCols[i].name];
      if (!analysis || !analysis.histogram || !analysis.histogram.length) continue;
      var bins = analysis.histogram;
      var total = bins.reduce(function(s, b) { return s + b.count; }, 0);
      var points = bins.map(function(b) {
        return {
          x: (b.bin_start + b.bin_end) / 2,
          y: total > 0 ? b.count / total : 0
        };
      });
      allBins.push({name: activeCols[i].name, points: points});
    }

    if (!allBins.length) return;

    datasets = allBins.map(function(b, i) {
      return {
        label: b.name,
        data: b.points,
        backgroundColor: PANEL_COLORS[i % PANEL_COLORS.length] + '66',
        borderColor: PANEL_COLORS[i % PANEL_COLORS.length],
        borderWidth: 1,
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        showLine: true
      };
    });

    canvas._chart = new Chart(ctx, {
      type: 'scatter',
      data: { datasets: datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { labels: { color: '#DEE4EA', font: { size: 9 } } },
          tooltip: {
            callbacks: {
              label: function(ctx) {
                return ctx.dataset.label + ': ' + (ctx.parsed.y * 100).toFixed(1) + '% at ' + ctx.parsed.x.toFixed(2);
              }
            }
          }
        },
        scales: {
          x: {
            type: 'linear',
            ticks: { color: '#9099A1', font: { size: 8 } },
            grid: { color: '#31393F' },
            title: { display: true, text: 'Value', color: '#9099A1', font: { size: 9 } }
          },
          y: {
            ticks: { color: '#9099A1', font: { size: 8 }, callback: function(v) { return (v * 100).toFixed(0) + '%'; } },
            grid: { color: '#31393F' },
            beginAtZero: true,
            title: { display: true, text: 'Density', color: '#9099A1', font: { size: 9 } }
          }
        },
        elements: { point: { radius: 0 } }
      }
    });
  } else {
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

function refreshChart() {
  panelState.analysisCache = null;
  panelState.fetchingAnalysis = false;
  renderAnalyticsPanel();
  fetchAnalysis();
}

function fetchAnalysis() {
  if (!tabState.query.results || panelState.fetchingAnalysis) return;
  panelState.fetchingAnalysis = true;
  var chartWrap = document.getElementById('apanel-chart');
  if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading">Loading histogram...</div>';

  fetch('/analyze', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      columns: tabState.query.results.columns,
      rows: tabState.query.results.rows
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    panelState.fetchingAnalysis = false;
    if (d.error) {
      if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading" style="color:var(--red);">Analysis error: ' + escHtml(d.error) + '</div>';
      return;
    }
    panelState.analysisCache = d;
    renderAnalyticsPanel();
  })
  .catch(function(e) {
    panelState.fetchingAnalysis = false;
    if (chartWrap) chartWrap.innerHTML = '<div class="apanel-loading" style="color:var(--red);">Error: ' + e.message + '</div>';
  });
}

function renderAnalyzeTab() {
  var placeholder = document.getElementById('analyze-placeholder');
  var content = document.getElementById('analyze-content');
  if (!content) return;
  if (!tabState.analyze.analysisCache || !tabState.query.results) {
    if (placeholder) placeholder.style.display = 'block';
    if (content) content.style.display = 'none';
    return;
  }
  if (placeholder) placeholder.style.display = 'none';
  if (content) content.style.display = 'block';

  renderAnalyzeColumnList();

  var col = tabState.analyze.selectedCols.length ? tabState.analyze.selectedCols[0] : {name: tabState.query.results.columns[0].name, idx: 0};
  var analysis = tabState.analyze.analysisCache.columns[col.name];
  var colDef = tabState.query.results.columns[col.idx];
  var stats = typeof computeQuickStats === 'function' ? computeQuickStats(col.idx) : {};

  // Render chart header
  var headerEl = document.getElementById('analyze-chart-header');
  if (headerEl) {
    headerEl.innerHTML = '<div style="font-size:0.85rem;font-weight:600;">' + escHtml(col.name) + ' <span style="color:var(--text-muted);font-weight:400;font-size:0.75rem;">' + escHtml(colDef ? colDef.type : '') + '</span></div>';
  }

  // Render chart
  var chartWrap = document.getElementById('analyze-chart-wrap');
  if (chartWrap) {
    chartWrap.innerHTML = '<canvas id="analyze-canvas" style="width:100%;height:100%;"></canvas>';
    var canvas = document.getElementById('analyze-canvas');
    if (canvas && analysis) {
      var ctx = canvas.getContext('2d');
      if (canvas._chart) canvas._chart.destroy();
      panelState.selectedCols = [col];
      panelState.analysisCache = tabState.analyze.analysisCache;
      var chartConfig = buildServerChartConfig(analysis, col);
      if (!chartConfig) chartConfig = buildClientChartConfig(col);
      if (chartConfig) {
        try { canvas._chart = new Chart(ctx, chartConfig); } catch(e) {}
      }
    }
  }

  // Summary line
  var summaryLine = document.getElementById('analyze-summary-line');
  if (summaryLine && analysis && tabState.analyze.analysisCache.summary) {
    var s = findColumnSummary ? findColumnSummary(col.name) : null;
    summaryLine.textContent = s || '';
  }

  // Representative rows
  var reprEl = document.getElementById('analyze-repr-rows');
  if (reprEl && typeof selectRepresentativeRows === 'function') {
    var reprRows = selectRepresentativeRows(col.idx);
    var leftIdx = col.idx > 0 ? col.idx - 1 : (col.idx + 1 < tabState.query.results.columns.length ? col.idx + 1 : -1);
    var rightIdx = col.idx + 1 < tabState.query.results.columns.length ? col.idx + 1 : (col.idx > 0 ? col.idx - 1 : -1);
    if (leftIdx === col.idx) leftIdx = -1;
    if (rightIdx === col.idx) rightIdx = -1;
    if (leftIdx === rightIdx) rightIdx = -1;

    var html = '<div style="font-size:0.85rem;font-weight:600;margin-bottom:0.5rem;">Representative Rows</div>';
    html += '<table style="width:100%;font-size:0.75rem;"><thead><tr><th>#</th>';
    if (leftIdx >= 0) html += '<th>' + escHtml(tabState.query.results.columns[leftIdx].name) + '</th>';
    html += '<th>' + escHtml(col.name) + '</th>';
    if (rightIdx >= 0) html += '<th>' + escHtml(tabState.query.results.columns[rightIdx].name) + '</th>';
    html += '</tr></thead><tbody>';
    for (var ri = 0; ri < reprRows.length; ri++) {
      var rowIdx = reprRows[ri];
      html += '<tr>';
      html += '<td style="padding:0.25rem 0.5rem;color:var(--text-muted);cursor:pointer;text-decoration:underline dotted;" onclick="scrollToRow(' + rowIdx + ')">' + (rowIdx + 1) + '</td>';
      if (leftIdx >= 0) html += '<td style="padding:0.25rem 0.5rem;">' + (tabState.query.results.rows[rowIdx][leftIdx] === null ? '<span style="color:var(--text-muted);font-style:italic;">NULL</span>' : escHtml(String(tabState.query.results.rows[rowIdx][leftIdx]))) + '</td>';
      html += '<td style="padding:0.25rem 0.5rem;">' + (tabState.query.results.rows[rowIdx][col.idx] === null ? '<span style="color:var(--text-muted);font-style:italic;">NULL</span>' : escHtml(String(tabState.query.results.rows[rowIdx][col.idx]))) + '</td>';
      if (rightIdx >= 0) html += '<td style="padding:0.25rem 0.5rem;">' + (tabState.query.results.rows[rowIdx][rightIdx] === null ? '<span style="color:var(--text-muted);font-style:italic;">NULL</span>' : escHtml(String(tabState.query.results.rows[rowIdx][rightIdx]))) + '</td>';
      html += '</tr>';
    }
    html += '</tbody></table>';
    reprEl.innerHTML = html;
  }
}

function renderAnalyzeColumnList() {
  var list = document.getElementById('analyze-col-list');
  var summary = document.getElementById('analyze-summary');
  if (!list || !tabState.query.results) return;
  var html = '';
  tabState.query.results.columns.forEach(function(c, i) {
    var isSelected = tabState.analyze.selectedCols.some(function(sc) { return sc.name === c.name; });
    html += '<label style="display:flex;align-items:center;gap:0.375rem;padding:0.25rem 0.35rem;font-size:0.85rem;cursor:pointer;border-radius:0.25rem;' + (isSelected ? 'background:rgba(0,101,255,0.15);' : '') + '" onclick="openAnalyticsPanel(\'' + escJs(c.name) + '\',' + i + ')">';
    html += '<input type="checkbox" ' + (isSelected ? 'checked' : '') + ' style="pointer-events:none;"> ' + escHtml(c.name) + ' <span style="font-size:0.65rem;color:var(--text-muted);background:var(--surface);padding:0.1rem 0.35rem;border-radius:0.25rem;">' + c.type + '</span>';
    html += '</label>';
  });
  list.innerHTML = html;
  if (summary && tabState.analyze.analysisCache && tabState.analyze.analysisCache.summary) {
    var sHtml = '<ul style="list-style:disc;padding-left:1.25rem;">';
    tabState.analyze.analysisCache.summary.forEach(function(s) {
      sHtml += '<li style="margin-bottom:0.25rem;">' + escHtml(s) + '</li>';
    });
    sHtml += '</ul>';
    summary.innerHTML = sHtml;
  }
}

document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    closeAnalyticsPanel();
  }
  if (e.ctrlKey && e.key === 'Enter') { runQuery(); }
});
