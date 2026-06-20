var reportState = {
  columns: [],
  rows: [],
  analysis: null,
  charts: [],
  title: 'Untitled Report',
  sql: '',
  projectId: ''
};

var COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6','#eab308','#14b8a6','#8b5cf6','#ec4899','#0ea5e9'];
var COLORS_ALPHA = COLORS.map(function(c) { return c + 'CC'; });

Chart.register(chartjsZoom);

var zoomOpts = {
  zoom: { wheel: { enabled: true }, pinch: { enabled: true }, mode: 'xy', onZoomComplete: function() { } },
  pan: { enabled: true, mode: 'xy', onPanComplete: function() { } }
};

function initReport(data, queryData) {
  if (data) {
    reportState.analysis = data;
    if (queryData) {
      reportState.columns = queryData.query_columns || queryData.columns || [];
      reportState.rows = queryData.query_rows || queryData.rows || [];
    }
    renderReport();
  }
}

function renderReport() {
  var app = document.getElementById('report-app');
  if (!reportState.analysis) { app.innerHTML = '<p>No analysis data</p>'; return; }

  var html = '<div class="report-layout">';
  html += '<div class="report-topbar">';
  html += '<input type="text" id="report-title" value="' + escHtml(reportState.title) + '" class="input" style="font-size:1.25rem;font-weight:600;width:400px;background:transparent;border:none;color:var(--text);" onchange="reportState.title=this.value;renderReport()">';
  html += '<div style="display:flex;gap:0.5rem;">';
  html += '<button class="btn btn-secondary" onclick="saveReport()">Save</button>';
  html += '</div></div>';
  html += '<div class="report-body">';
  html += '<div class="report-sidebar">';
  html += '<h3>Columns</h3>';
  reportState.columns.forEach(function(c, i) {
    html += '<label class="report-column-label"><input type="checkbox" checked data-col="' + i + '" onchange="toggleColumn(' + i + ',this.checked)"> ' + escHtml(c.name) + ' <span class="type-badge">' + c.type + '</span></label>';
  });
  html += '<h3 style="margin-top:1.5rem;">Add Chart</h3>';
  html += '<div class="chart-types">';
  ['Bar','Pie','Doughnut','Line','Scatter','Histogram','Radar','PolarArea','Stacked Bar'].forEach(function(t) {
    html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.3rem 0.6rem;" onclick="addChart(\'' + t.toLowerCase().replace(/\s+/g, '-') + '\')">' + t + '</button>';
  });
  html += '</div>';
  html += '<p style="font-size:0.75rem;color:var(--text-muted);margin-top:0.5rem;">Scroll/pinch to zoom · Drag to pan · Double-click to reset</p>';
  html += '</div>';
  html += '<div class="report-canvas">';
  html += '<div class="stats-summary"><h3>Summary</h3>';
  if (reportState.analysis.summary) {
    html += '<ul>';
    reportState.analysis.summary.forEach(function(s) { html += '<li>' + escHtml(s) + '</li>'; });
    html += '</ul>';
  }
  html += '</div>';
  html += '<div id="chart-container"></div>';
  html += '</div></div></div>';
  app.innerHTML = html;
  renderCharts();
}

function toggleColumn(idx, visible) { renderCharts(); }

function addChart(type) {
  var id = 'c' + Date.now();
  var visCols = getVisibleColumns();
  reportState.charts.push({
    id: id,
    type: type,
    x_column: visCols[0] ? visCols[0].name : '',
    y_column: visCols.length > 1 ? visCols[1].name : '',
    group_by: '',
    bucket: 'auto',
    title: type.charAt(0).toUpperCase() + type.slice(1),
    max_groups: 10,
    stacking: type === 'stacked-bar'
  });
  renderCharts();
}

function getVisibleColumns() {
  var cols = [];
  reportState.columns.forEach(function(c, i) {
    var cb = document.querySelector('.report-column-label input[data-col="' + i + '"]');
    if (!cb || cb.checked) cols.push(c);
  });
  return cols.length ? cols : reportState.columns;
}

function renderCharts() {
  var container = document.getElementById('chart-container');
  if (!container) return;
  var html = '';
  reportState.charts.forEach(function(chart, idx) {
    html += '<div class="chart-card" id="chart-' + chart.id + '">';
    html += '<div class="chart-card-header">';
    html += '<div style="display:flex;align-items:center;gap:0.5rem;flex:1;">';
    html += '<input type="text" value="' + escHtml(chart.title) + '" style="background:transparent;border:none;color:var(--text);font-weight:600;flex:1;" onchange="reportState.charts[' + idx + '].title=this.value">';
    html += '<span class="type-badge" style="text-transform:capitalize;">' + chart.type.replace(/-/g,' ') + '</span>';
    html += '</div>';
    html += '<div style="display:flex;gap:0.375rem;">';
    html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.5rem;" onclick="exportChartPNG(\'' + chart.id + '\')" title="Export as PNG">&#8595;</button>';
    html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.2rem 0.5rem;" onclick="removeChart(\'' + chart.id + '\')">&#10005;</button>';
    html += '</div>';
    html += '</div>';
    html += '<div class="chart-config">';
    html += '<label>X: <select onchange="reportState.charts[' + idx + '].x_column=this.value;renderCharts()">';
    getVisibleColumns().forEach(function(c) {
      html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.x_column ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
    });
    html += '</select></label>';
    if (chart.type !== 'pie' && chart.type !== 'doughnut' && chart.type !== 'histogram' && chart.type !== 'radar' && chart.type !== 'polararea') {
      html += '<label>Y: <select onchange="reportState.charts[' + idx + '].y_column=this.value;renderCharts()">';
      getVisibleColumns().forEach(function(c) {
        html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.y_column ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
      });
      html += '</select></label>';
    }
    if (chart.type !== 'pie' && chart.type !== 'doughnut' && chart.type !== 'histogram' && chart.type !== 'radar' && chart.type !== 'polararea') {
      html += '<label>Group: <select onchange="reportState.charts[' + idx + '].group_by=this.value;renderCharts()"><option value="">None</option>';
      getVisibleColumns().forEach(function(c) {
        html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.group_by ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
      });
      html += '</select></label>';
    }
    if (chart.type === 'line') {
      html += '<label>Bucket: <select onchange="reportState.charts[' + idx + '].bucket=this.value;renderCharts()">';
      ['auto','hour','day','week','month'].forEach(function(b) {
        html += '<option value="' + b + '"' + (b === chart.bucket ? ' selected' : '') + '>' + b + '</option>';
      });
      html += '</select></label>';
    }
    if (chart.type === 'histogram') {
      html += '<label>Bins: <input type="number" value="' + (chart.bins || 20) + '" min="2" max="100" style="width:50px;background:var(--surface-2);color:var(--text);border:0.0625rem solid var(--border);border-radius:var(--radius);padding:0.15rem 0.3rem;" onchange="reportState.charts[' + idx + '].bins=parseInt(this.value)||20;renderCharts()"></label>';
    }
    html += '<label>Max: <input type="number" value="' + (chart.max_groups || 10) + '" min="2" max="200" style="width:50px;background:var(--surface-2);color:var(--text);border:0.0625rem solid var(--border);border-radius:var(--radius);padding:0.15rem 0.3rem;" onchange="reportState.charts[' + idx + '].max_groups=parseInt(this.value)||10;renderCharts()"></label>';
    html += '</div>';
    html += '<div class="chart-canvas-wrapper"><canvas id="canvas-' + chart.id + '"></canvas></div>';
    html += '</div>';
  });
  container.innerHTML = html;
  reportState.charts.forEach(function(chart) {
    var canvas = document.getElementById('canvas-' + chart.id);
    if (!canvas) return;
    canvas.addEventListener('dblclick', function() { resetZoom(chart.id); });
    renderChart(canvas, chart);
  });
}

function resetZoom(id) {
  var canvas = document.getElementById('canvas-' + id);
  if (canvas && canvas._chart) canvas._chart.resetZoom();
}

function renderChart(canvas, config) {
  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }
  var data = buildChartData(config);
  if (!data.labels.length && !data.datasets.length) {
    var p = canvas.parentNode;
    if (p) p.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:300px;color:var(--text-muted);font-size:0.85rem;">Select columns to chart</div>';
    return;
  }

  var chartType = config.type === 'histogram' ? 'bar' : config.type === 'stacked-bar' ? 'bar' : config.type === 'doughnut' ? 'doughnut' : config.type === 'polararea' ? 'polarArea' : config.type;

  var chartConfig = {
    type: chartType,
    data: { labels: data.labels, datasets: data.datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { labels: { color: '#DEE4EA', font: { family: 'Source Sans 3' } } },
        tooltip: {
          backgroundColor: '#1C1C1C',
          titleColor: '#DEE4EA',
          bodyColor: '#9099A1',
          borderColor: '#515E69',
          borderWidth: 1,
          padding: 8,
          cornerRadius: 4,
          callbacks: {
            label: function(ctx) {
              var val = ctx.parsed.y !== undefined ? ctx.parsed.y : ctx.parsed.r !== undefined ? ctx.parsed.r : ctx.parsed;
              if (typeof val === 'number') return ctx.dataset.label + ': ' + val.toLocaleString();
              return ctx.dataset.label + ': ' + val;
            }
          }
        },
        zoom: zoomOpts
      },
      scales: {
        x: { ticks: { color: '#9099A1', font: { family: 'Source Sans 3' } }, grid: { color: '#31393F' } },
        y: { ticks: { color: '#9099A1', font: { family: 'Source Sans 3' } }, grid: { color: '#31393F' }, beginAtZero: true }
      },
      onClick: function(e) { handleChartClick(e, config, this); }
    }
  };

  if (config.stacking || config.type === 'stacked-bar') {
    chartConfig.options.scales.x.stacked = true;
    chartConfig.options.scales.y.stacked = true;
  }
  if (config.type === 'pie' || config.type === 'doughnut') {
    chartConfig.options.scales = {};
    chartConfig.options.cutout = config.type === 'doughnut' ? '50%' : 0;
    chartConfig.options.plugins.legend.position = 'right';
  }
  if (config.type === 'radar') {
    chartConfig.options.scales = {
      r: { ticks: { color: '#9099A1', backdropColor: 'transparent' }, grid: { color: '#31393F' }, pointLabels: { color: '#DEE4EA', font: { family: 'Source Sans 3' } } }
    };
  }
  if (config.type === 'polararea') {
    chartConfig.options.scales = {};
    chartConfig.options.plugins.legend.position = 'right';
  }
  if (config.type === 'scatter') {
    chartConfig.options.scales.x.type = 'linear';
  }
  canvas._chart = new Chart(ctx, chartConfig);
}

function handleChartClick(event, config, chart) {
  var points = chart.getElementsAtEventForMode(event, 'nearest', { intersect: true }, false);
  if (!points.length) return;
  var idx = points[0].index;
  var dsIdx = points[0].datasetIndex;
  var label = chart.data.labels[idx];
  var dataset = chart.data.datasets[dsIdx];
  var val = dataset.data[idx];
  if (!config._drillStack) config._drillStack = [];
  config._drillStack.push({ label: label, filter: config.x_column + '=' + label });

  // Highlight in a small overlay
  showDrillInfo(config.title, label, dataset.label, val);
}

function showDrillInfo(chartTitle, label, series, value) {
  var container = document.getElementById('chart-container');
  if (!container) return;
  var existing = document.getElementById('drill-info');
  if (existing) existing.remove();
  var div = document.createElement('div');
  div.id = 'drill-info';
  div.style.cssText = 'position:fixed;bottom:1rem;right:1rem;background:var(--surface);border:0.0625rem solid var(--border);border-radius:var(--radius);padding:0.75rem 1rem;z-index:200;max-width:320px;font-size:0.85rem;box-shadow:0 4px 12px rgba(0,0,0,0.3);';
  div.innerHTML = '<div style="font-weight:600;margin-bottom:0.25rem;">' + escHtml(chartTitle) + '</div>' +
    '<div style="color:var(--text-muted);">' + escHtml(label) + '</div>' +
    '<div style="color:var(--text);margin-top:0.25rem;">' + escHtml(series ? series + ': ' : '') + (typeof value === 'number' ? value.toLocaleString() : value) + '</div>' +
    '<button class="btn-link" style="margin-top:0.5rem;font-size:0.75rem;" onclick="this.parentElement.remove()">Dismiss</button>';
  document.body.appendChild(div);
}

function buildChartData(config) {
  var labels = [];
  var datasets = [];
  var visCols = getVisibleColumns();
  var xIdx = findColIdx(config.x_column, visCols);
  var yIdx = findColIdx(config.y_column, visCols);
  var gIdx = config.group_by ? findColIdx(config.group_by, visCols) : -1;

  if (config.type === 'pie' || config.type === 'doughnut') {
    var counts = {};
    reportState.rows.forEach(function(row) {
      var val = String(xIdx >= 0 ? row[xIdx] ?? 'null' : '');
      counts[val] = (counts[val] || 0) + 1;
    });
    var entries = Object.entries(counts).sort(function(a,b) { return b[1] - a[1]; }).slice(0, config.max_groups || 10);
    labels = entries.map(function(e) { return e[0]; });
    datasets = [{ data: entries.map(function(e) { return e[1]; }), backgroundColor: COLORS.slice(0, entries.length), borderColor: '#1C1C1C', borderWidth: 1 }];
    return {labels: labels, datasets: datasets};
  }

  if (config.type === 'histogram') {
    var allVals = [];
    reportState.rows.forEach(function(row) { var v = parseFloat(row[xIdx]); if (!isNaN(v)) allVals.push(v); });
    if (!allVals.length) return {labels: [], datasets: []};
    var bins = config.bins || 20;
    var min = Math.min.apply(null, allVals), max = Math.max.apply(null, allVals);
    var binWidth = (max - min) / bins || 1;
    var hist = new Array(bins).fill(0);
    allVals.forEach(function(v) { var b = Math.min(Math.floor((v - min) / binWidth), bins - 1); hist[b]++; });
    labels = [];
    for (var i = 0; i < bins; i++) {
      labels.push((min + i * binWidth).toFixed(1) + '-' + (min + (i + 1) * binWidth).toFixed(1));
    }
    datasets = [{ data: hist, backgroundColor: COLORS[0] + '88', borderColor: COLORS[0], borderWidth: 1 }];
    return {labels: labels, datasets: datasets};
  }

  if (config.type === 'radar' || config.type === 'polararea') {
    var radarData = {};
    var catCol = xIdx;
    var valCol = yIdx >= 0 ? yIdx : (xIdx >= 0 ? xIdx : 0);
    reportState.rows.forEach(function(row) {
      var cat;
      if (catCol >= 0) cat = String(row[catCol] ?? 'null');
      else cat = 'value';
      var v = parseFloat(row[valCol]);
      if (isNaN(v)) return;
      radarData[cat] = (radarData[cat] || 0) + v;
    });
    var sorted = Object.entries(radarData).sort(function(a,b) { return b[1] - a[1]; }).slice(0, config.max_groups || 10);
    labels = sorted.map(function(e) { return e[0]; });
    datasets = [{ data: sorted.map(function(e) { return e[1]; }), backgroundColor: COLORS_ALPHA[0], borderColor: COLORS[0], borderWidth: 1, pointBackgroundColor: COLORS[0] }];
    return {labels: labels, datasets: datasets};
  }

  if (config.type === 'scatter') {
    var points = [];
    reportState.rows.forEach(function(row) {
      var xv = parseFloat(row[xIdx]), yv = parseFloat(row[yIdx]);
      if (isNaN(xv) || isNaN(yv)) return;
      points.push({ x: xv, y: yv });
    });
    datasets = [{ data: points, backgroundColor: COLORS[0] + '66', borderColor: COLORS[0], pointRadius: 4 }];
    return {labels: [], datasets: datasets};
  }

  // Bar, line, stacked-bar
  if (xIdx < 0 || yIdx < 0) return {labels: [], datasets: []};

  if (gIdx >= 0) {
    var map = {};
    reportState.rows.forEach(function(row) {
      var xv = String(row[xIdx] ?? 'null');
      var gv = String(row[gIdx] ?? 'null');
      var yv = parseFloat(row[yIdx]);
      if (isNaN(yv)) return;
      var key = xv + '\x00' + gv;
      if (!map[key]) map[key] = {sum: 0, count: 0};
      map[key].sum += yv;
      map[key].count++;
    });
    var groupSet = new Set();
    Object.keys(map).forEach(function(k) { groupSet.add(k.split('\x00')[1]); });
    var groups = Array.from(groupSet).slice(0, config.max_groups || 10);
    var xSet = new Set();
    Object.keys(map).forEach(function(k) { xSet.add(k.split('\x00')[0]); });
    labels = Array.from(xSet);
    datasets = groups.map(function(g, gi) {
      var color = COLORS[gi % COLORS.length];
      return {
        label: g,
        data: labels.map(function(l) {
          var key = l + '\x00' + g;
          return map[key] ? Math.round(map[key].sum / map[key].count * 100) / 100 : 0;
        }),
        backgroundColor: config.type === 'line' ? color : color + '88',
        borderColor: color,
        borderWidth: config.type === 'line' ? 2 : 1,
        pointRadius: config.type === 'line' ? 2 : undefined,
        fill: config.type === 'line' ? false : undefined,
        tension: config.type === 'line' ? 0.2 : undefined
      };
    });
  } else {
    var agg = {};
    reportState.rows.forEach(function(row) {
      var xv = String(row[xIdx] ?? 'null');
      var yv = parseFloat(row[yIdx]);
      if (isNaN(yv)) return;
      if (!agg[xv]) agg[xv] = {sum: 0, count: 0};
      agg[xv].sum += yv;
      agg[xv].count++;
    });
    var entries = Object.entries(agg).sort(function(a,b) { return b[1].sum - a[1].sum; }).slice(0, config.max_groups || 10);
    labels = entries.map(function(e) { return e[0]; });
    datasets = [{
      data: entries.map(function(e) { return Math.round(e[1].sum / e[1].count * 100) / 100; }),
      backgroundColor: COLORS[0] + '88',
      borderColor: COLORS[0],
      borderWidth: 1,
      pointRadius: config.type === 'line' ? 2 : undefined,
      fill: config.type === 'line' ? false : undefined,
      tension: config.type === 'line' ? 0.2 : undefined
    }];
  }
  return {labels: labels, datasets: datasets};
}

function findColIdx(name, cols) {
  for (var i = 0; i < cols.length; i++) {
    if (cols[i].name === name) return i;
  }
  // fallback to reportState.columns
  for (var j = 0; j < reportState.columns.length; j++) {
    if (reportState.columns[j].name === name) return j;
  }
  return -1;
}

function removeChart(id) {
  reportState.charts = reportState.charts.filter(function(c) { return c.id !== id; });
  renderCharts();
}

function exportChartPNG(id) {
  var canvas = document.getElementById('canvas-' + id);
  if (!canvas || !canvas._chart) return;
  var link = document.createElement('a');
  link.download = 'chart-' + id + '.png';
  link.href = canvas.toDataURL('image/png');
  link.click();
}

function saveReport() {
  var body = {
    title: reportState.title,
    sql: reportState.sql,
    project_id: reportState.projectId,
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

function escHtml(s) { return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function escAttr(s) { return String(s).replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;'); }

function renderReportTab() {
  if (!tabState.analyze.analysisCache || !tabState.query.results) {
    var app = document.getElementById('report-app');
    if (app) app.innerHTML = '<p style="color:var(--text-muted);text-align:center;padding:3rem 0;">Analyze your data first to build a report.<br><button class="btn btn-secondary" style="margin-top:0.5rem;" onclick="switchTab(\'analyze\')">Go to Analyze</button></p>';
    return;
  }
  reportState.analysis = tabState.analyze.analysisCache;
  reportState.columns = tabState.query.results.columns;
  reportState.rows = tabState.query.results.rows;
  renderReport();
}
