// Tab Manager for Multi-Tab Workflow UI — DS3 SQL Server
var tabState = {
  catalog: { project: null, selectedTable: null },
  browse: { project: null, bucket: null, prefix: '', selectedFiles: [] },
  transform: { configs: {}, activeFile: null, pendingBucket: null, pendingFile: null },
  query: { sql: '', results: null, currentPage: 0, pageSize: 100 },
  analyze: { analysisCache: null, selectedCols: [] },
  report: { title: '', charts: [], savedId: null }
};

function switchTab(tabName) {
  document.querySelectorAll('.tab-content').forEach(function(el) {
    el.style.display = 'none';
  });
  var tabContent = document.getElementById('tab-' + tabName);
  if (tabContent) tabContent.style.display = 'block';

  document.querySelectorAll('.tab-bar .tab').forEach(function(t) {
    t.classList.remove('active');
  });
  var tabEl = document.querySelector('.tab-bar .tab[data-tab="' + tabName + '"]');
  if (tabEl) tabEl.classList.add('active');

  window.location.hash = tabName;
  updateTabBadges();

  if (tabName === 'catalog' && typeof loadCatalogTree === 'function') loadCatalogTree();
  if (tabName === 'analyze') renderAnalyzeTab();
  if (tabName === 'report') renderReportTab();
  if (tabName === 'transform') renderTransformTab();
  if (tabName === 'query' && typeof loadJobsPanel === 'function') loadJobsPanel();
}

function renderTransformTab() {
  var list = document.getElementById('transform-file-list');
  var configArea = document.getElementById('transform-config-area');
  if (!list) return;

  var files = tabState.browse.selectedFiles;
  var convertible = files.filter(function(p) { return !isQueryable(p); });

  if (!convertible.length && !tabState.transform.pendingFile) {
    list.innerHTML = '<p style="color:var(--text-muted);">No convertible files selected. <a href="#" onclick="switchTab(\'buckets\');return false;">Go to Buckets</a> to select files.</p>';
    if (configArea) configArea.style.display = 'none';
    return;
  }

  var html = '<div style="font-size:0.85rem;margin-bottom:0.5rem;color:var(--text-muted);">Selected files to convert:</div>' +
    '<ul style="font-size:0.8rem;color:var(--text);">';
  convertible.forEach(function(p) {
    var name = p.split('/').pop();
    html += '<li style="margin-bottom:0.15rem;">' + escHtml(name) + '</li>';
  });
  html += '</ul>';
  html += '<div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.5rem;">Configure column parsing below, then click Save & Convert.</div>';
  list.innerHTML = html;

  // Determine which file to configure
  var bucket = tabState.browse.bucket || tabState.transform.pendingBucket;
  var fileKey = tabState.transform.pendingFile;
  if (!fileKey && convertible.length) {
    var firstPath = convertible[0];
    fileKey = firstPath.replace(/^s3:\/\/[^\/]+\//, '');
  }

  if (fileKey && typeof loadPreview === 'function' && bucket) {
    if (configArea) configArea.style.display = 'block';
    configArea.innerHTML = '<div id="config-app"></div>';
    loadPreview(bucket, fileKey);
    tabState.transform.pendingBucket = null;
    tabState.transform.pendingFile = null;
  }
}

function configureFile(bucket, fileKey) {
  // Ensure file is selected
  var s3path = 's3://' + bucket + '/' + fileKey;
  if (tabState.browse.selectedFiles.indexOf(s3path) < 0) {
    tabState.browse.selectedFiles.push(s3path);
    updateTabBadges();
    if (typeof updateBadge === 'function') updateBadge();
    if (typeof updateBrowseActions === 'function') updateBrowseActions();
  }
  // Set pending for transform tab
  tabState.transform.pendingBucket = bucket;
  tabState.transform.pendingFile = fileKey;
  switchTab('transform');
}

function isQueryable(f) {
  var l = f.toLowerCase();
  return l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv');
}

function updateTabBadges() {
  var browse = tabState.browse;
  var hasConvertible = browse.selectedFiles.some(function(f) { return !isQueryable(f); });

  var browseBadge = document.querySelector('.tab[data-tab="buckets"] .tab-badge');
  if (browseBadge) browseBadge.textContent = browse.selectedFiles.length || '';

  var transformBadge = document.querySelector('.tab[data-tab="transform"] .tab-badge');
  if (transformBadge) {
    if (hasConvertible && browse.selectedFiles.length) {
      transformBadge.textContent = '!';
      transformBadge.classList.add('active');
    } else {
      transformBadge.textContent = browse.selectedFiles.length ? '\u2713' : '';
      transformBadge.classList.remove('active');
    }
  }

  var queryBadge = document.querySelector('.tab[data-tab="query"] .tab-badge');
  if (queryBadge) queryBadge.textContent = tabState.query.results ? '\u2713' : '';

  var analyzeBadge = document.querySelector('.tab[data-tab="analyze"] .tab-badge');
  if (analyzeBadge) analyzeBadge.textContent = tabState.analyze.analysisCache ? '\u2713' : '';

  var reportBadge = document.querySelector('.tab[data-tab="report"] .tab-badge');
  if (reportBadge) reportBadge.textContent = tabState.report.charts.length ? String(tabState.report.charts.length) : '';
}

function getNextStep() {
  var browse = tabState.browse;
  if (!browse.selectedFiles.length) return null;
  var hasConvertible = browse.selectedFiles.some(function(f) { return !isQueryable(f); });
  if (hasConvertible) return 'transform';
  if (!tabState.query.results) return 'query';
  if (!tabState.analyze.analysisCache) return 'analyze';
  return 'report';
}

function navigateToTab(tabName) {
  // Works from any page — uses switchTab on /app, navigates there otherwise
  if (window.location.pathname === '/app') {
    switchTab(tabName);
  } else {
    window.location.href = '/app#' + tabName;
  }
}

function resetDownstreamTabs(from) {
  var order = ['browse','transform','query','analyze','report'];
  var idx = order.indexOf(from);
  if (idx < 0) return;
  for (var i = idx + 1; i < order.length; i++) {
    var t = order[i];
    switch (t) {
      case 'transform': tabState.transform = { configs: {}, activeFile: null }; break;
      case 'query': tabState.query = { sql: '', results: null, currentPage: 0, pageSize: 100 }; break;
      case 'analyze': tabState.analyze = { analysisCache: null, selectedCols: [] }; break;
      case 'report': tabState.report = { title: '', charts: [], savedId: null }; break;
    }
  }
  updateTabBadges();
}

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

// Shared helpers used across browse.js, query.js, report.js, column_config.js
function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function escJs(s) { return s.replace(/'/g,"\\'").replace(/"/g,'\\"'); }
function escAttr(s) { return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;'); }
function escCsv(s) {
  if (s.indexOf(',') >= 0 || s.indexOf('"') >= 0 || s.indexOf('\n') >= 0) {
    return '"' + s.replace(/"/g, '""') + '"';
  }
  return s;
}
