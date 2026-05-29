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
  html += '<input type="text" id="report-title" value="' + escHtml(reportState.title) + '" class="input" style="font-size:1.25rem;font-weight:600;width:400px;background:transparent;border:none;color:var(--text);" onchange="reportState.title=this.value">';
  html += '<div style="display:flex;gap:0.5rem;">';
  html += '<button class="btn btn-secondary" onclick="saveReport()">💾 Save</button>';
  html += '<button class="btn btn-secondary" onclick="window.print()">📄 Export PDF</button>';
  html += '</div></div>';
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
    if (chart.type !== 'pie' && chart.type !== 'histogram') {
      html += '<label>Group: <select onchange="reportState.charts[' + idx + '].group_by=this.value"><option value="">None</option>';
      reportState.columns.forEach(function(c) {
        html += '<option value="' + escAttr(c.name) + '"' + (c.name === chart.group_by ? ' selected' : '') + '>' + escHtml(c.name) + '</option>';
      });
      html += '</select></label>';
    }
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
  reportState.charts.forEach(function(chart) {
    var canvas = document.getElementById('canvas-' + chart.id);
    if (!canvas) return;
    renderChart(canvas, chart);
  });
}

function renderChart(canvas, config) {
  var ctx = canvas.getContext('2d');
  if (canvas._chart) { canvas._chart.destroy(); }
  var data = buildChartData(config);
  var chartConfig = {
    type: config.type === 'histogram' ? 'bar' : config.type,
    data: { labels: data.labels, datasets: data.datasets },
    options: {
      responsive: true, maintainAspectRatio: false,
      plugins: { legend: { labels: { color: '#DEE4EA' } } },
      scales: {
        x: { ticks: { color: '#9099A1' }, grid: { color: '#31393F' } },
        y: { ticks: { color: '#9099A1' }, grid: { color: '#31393F' } }
      }
    }
  };
  if (config.type === 'pie') { chartConfig.options.scales = {}; }
  canvas._chart = new Chart(ctx, chartConfig);
}

function buildChartData(config) {
  var labels = [];
  var datasets = [];
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
    datasets = [{ data: entries.map(function(e) { return e[1]; }), backgroundColor: CHART_COLORS.slice(0, entries.length) }];
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
  var xIdx = reportState.columns.findIndex(function(c) { return c.name === config.x_column; });
  var yIdx = reportState.columns.findIndex(function(c) { return c.name === config.y_column; });
  var gIdx = config.group_by ? reportState.columns.findIndex(function(c) { return c.name === config.group_by; }) : -1;
  if (xIdx < 0 || yIdx < 0) return {labels: [], datasets: []};
  if (config.group_by && gIdx >= 0) {
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
    datasets = [{ data: entries.map(function(e) { return e[1].sum / e[1].count; }), backgroundColor: CHART_COLORS[0] }];
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
