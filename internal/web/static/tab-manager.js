// Tab Manager for Multi-Tab Workflow UI — DS3 SQL Server
var tabState = {
  browse: { project: null, bucket: null, prefix: '', selectedFiles: [] },
  transform: { configs: {}, activeFile: null },
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

  if (tabName === 'analyze') renderAnalyzeTab();
  if (tabName === 'report') renderReportTab();
}

function updateTabBadges() {
  var browse = tabState.browse;
  var hasConvertible = browse.selectedFiles.some(function(f) {
    var l = f.toLowerCase();
    return l.endsWith('.log') || l.endsWith('.txt') || l.endsWith('.syslog') || l.endsWith('.out') || l.endsWith('.err');
  });

  var browseBadge = document.querySelector('.tab[data-tab="browse"] .tab-badge');
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
  var hasConvertible = browse.selectedFiles.some(function(f) {
    var l = f.toLowerCase();
    return l.endsWith('.log') || l.endsWith('.txt') || l.endsWith('.syslog') || l.endsWith('.out') || l.endsWith('.err');
  });
  if (hasConvertible) return 'transform';
  if (!tabState.query.results) return 'query';
  if (!tabState.analyze.analysisCache) return 'analyze';
  return 'report';
}

function navigateTo(step) {
  if (step) switchTab(step);
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
  var tab = window.location.hash.replace('#', '') || 'browse';
  if (['browse','transform','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  }
});

document.addEventListener('DOMContentLoaded', function() {
  var tab = window.location.hash.replace('#', '') || 'browse';
  if (['browse','transform','query','analyze','report'].indexOf(tab) >= 0) {
    switchTab(tab);
  } else {
    switchTab('browse');
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
