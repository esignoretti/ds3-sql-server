// Browse tab — file browsing and selection functions

function switchProject(id) {
  if (!id) return;
  tabState.browse.project = id;
  tabState.browse.bucket = '';
  tabState.browse.selectedFiles = [];
  resetDownstreamTabs('browse');
  document.getElementById('breadcrumb').textContent = 'Loading buckets...';
  document.getElementById('browser-content').innerHTML = '<p style="color:var(--text-muted);">Loading...</p>';
  fetch('/buckets?project=' + encodeURIComponent(id))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">' + d.error + '</p>'; return; }
      if (!d.buckets || !d.buckets.length) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--text-muted);">No buckets</p>'; return; }
      document.getElementById('breadcrumb').innerHTML = '<a href="#" onclick="showBuckets();return false;" style="color:var(--primary);text-decoration:none;">Buckets</a>';
      var html = '';
      d.buckets.forEach(function(b) { html += '<div class="bucket-item" onclick="loadPrefix(\'' + escJs(b.name) + '\',\'\')">📁 ' + escHtml(b.name) + '</div>'; });
      html += '<div style="margin-top:0.75rem;border-top:0.0625rem solid var(--border);padding-top:0.75rem;">';
      html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.25rem;">Or enter a bucket name:</div>';
      html += '<div style="display:flex;gap:0.5rem;"><input type="text" id="manual-bucket" class="input" placeholder="my-bucket" style="flex:1;">';
      html += '<button class="btn btn-secondary" onclick="manualBucket()">Open</button></div></div>';
      document.getElementById('browser-content').innerHTML = html;
    })
    .catch(function(e) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function loadPrefix(bucket, prefix) {
  tabState.browse.bucket = bucket;
  var crumb = document.getElementById('breadcrumb');
  crumb.innerHTML = '<a href="#" onclick="showBuckets();return false;" style="color:var(--primary);text-decoration:none;">' + escHtml(tabState.browse.project) + '</a> / ' + (prefix ? escHtml(prefix) : escHtml(bucket));
  document.getElementById('browser-content').innerHTML = '<p style="color:var(--text-muted);">Loading...</p>';
  fetch('/buckets/' + encodeURIComponent(bucket) + '?project=' + encodeURIComponent(tabState.browse.project) + '&prefix=' + encodeURIComponent(prefix))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">' + d.error + '</p>'; return; }
      var html = '';
      d.prefixes.forEach(function(p) {
        var name = p.replace(/\/$/,'').split('/').pop();
        html += '<div class="bucket-item" onclick="loadPrefix(\'' + escJs(bucket) + '\',\'' + escJs(p) + '\')">📁 ' + escHtml(name) + '</div>';
      });
      var supported = d.objects.filter(function(o) { var l = o.key.toLowerCase(); return l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv'); });
      var convertibles = d.objects.filter(function(o) {
        var l = o.key.toLowerCase();
        return !(l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv'));
      });
      if (supported.length) {
        html += '<div style="margin-top:0.5rem;font-size:0.85rem;color:var(--text-muted);">Queryable files — click to add:</div>';
        supported.forEach(function(o) {
          var s3path = 's3://' + bucket + '/' + o.key;
          var isSelected = tabState.browse.selectedFiles.indexOf(s3path) >= 0;
          html += '<div class="bucket-item" onclick="togglePath(\'' + escAttr(s3path) + '\',this)">' + (isSelected ? '☑' : '☐') + ' ' + escHtml(o.key.split('/').pop()) + ' <span style="color:var(--text-muted);font-size:0.75rem;">' + fmtSize(o.size) + '</span></div>';
        });
      }
      if (convertibles.length) {
        html += '<div style="margin-top:0.5rem;font-size:0.85rem;color:var(--text-muted);">Convertible files — select to convert:</div>';
        convertibles.forEach(function(o) {
          var s3path = 's3://' + bucket + '/' + o.key;
          var isSelected = tabState.browse.selectedFiles.indexOf(s3path) >= 0;
          html += '<label class="convert-item" style="display:flex;align-items:center;gap:0.375rem;padding:0.25rem 0.5rem;font-size:0.85rem;cursor:pointer;">';
          html += '<input type="checkbox" class="convert-checkbox" data-file="' + escAttr(o.key) + '" ' + (isSelected ? 'checked' : '') + ' onchange="togglePath(\'' + escAttr(s3path) + '\',this.parentElement)">';
          html += escHtml(o.key.split('/').pop()) + ' <span style="color:var(--text-muted);font-size:0.75rem;">' + fmtSize(o.size) + '</span>';
          html += ' <a href="#" onclick="configureFile(\'' + escJs(bucket) + '\',\'' + escJs(o.key) + '\');return false;" style="color:var(--primary);font-size:0.75rem;text-decoration:none;cursor:pointer;">[configure]</a>';
          html += '</label>';
        });
      }
      if (!supported.length && !convertibles.length && !d.prefixes.length) { html = '<p style="color:var(--text-muted);">No files</p>'; }
      document.getElementById('browser-content').innerHTML = html;
    })
    .catch(function(e) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function showBuckets() {
  if (!tabState.browse.project) return;
  tabState.browse.bucket = '';
  tabState.browse.selectedFiles = [];
  resetDownstreamTabs('browse');
  document.getElementById('breadcrumb').innerHTML = '<a href="#" onclick="showBuckets();return false" style="color:var(--primary);text-decoration:none;">Buckets</a>';
  document.getElementById('browser-content').innerHTML = '<p style="color:var(--text-muted);">Loading...</p>';
  fetch('/buckets?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">' + d.error + '</p>'; return; }
      if (!d.buckets || !d.buckets.length) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--text-muted);">No buckets</p>'; return; }
      document.getElementById('breadcrumb').innerHTML = '<a href="#" onclick="showBuckets();return false" style="color:var(--primary);text-decoration:none;">Buckets</a>';
      var html = '';
      d.buckets.forEach(function(b) { html += '<div class="bucket-item" onclick="loadPrefix(\'' + escJs(b.name) + '\',\'\')">📁 ' + escHtml(b.name) + '</div>'; });
      html += '<div style="margin-top:0.75rem;border-top:0.0625rem solid var(--border);padding-top:0.75rem;">';
      html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.25rem;">Or enter a bucket name:</div>';
      html += '<div style="display:flex;gap:0.5rem;"><input type="text" id="manual-bucket" class="input" placeholder="my-bucket" style="flex:1;">';
      html += '<button class="btn btn-secondary" onclick="manualBucket()">Open</button></div></div>';
      document.getElementById('browser-content').innerHTML = html;
    })
    .catch(function(e) { document.getElementById('browser-content').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function manualBucket() {
  var bucket = document.getElementById('manual-bucket').value.trim();
  if (!bucket) { alert('Enter a bucket name'); return; }
  loadPrefix(bucket, '');
}

function togglePath(path, el) {
  var idx = tabState.browse.selectedFiles.indexOf(path);
  if (idx >= 0) {
    tabState.browse.selectedFiles.splice(idx, 1);
  } else {
    tabState.browse.selectedFiles.push(path);
  }
  updateBadge();
  updateTabBadges();
  updateBrowseActions();
}

function updateBadge() {
  var b = document.getElementById('selected-files-badge');
  b.textContent = tabState.browse.selectedFiles.length ? tabState.browse.selectedFiles.length + ' file(s)' : '';
  var list = document.getElementById('selection-list');
  if (list) {
    if (!tabState.browse.selectedFiles.length) {
      list.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select files from the browser</p>';
    } else {
      var html = '<div style="font-size:0.8rem;">';
      tabState.browse.selectedFiles.forEach(function(p) {
        var name = p.split('/').pop();
        var isConv = !isQueryable(p);
        html += '<div style="display:flex;justify-content:space-between;padding:0.2rem 0;border-bottom:1px solid var(--border);">' + escHtml(name) + ' <span style="color:var(--text-muted);font-size:0.7rem;">' + (isConv ? '⚠️ needs conversion' : '✔ queryable') + '</span></div>';
      });
      html += '</div>';
      list.innerHTML = html;
    }
  }
}

function isQueryable(p) {
  return /\.(parquet|csv|json|jsonl|tsv)$/i.test(p);
}

function updateBrowseActions() {
  var files = tabState.browse.selectedFiles;
  var btnQuery = document.getElementById('btn-goto-query');
  var btnTransform = document.getElementById('btn-goto-transform');
  var convertControls = document.getElementById('convert-controls');
  if (!files.length) {
    if (btnQuery) btnQuery.style.display = 'none';
    if (btnTransform) btnTransform.style.display = 'none';
    if (convertControls) convertControls.style.display = 'none';
    return;
  }
  var hasConvertible = files.some(function(f) { return !isQueryable(f); });
  var allQueryable = files.every(function(f) { return isQueryable(f); });
  if (btnQuery) btnQuery.style.display = allQueryable ? 'inline-block' : 'none';
  if (btnTransform) btnTransform.style.display = hasConvertible ? 'inline-block' : 'none';
  if (convertControls) convertControls.style.display = hasConvertible ? 'flex' : 'none';
}

function buildSQL() {
  var paths = tabState.browse.selectedFiles;
  if (!paths.length) { alert('Click files in the browser to select them'); return; }
  var prefix = paths[0].substring(0, paths[0].lastIndexOf('/') + 1);
  var same = paths.every(function(p) { return p.startsWith(prefix); });
  var sql;
  if (same && paths.length > 1) {
    var ext = paths[0].split('.').pop();
    var allSame = paths.every(function(p) { return p.endsWith('.' + ext); });
    sql = "SELECT * FROM " + reader(paths[0]) + "('" + prefix + "*." + (allSame ? ext : '*') + "') LIMIT 100";
  } else {
    sql = paths.map(function(p) { return "SELECT * FROM " + reader(p) + "('" + p + "')"; }).join('\nUNION ALL\n') + '\nLIMIT 100';
  }
  // Set SQL in query tab
  tabState.query.sql = sql;
  switchTab('query');
  // Fill editor
  var editor = document.getElementById('sql-editor');
  if (editor) editor.value = sql;
}

function reader(p) {
  var l = p.toLowerCase();
  if (l.endsWith('.csv') || l.endsWith('.tsv')) return 'read_csv_auto';
  if (l.endsWith('.json') || l.endsWith('.jsonl')) return 'read_json_auto';
  return 'read_parquet';
}


function startConvert() {
  var files = tabState.browse.selectedFiles.filter(function(p) { return !isQueryable(p); });
  if (!files.length) return;
  var fileKeys = files.map(function(p) { return p.replace(/^s3:\/\/[^\/]+\//, ''); });
  var deleteOrig = document.getElementById('delete-original').checked;
  var html = '<div class="convert-progress" id="convert-progress">';
  html += '<div class="card"><h3>Converting to Parquet</h3>';
  html += '<div id="convert-status"><p style="color:var(--text-muted);">Starting conversion...</p></div>';
  html += '</div></div>';
  document.getElementById('browser-content').innerHTML += html;
  fetch('/convert?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({bucket: tabState.browse.bucket, files: fileKeys, delete_original: deleteOrig})
  })
  .then(function(r) { return r.json(); })
  .then(function(job) { pollConvertStatus(job.job_id); })
  .catch(function(e) { document.getElementById('convert-status').innerHTML = '<span class="error">Error: ' + e.message + '</span>'; });
}

function pollConvertStatus(jobId) {
  fetch('/convert/status/' + encodeURIComponent(jobId))
    .then(function(r) { return r.json(); })
    .then(function(job) {
      var html = '<div style="margin-bottom:0.75rem;">';
      var pct = job.total > 0 ? Math.round(job.completed / job.total * 100) : 0;
      html += '<div style="display:flex;align-items:center;gap:0.75rem;margin-bottom:0.5rem;">';
      html += '<div style="flex:1;height:8px;background:var(--surface-2);border-radius:4px;overflow:hidden;">';
      html += '<div style="height:100%;width:' + pct + '%;background:var(--primary);border-radius:4px;transition:width 0.5s;"></div></div>';
      html += '<span style="font-size:0.85rem;color:var(--text-muted);">' + job.completed + '/' + job.total + '</span>';
      html += '</div>';
      html += '<table style="font-size:0.8rem;"><thead><tr><th>File</th><th>Status</th><th>Time</th></tr></thead><tbody>';
      job.results.forEach(function(r) {
        var icon = r.status === 'done' ? '✅' : r.status === 'error' ? '❌' : r.status === 'running' ? '⏳' : '⬜';
        html += '<tr><td>' + escHtml(r.file) + '</td><td>' + icon + ' ' + r.status + (r.error ? ': ' + escHtml(r.error) : '') + '</td><td>' + (r.elapsed_ms ? r.elapsed_ms + 'ms' : '-') + '</td></tr>';
      });
      html += '</tbody></table></div>';
      if (job.status === 'running') {
        html += '<p style="font-size:0.8rem;color:var(--text-muted);">Running...</p>';
      } else {
        html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.25rem 0.75rem;" onclick="loadPrefix(\'' + escJs(job.bucket) + '\',\'\')">Done — Refresh file list</button>';
        // Auto-navigate to query if all converted
        if (job.results.every(function(r) { return r.status === 'done'; })) {
          switchTab('query');
        }
      }
      document.getElementById('convert-status').innerHTML = html;
      if (job.status === 'running') {
        setTimeout(function() { pollConvertStatus(jobId); }, 2000);
      }
    })
    .catch(function(e) { document.getElementById('convert-status').innerHTML = '<span class="error">Poll error: ' + e.message + '</span>'; });
}

function fmtSize(b) {
  if (!b) return '0B';
  var u = ['B','KB','MB','GB','TB'], i = Math.floor(Math.log(b) / Math.log(1024));
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i];
}

function download(content, filename, mime) {
  var blob = new Blob([content], {type: mime});
  var a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}

function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
function escJs(s) { return s.replace(/'/g,"\\'").replace(/"/g,'\\"'); }
function escAttr(s) { return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;'); }
