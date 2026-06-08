// Tab Manager for Multi-Tab Workflow UI — DS3 SQL Server

// Global fetch interceptor: on 401, attempt a token refresh and retry once.
var _authRefreshing = null;
var _origFetch = window.fetch;
function _authFetch(url, opts) {
  opts = opts || {};
  // Don't intercept auth endpoints to avoid recursion.
  if (typeof url === 'string' && url.indexOf('/auth/') >= 0) {
    return _origFetch(url, opts);
  }
  return _origFetch(url, opts).then(function(res) {
    if (res.status !== 401) return res;
    // Token expired — try refreshing once.
    if (_authRefreshing) return _authRefreshing.then(function() { return _authFetch(url, opts); });
    _authRefreshing = _origFetch('/auth/refresh', {method: 'POST', credentials: 'same-origin'}).then(function(r) {
      _authRefreshing = null;
      if (!r.ok) { window.location.href = '/login'; return null; }
      return r;
    });
    return _authRefreshing.then(function() { if (document.body) return _authFetch(url, opts); });
  });
}
window.fetch = function(url, opts) { return _authFetch(url, opts); };

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
  if (tabName === 'transform') {
    var sel = document.getElementById('tf-project-select');
    if (sel && tabState.browse.project && sel.value !== tabState.browse.project) {
      sel.value = tabState.browse.project;
      tfSwitchProject(tabState.browse.project);
    }
    renderTransformTab();
  }
  if (tabName === 'analyze') renderAnalyzeTab();
  if (tabName === 'report') renderReportTab();
  if (tabName === 'transform') renderTransformTab();
  if (tabName === 'query' && typeof loadJobsPanel === 'function') loadJobsPanel();
  if (tabName === 'schedules' && typeof renderSchedulesTab === 'function') renderSchedulesTab();
}

function renderTransformTab() {
  var list = document.getElementById('transform-file-list');
  var configArea = document.getElementById('transform-config-area');
  if (!list) return;

  var files = tabState.browse.selectedFiles;
  var convertible = files.filter(function(p) { return !isQueryable(p); });

  if (!convertible.length && !tabState.transform.pendingFile) {
    list.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No files selected for conversion. Use the browser on the left to find and click files.</p>';
    if (configArea) configArea.style.display = 'none';
    document.getElementById('tf-schedule-area').style.display = 'none';
    document.getElementById('tf-postaction-area').style.display = 'none';
    return;
  }

  var html = '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.5rem;">' + convertible.length + ' file(s) to convert:</div>' +
    '<ul style="font-size:0.8rem;color:var(--text);margin:0;padding-left:1rem;">';
  convertible.forEach(function(p) {
    var name = p.split('/').pop();
    html += '<li style="margin-bottom:0.15rem;">' + escHtml(name) + '</li>';
  });
  html += '</ul>';
  html += '<button class="btn btn-secondary" style="font-size:0.8rem;margin-top:0.5rem;" onclick="tabState.browse.selectedFiles = []; renderTransformTab(); updateTabBadges();">Clear</button>';
  html += '<div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.75rem;">Configure column parsing below, then click Save & Convert.</div>';
  list.innerHTML = html;

  // Show schedule and post-action areas
  var scheduleArea = document.getElementById('tf-schedule-area');
  var postactionArea = document.getElementById('tf-postaction-area');
  if (scheduleArea) scheduleArea.style.display = 'block';
  if (postactionArea) postactionArea.style.display = 'block';

  // Determine which file to configure
  var bucket = '';
  var fileKey = tabState.transform.pendingFile;
  if (!fileKey && convertible.length) {
    var firstPath = convertible[0];
    var parts = firstPath.replace(/^s3:\/\//, '').split('/');
    bucket = parts[0];
    fileKey = parts.slice(1).join('/');
  }

  if (fileKey && bucket && typeof loadPreview === 'function') {
    if (configArea) configArea.style.display = 'block';
    configArea.innerHTML = '<div id="config-app"></div>';
    loadPreview(bucket, fileKey);
    tabState.transform.pendingBucket = null;
    tabState.transform.pendingFile = null;
  }
}

function configureFile(bucket, fileKey) {
  if (typeof tfConfigure === 'function') {
    tfConfigure(bucket, fileKey);
  } else {
    var s3path = 's3://' + bucket + '/' + fileKey;
    if (tabState.browse.selectedFiles.indexOf(s3path) < 0) {
      tabState.browse.selectedFiles.push(s3path);
      updateTabBadges();
    }
    tabState.transform.pendingBucket = bucket;
    tabState.transform.pendingFile = fileKey;
  }
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
  if (['catalog','transform','schedules','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  }
});

document.addEventListener('DOMContentLoaded', function() {
  var tab = window.location.hash.replace('#', '') || 'catalog';
  if (['catalog','transform','schedules','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  } else {
    switchTab('catalog');
  }
});

// When switching to buckets, sync the project selector and auto-load buckets.
document.addEventListener('DOMContentLoaded', function() {
  // If a project was previously selected (from catalog tab), sync buckets selector.
  var sel = document.getElementById('project-select');
  if (sel && tabState.browse.project && sel.value !== tabState.browse.project) {
    sel.value = tabState.browse.project;
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
