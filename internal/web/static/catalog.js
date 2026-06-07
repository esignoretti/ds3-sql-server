// Catalog browser — DS3 SQL Server. Renders datasets -> tables -> schema, and
// seeds the query editor when a table is clicked.
function switchCatalogProject(id) {
  tabState.browse.project = id;
  // keep the buckets tab's project selector in sync if present
  var bsel = document.getElementById('project-select');
  if (bsel) bsel.value = id;
  // If the buckets tab content is still showing the placeholder,
  // also sync its project and load buckets
  var content = document.getElementById('browser-content');
  if (content && content.textContent.indexOf('Select a project first') >= 0 && typeof showBuckets === 'function') {
    showBuckets();
  }
  loadCatalogTree();
}

function loadCatalogTree() {
  var content = document.getElementById('catalog-tree-content');
  if (!content) return;
  if (!tabState.browse.project) {
    content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project first.</p>';
    return;
  }
  content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Loading…</p>';
  fetch('/datasets?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { content.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var datasets = d.datasets || [];
      if (!datasets.length) {
        content.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No datasets. Click "+ Dataset".</p>';
        return;
      }
      var html = '<ul class="catalog-tree">';
      datasets.forEach(function(ds) {
        var dsId = 'ds-' + escAttr(ds.name);
        html += '<li class="catalog-ds">' +
          '<div class="catalog-ds-name">' +
          '<span onclick="toggleDataset(\'' + escJs(ds.name) + '\')">▸ ' + escHtml(ds.name) + '</span>' +
          '<button class="btn btn-secondary" style="font-size:0.65rem;padding:0.1rem 0.4rem;margin-left:0.5rem;" onclick="event.stopPropagation();promptRegisterTable(\'' + escJs(ds.name) + '\')">+ Table</button>' +
          '</div>' +
          '<ul class="catalog-tables" id="' + dsId + '" style="display:none;"></ul></li>';
      });
      html += '</ul>';
      content.innerHTML = html;
    })
    .catch(function(e) { content.innerHTML = '<p style="color:var(--red);">Error: ' + escHtml(e.message) + '</p>'; });
}

function toggleDataset(ds) {
  var ul = document.getElementById('ds-' + ds);
  if (!ul) return;
  if (ul.style.display === 'none') {
    ul.style.display = 'block';
    if (!ul.dataset.loaded) loadTablesForDataset(ds, ul);
  } else {
    ul.style.display = 'none';
  }
}

function promptRegisterTable(dataset) {
  if (!tabState.browse.project) { alert('Select a project first'); return; }
  // Build the register URL with the current project
  var baseUrl = '/datasets/' + encodeURIComponent(dataset) + '/tables?project=' + encodeURIComponent(tabState.browse.project);
  var name = prompt('Table name (letters, digits, underscore):');
  if (!name) return;
  var location = prompt('S3 path or glob, e.g. s3://my-bucket/path/*.parquet:');
  if (!location) return;
  var format = prompt('Format (parquet, csv, json, tsv):', 'parquet');
  if (!format) return;
  fetch(baseUrl, {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({name: name, location: location, format: format})
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { alert(d.error); return; }
    // Reload tables for this dataset — ensure visible
    var ul = document.getElementById('ds-' + dataset);
    if (ul) {
      ul.dataset.loaded = '';
      ul.style.display = 'block';
      loadTablesForDataset(dataset, ul);
    }
  });
}

function loadTablesForDataset(ds, ul) {
  ul.innerHTML = '<li class="catalog-table-empty">Loading…</li>';
  fetch('/datasets/' + encodeURIComponent(ds) + '/tables?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { ul.innerHTML = '<li class="catalog-table-empty">' + escHtml(d.error) + '</li>'; return; }
      var tables = d.tables || [];
      if (!tables.length) { ul.innerHTML = '<li class="catalog-table-empty">no tables</li>'; return; }
      var html = '';
      tables.forEach(function(t) {
        html += '<li class="catalog-table" data-dataset="' + escAttr(ds) + '" data-table="' + escAttr(t.name) + '" ' +
          'onclick="selectCatalogTable(\'' + escJs(ds) + '\',\'' + escJs(t.name) + '\')">' +
          '<span class="catalog-table-name">' + escHtml(t.name) + '</span> ' +
          '<span class="catalog-table-meta">' + escHtml(t.format || '') + ' · ' + ((t.stats && t.stats.row_count) || 0) + ' rows</span></li>';
      });
      ul.innerHTML = html;
      ul.dataset.loaded = '1';
    })
    .catch(function(e) { ul.innerHTML = '<li class="catalog-table-empty">Error: ' + escHtml(e.message) + '</li>'; });
}

function selectCatalogTable(ds, table) {
  // Highlight selection
  document.querySelectorAll('.catalog-table.selected').forEach(function(el) { el.classList.remove('selected'); });
  var el = document.querySelector('.catalog-table[data-dataset="' + ds + '"][data-table="' + table + '"]');
  if (el) el.classList.add('selected');

  fetch('/datasets/' + encodeURIComponent(ds) + '/tables/' + encodeURIComponent(table) +
        '?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(t) {
      if (t.error) { return; }
      renderTableDetail(t);
      seedQueryEditor(ds, table);
    });
}

function renderTableDetail(t) {
  var title = document.getElementById('catalog-detail-title');
  if (title) title.textContent = t.dataset + '.' + t.name;
  var c = document.getElementById('catalog-detail-content');
  if (!c) return;
  var html = '<div style="font-size:0.8rem;color:var(--text-muted);margin-bottom:0.5rem;">' +
    escHtml(t.format || '') + ' · ' + escHtml(t.storage_class || '') + ' · ' + ((t.stats && t.stats.row_count) || 0) + ' rows</div>';
  html += '<table class="catalog-schema"><thead><tr><th>Column</th><th>Type</th></tr></thead><tbody>';
  (t.schema || []).forEach(function(col) {
    html += '<tr><td>' + escHtml(col.name) + '</td><td>' + escHtml(col.type) + '</td></tr>';
  });
  html += '</tbody></table>';
  html += '<button class="btn" style="margin-top:0.75rem;" onclick="seedQueryEditor(\'' + escJs(t.dataset) + '\',\'' + escJs(t.name) + '\');navigateToTab(\'query\');">▶ Query this table</button>';
  c.innerHTML = html;
}

function seedQueryEditor(ds, table) {
  var ed = document.getElementById('sql-editor');
  if (ed) ed.value = 'SELECT * FROM ' + ds + '.' + table + ' LIMIT 100';
  var badge = document.getElementById('query-source-badge');
  if (badge) badge.textContent = ds + '.' + table;
  // A fresh table selection invalidates any prior query/analysis/report.
  if (typeof resetDownstreamTabs === 'function') resetDownstreamTabs('browse');
}

function newDatasetPrompt() {
  if (!tabState.browse.project) { alert('Select a project first'); return; }
  var name = prompt('New dataset name (letters, digits, underscore):');
  if (!name) return;
  fetch('/datasets?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({name: name})
  })
  .then(function(r) { return r.json(); })
  .then(function(d) {
    if (d.error) { alert(d.error); return; }
    loadCatalogTree();
  });
}
