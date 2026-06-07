// Catalog browser — DS3 SQL Server. Renders datasets -> tables -> schema, and
// seeds the query editor when a table is clicked.
function switchCatalogProject(id) {
  tabState.browse.project = id;
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
          '<span style="display:inline-flex;gap:0.25rem;margin-left:0.5rem;">' +
          '<button class="btn btn-secondary" style="font-size:0.65rem;padding:0.1rem 0.4rem;" onclick="event.stopPropagation();promptRegisterTable(\'' + escJs(ds.name) + '\')">+ Table</button>' +
          '<button class="btn btn-secondary" style="font-size:0.65rem;padding:0.1rem 0.4rem;color:var(--red);" onclick="event.stopPropagation();deleteDataset(\'' + escJs(ds.name) + '\')">✕</button>' +
          '</span>' +
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
  // Show bucket browser in the detail panel for importing.
  showCatalogDatasetDetail(ds);
}

function showCatalogDatasetDetail(ds) {
  var title = document.getElementById('catalog-detail-title');
  if (title) title.textContent = ds + ' — Browse & Import';
  var c = document.getElementById('catalog-detail-content');
  if (!c) return;
  c.innerHTML =
    '<div style="margin-bottom:0.75rem;">' +
      '<div style="display:flex;gap:0.5rem;align-items:center;flex-wrap:wrap;">' +
        '<span style="font-weight:600;font-size:0.9rem;">Browse buckets</span>' +
        '<select id="cat-browse-bucket" class="input" style="flex:1;min-width:200px;" onchange="catBrowseBucket(this.value)"><option value="">Select bucket…</option></select>' +
      '</div>' +
      '<div id="cat-browse-files" style="margin-top:0.5rem;font-size:0.85rem;color:var(--text-muted);">Select a bucket to browse files you can register as tables.</div>' +
    '</div>' +
    '<div id="cat-region-info" style="font-size:0.82rem;color:var(--text-muted);border-top:0.0625rem solid var(--border);padding-top:0.75rem;">' +
      'Click <strong>+ Table</strong> next to the dataset name to register a path manually.' +
    '</div>';

  // Load bucket list
  if (tabState.browse.project) {
    fetch('/buckets?project=' + encodeURIComponent(tabState.browse.project))
      .then(function(r) { return r.json(); })
      .then(function(d) {
        if (d.error) return;
        var sel = document.getElementById('cat-browse-bucket');
        if (!sel) return;
        var html = '<option value="">Select bucket…</option>';
        (d.buckets || []).forEach(function(b) {
          html += '<option value="' + escAttr(b.name) + '">' + escHtml(b.name) + '</option>';
        });
        sel.innerHTML = html;
      });
  }
}

var catBrowsePrefix = '';

function catBrowseBucket(bucket) {
  catBrowsePrefix = '';
  catListAt(bucket, '');
}

function catListAt(bucket, prefix) {
  var filesEl = document.getElementById('cat-browse-files');
  if (!filesEl) return;
  filesEl.innerHTML = '<span style="color:var(--text-muted);">Loading…</span>';
  fetch('/buckets/' + encodeURIComponent(bucket) + '?project=' + encodeURIComponent(tabState.browse.project) + '&prefix=' + encodeURIComponent(prefix))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { filesEl.innerHTML = '<span style="color:var(--red);">' + escHtml(d.error) + '</span>'; return; }

      var html = '';
      // Breadcrumb
      if (prefix) {
        var parent = prefix.split('/').filter(Boolean).slice(0, -1).join('/');
        parent = parent ? parent + '/' : '';
        html += '<div style="margin-bottom:0.35rem;font-size:0.85rem;">' +
          '<a href="#" onclick="catListAt(\'' + escJs(bucket) + '\',\'\');return false;" style="color:var(--primary);">📁 ' + escHtml(bucket) + '</a>';
        var parts = prefix.split('/').filter(Boolean);
        var cumulative = '';
        parts.forEach(function(p) {
          cumulative += p + '/';
          html += ' / <a href="#" onclick="catListAt(\'' + escJs(bucket) + '\',\'' + escJs(cumulative) + '\');return false;" style="color:var(--primary);">' + escHtml(p) + '</a>';
        });
        html += '</div>';
      } else {
        html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.35rem;">📁 ' + escHtml(bucket) + '</div>';
      }

      // Subdirectories
      (d.prefixes || []).forEach(function(p) {
        var name = p.replace(/\/$/, '').split('/').pop();
        html += '<div onclick="catListAt(\'' + escJs(bucket) + '\',\'' + escJs(p) + '\')" style="cursor:pointer;padding:0.25rem;font-size:0.85rem;">📁 ' + escHtml(name) + '/</div>';
      });

      // Files — all queryable formats
      var files = (d.objects || []).filter(function(o) {
        var l = o.key.toLowerCase();
        return l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv');
      });
      if (files.length) {
        html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-top:0.35rem;margin-bottom:0.25rem;">' + files.length + ' file(s) — click +Table to register:</div>';
        files.forEach(function(o) {
          html += '<div style="display:flex;align-items:center;justify-content:space-between;padding:0.3rem 0.25rem;border-bottom:0.0625rem solid var(--border);font-size:0.85rem;">' +
            '<span>' + escHtml(o.key.split('/').pop()) + ' <span style="color:var(--text-muted);font-size:0.75rem;">(' + fmtSize(o.size) + ')</span></span>' +
            '<button class="btn btn-secondary" style="font-size:0.7rem;padding:0.1rem 0.5rem;" onclick="catalogRegisterFromBrowse(\'' + escJs(bucket) + '\',\'' + escJs(o.key) + '\')">+ Table</button>' +
            '</div>';
        });
      }

      if (!html) html = '<span style="color:var(--text-muted);">Empty.</span>';
      filesEl.innerHTML = html;
    });
}

function catalogRegisterFromBrowse(bucket, key) {
  var ds = document.getElementById('catalog-detail-title');
  var dataset = ds ? ds.textContent.replace(' — Browse & Import', '') : '';
  if (!dataset) { alert('Select a dataset first'); return; }
  // Default to parent directory glob so new files are automatically included.
  var ext = key.split('.').pop();
  var parentKey = key.split('/').slice(0, -1).join('/');
  var globPath = parentKey ? 's3://' + bucket + '/' + parentKey + '/*.' + ext : 's3://' + bucket + '/*.' + ext;
  var defaultName = (parentKey.split('/').pop() || bucket).replace(/[^a-zA-Z0-9_]/g, '_');
  showModal(
    '<h3 style="margin:0 0 1rem 0;font-size:1rem;">Register Table in ' + escHtml(dataset) + '</h3>' +
    '<div style="display:flex;flex-direction:column;gap:0.75rem;">' +
    '<label><span style="font-size:0.85rem;color:var(--text-muted);">Table name</span><input id="reg-name" class="input" value="' + escAttr(defaultName) + '" style="width:100%;margin-top:0.25rem;"></label>' +
    '<label><span style="font-size:0.85rem;color:var(--text-muted);">Path (<code>*</code> matches all files)</span><input id="reg-location" class="input" value="' + escAttr(globPath) + '" style="width:100%;margin-top:0.25rem;"></label>' +
    '<div style="font-size:0.8rem;color:var(--text-muted);background:var(--surface-2);padding:0.5rem;border-radius:var(--radius);line-height:1.4;">' +
    '✅ Path ends with <code>*.' + escHtml(ext) + '</code> — all <code>.' + escHtml(ext) + '</code> files in this directory are read at query time. ' +
    'Drop a new file and <code>SELECT * FROM table</code> will see it immediately — no registration needed.' +
    '</div>' +
    '<div><span style="font-size:0.85rem;color:var(--text-muted);">Format (auto-detected if unsure)</span>' +
    '<div style="display:flex;gap:1rem;margin-top:0.35rem;flex-wrap:wrap;">' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="auto" checked> Auto</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="parquet"> Parquet</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="csv"> CSV</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="json"> JSON</label>' +
    '</div></div>' +
    '<div id="reg-error" style="color:var(--red);font-size:0.85rem;display:none;"></div>' +
    '<div style="display:flex;gap:0.5rem;justify-content:flex-end;margin-top:0.25rem;">' +
    '<button class="btn btn-secondary" onclick="this.closest(\'#catalog-modal\').remove()">Cancel</button>' +
    '<button class="btn" id="reg-submit" onclick="submitRegisterTable(\'' + escJs(dataset) + '\')">Register</button>' +
    '</div></div>'
  );
  document.getElementById('reg-name').focus();
}

function showModal(html) {
  var existing = document.getElementById('catalog-modal');
  if (existing) existing.remove();
  var overlay = document.createElement('div');
  overlay.id = 'catalog-modal';
  overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,0.4);z-index:1000;display:flex;align-items:center;justify-content:center;';
  overlay.innerHTML = '<div style="background:var(--surface);border:0.0625rem solid var(--border);border-radius:var(--radius);padding:1.25rem;min-width:380px;max-width:500px;box-shadow:0 8px 24px rgba(0,0,0,0.15);">' + html + '</div>';
  overlay.addEventListener('click', function(e) { if (e.target === overlay) overlay.remove(); });
  document.body.appendChild(overlay);
  return overlay;
}

function promptRegisterTable(dataset) {
  if (!tabState.browse.project) { alert('Select a project first'); return; }
  var modal = showModal(
    '<h3 style="margin:0 0 1rem 0;font-size:1rem;">Register Table in ' + escHtml(dataset) + '</h3>' +
    '<div style="display:flex;flex-direction:column;gap:0.75rem;">' +
    '<label><span style="font-size:0.85rem;color:var(--text-muted);">Table name</span><input id="reg-name" class="input" placeholder="my_table" style="width:100%;margin-top:0.25rem;"></label>' +
    '<label><span style="font-size:0.85rem;color:var(--text-muted);">S3 path or glob</span><input id="reg-location" class="input" placeholder="s3://my-bucket/path/*.parquet" style="width:100%;margin-top:0.25rem;"></label>' +
    '<div><span style="font-size:0.85rem;color:var(--text-muted);">Format (auto-detected if unsure)</span>' +
    '<div style="display:flex;gap:1rem;margin-top:0.35rem;flex-wrap:wrap;">' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="auto" checked> Auto</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="parquet"> Parquet</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="csv"> CSV</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="json"> JSON</label>' +
    '<label style="display:flex;align-items:center;gap:0.3rem;font-size:0.85rem;cursor:pointer;"><input type="radio" name="reg-format" value="tsv"> TSV</label>' +
    '</div></div>' +
    '<div id="reg-error" style="color:var(--red);font-size:0.85rem;display:none;"></div>' +
    '<div style="display:flex;gap:0.5rem;justify-content:flex-end;margin-top:0.25rem;">' +
    '<button class="btn btn-secondary" onclick="this.closest(\'#catalog-modal\').remove()">Cancel</button>' +
    '<button class="btn" id="reg-submit" onclick="submitRegisterTable(\'' + escJs(dataset) + '\')">Register</button>' +
    '</div></div>'
  );
  document.getElementById('reg-name').focus();
}

function submitRegisterTable(dataset) {
  var name = document.getElementById('reg-name').value.trim();
  var location = document.getElementById('reg-location').value.trim();
  var formatEls = document.getElementsByName('reg-format');
  var format = 'auto';
  for (var i = 0; i < formatEls.length; i++) { if (formatEls[i].checked) { format = formatEls[i].value; break; } }
  var errEl = document.getElementById('reg-error');
  var btn = document.getElementById('reg-submit');
  if (!name) { errEl.textContent = 'Table name is required'; errEl.style.display = 'block'; return; }
  if (!location) { errEl.textContent = 'S3 path is required'; errEl.style.display = 'block'; return; }
  errEl.style.display = 'none';
  btn.disabled = true;
  btn.textContent = 'Registering…';
  var body = {name: name, location: location};
  if (format !== 'auto') body.format = format;
  fetch('/datasets/' + encodeURIComponent(dataset) + '/tables?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body)
  })
  .then(function(r) {
    if (!r.ok) {
      return r.text().then(function(text) {
        var msg = text;
        try { var j = JSON.parse(text); if (j.error) msg = j.error; } catch(e) {}
        throw new Error(msg);
      });
    }
    return r.json();
  })
  .then(function(d) {
    // Success — close modal, show toast, reload tree
    var modal = document.getElementById('catalog-modal');
    if (modal) modal.remove();
    showToast('Table "' + name + '" registered · ' + (d.format || format) + ' · ' + ((d.stats && d.stats.row_count) || 0) + ' rows');
    var ul = document.getElementById('ds-' + dataset);
    if (ul) { ul.dataset.loaded = ''; ul.style.display = 'block'; loadTablesForDataset(dataset, ul); }
  })
  .catch(function(e) { errEl.textContent = e.message; errEl.style.display = 'block'; btn.disabled = false; btn.textContent = 'Register'; });
}

function showToast(msg) {
  var el = document.createElement('div');
  el.textContent = msg;
  el.style.cssText = 'position:fixed;bottom:1.5rem;right:1.5rem;background:#27B681;color:#fff;padding:0.75rem 1.25rem;border-radius:var(--radius);font-size:0.85rem;z-index:1100;box-shadow:0 4px 12px rgba(0,0,0,0.15);max-width:360px;';
  document.body.appendChild(el);
  setTimeout(function() { el.style.opacity = '0'; el.style.transition = 'opacity 0.4s'; setTimeout(function() { el.remove(); }, 500); }, 3000);
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
        html += '<li class="catalog-table" data-dataset="' + escAttr(ds) + '" data-table="' + escAttr(t.name) + '">' +
          '<span onclick="selectCatalogTable(\'' + escJs(ds) + '\',\'' + escJs(t.name) + '\')" style="cursor:pointer;flex:1;">' +
          '<span class="catalog-table-name">' + escHtml(t.name) + '</span> ' +
          '<span class="catalog-table-meta">' + escHtml(t.format || '') + ' · ' + ((t.stats && t.stats.row_count) || 0) + ' rows</span></span>' +
          '<button class="btn btn-secondary" style="font-size:0.6rem;padding:0.05rem 0.3rem;color:var(--red);flex-shrink:0;" onclick="event.stopPropagation();deleteTable(\'' + escJs(ds) + '\',\'' + escJs(t.name) + '\',false)">✕</button></li>';
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
  var modal = showModal(
    '<h3 style="margin:0 0 1rem 0;font-size:1rem;">New Dataset</h3>' +
    '<div style="display:flex;flex-direction:column;gap:0.75rem;">' +
    '<label><span style="font-size:0.85rem;color:var(--text-muted);">Dataset name</span><input id="ds-name" class="input" placeholder="my_dataset" style="width:100%;margin-top:0.25rem;"></label>' +
    '<div id="ds-error" style="color:var(--red);font-size:0.85rem;display:none;"></div>' +
    '<div style="display:flex;gap:0.5rem;justify-content:flex-end;">' +
    '<button class="btn btn-secondary" onclick="this.closest(\'#catalog-modal\').remove()">Cancel</button>' +
    '<button class="btn" id="ds-submit" onclick="submitNewDataset()">Create</button>' +
    '</div></div>'
  );
  document.getElementById('ds-name').focus();
}

function submitNewDataset() {
  var name = document.getElementById('ds-name').value.trim();
  var errEl = document.getElementById('ds-error');
  var btn = document.getElementById('ds-submit');
  if (!name) { errEl.textContent = 'Dataset name is required'; errEl.style.display = 'block'; return; }
  errEl.style.display = 'none';
  btn.disabled = true;
  btn.textContent = 'Creating…';
  fetch('/datasets?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({name: name})
  })
  .then(function(r) {
    if (!r.ok) { return r.text().then(function(t) { var m = t; try { var j = JSON.parse(t); if (j.error) m = j.error; } catch(e) {} throw new Error(m); }); }
    return r.json();
  })
  .then(function() {
    var modal = document.getElementById('catalog-modal');
    if (modal) modal.remove();
    showToast('Dataset "' + name + '" created');
    loadCatalogTree();
  })
  .catch(function(e) { errEl.textContent = e.message; errEl.style.display = 'block'; btn.disabled = false; btn.textContent = 'Create'; });
}

function deleteDataset(name) {
  if (!confirm('Delete dataset "' + name + '" and all its tables?')) return;
  // Tables need to be deleted first (the API doesn't cascade).
  var ul = document.getElementById('ds-' + name);
  if (ul) {
    var tables = ul.querySelectorAll('.catalog-table');
    tables.forEach(function(el) {
      var tableName = el.getAttribute('data-table');
      if (tableName) deleteTable(name, tableName, true);
    });
  }
  // Delete the dataset itself.
  fetch('/datasets/' + encodeURIComponent(name) + '?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'DELETE'
  })
  .then(function(r) {
    if (!r.ok) return r.text().then(function(t) { var m = t; try { var j = JSON.parse(t); if (j.error) m = j.error; } catch(e) {} throw new Error(m); });
    showToast('Dataset "' + name + '" deleted');
    loadCatalogTree();
  })
  .catch(function(e) { alert('Error deleting dataset: ' + e.message); });
}

function deleteTable(dataset, table, silent) {
  fetch('/datasets/' + encodeURIComponent(dataset) + '/tables/' + encodeURIComponent(table) + '?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'DELETE'
  })
  .then(function(r) {
    if (!r.ok) return r.text().then(function(t) { var m = t; try { var j = JSON.parse(t); if (j.error) m = j.error; } catch(e) {} throw new Error(m); });
    if (!silent) {
      showToast('Table "' + table + '" deleted');
      var ul = document.getElementById('ds-' + dataset);
      if (ul) { ul.dataset.loaded = ''; ul.style.display = 'block'; loadTablesForDataset(dataset, ul); }
    }
  })
  .catch(function(e) { if (!silent) alert('Error: ' + e.message); });
}

function fmtSize(b) {
  if (!b) return '0B';
  var u = ['B','KB','MB','GB','TB'], i = Math.floor(Math.log(b) / Math.log(1024));
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i];
}
