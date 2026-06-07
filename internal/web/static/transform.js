// Transform tab — bucket browser + file selection + conversion.
function tfSwitchProject(id) {
  tabState.browse.project = id;
  var sel = document.getElementById('tf-bucket');
  if (!sel) return;
  sel.innerHTML = '<option value="">Loading buckets…</option>';
  fetch('/buckets?project=' + encodeURIComponent(id))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { sel.innerHTML = '<option value="">' + escHtml(d.error) + '</option>'; return; }
      var html = '<option value="">Select bucket…</option>';
      (d.buckets || []).forEach(function(b) { html += '<option value="' + escAttr(b.name) + '">' + escHtml(b.name) + '</option>'; });
      sel.innerHTML = html;
    });
}

function tfLoadBucket(bucket) {
  var list = document.getElementById('tf-file-list');
  if (!list || !bucket) { list.innerHTML = '<p style="color:var(--text-muted);">Select a bucket.</p>'; return; }
  list.innerHTML = '<p style="color:var(--text-muted);">Loading…</p>';
  fetch('/buckets/' + encodeURIComponent(bucket) + '?project=' + encodeURIComponent(tabState.browse.project) + '&prefix=')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { list.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var all = d.objects || [];
      var convertible = all.filter(function(o) {
        var l = o.key.toLowerCase();
        return !(l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv'));
      });
      if (!convertible.length) {
        list.innerHTML = '<p style="color:var(--text-muted);">No files that need conversion in this bucket (only .parquet/.csv/.json/.tsv found).</p>';
        return;
      }
      var html = '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.35rem;">' + convertible.length + ' file(s) — click to add, <a href="#" onclick="return false;" style="color:var(--primary);">[configure]</a> to edit parsing:</div>';
      convertible.forEach(function(o) {
        var s3path = 's3://' + bucket + '/' + o.key;
        var isSelected = tabState.browse.selectedFiles.indexOf(s3path) >= 0;
        html += '<div class="tf-file-row' + (isSelected ? ' tf-selected' : '') + '">' +
          '<span onclick="tfToggleFile(\'' + escAttr(s3path) + '\',this.parentElement)" style="cursor:pointer;">' + (isSelected ? '☑' : '☐') + ' ' + escHtml(o.key.split('/').pop()) + '</span>' +
          ' <span style="color:var(--text-muted);font-size:0.75rem;">' + fmtSize(o.size) + '</span>' +
          ' <a href="#" onclick="tfConfigure(\'' + escJs(bucket) + '\',\'' + escJs(o.key) + '\');return false;" style="color:var(--primary);font-size:0.75rem;text-decoration:none;">[configure]</a></div>';
      });
      list.innerHTML = html;
    });
}

function tfToggleFile(path, el) {
  var idx = tabState.browse.selectedFiles.indexOf(path);
  if (idx >= 0) {
    tabState.browse.selectedFiles.splice(idx, 1);
    el.classList.remove('tf-selected');
    el.innerHTML = '☐ ' + el.innerHTML.slice(el.innerHTML.indexOf('☑') >= 0 ? 2 : 0).trim();
  } else {
    tabState.browse.selectedFiles.push(path);
    el.classList.add('tf-selected');
    el.innerHTML = '☑ ' + el.innerHTML.slice(el.innerHTML.indexOf('☐') >= 0 ? 2 : 0).trim();
  }
  renderTransformTab();
  updateTabBadges();
}

function tfConfigure(bucket, fileKey) {
  var s3path = 's3://' + bucket + '/' + fileKey;
  if (tabState.browse.selectedFiles.indexOf(s3path) < 0) {
    tabState.browse.selectedFiles.push(s3path);
    renderTransformTab();
    updateTabBadges();
  }
  tabState.transform.pendingBucket = bucket;
  tabState.transform.pendingFile = fileKey;
  renderTransformTab();
}

function tfStartConvert() {
  var files = tabState.browse.selectedFiles.filter(function(p) { return !isQueryable(p); });
  if (!files.length) { alert('No convertible files selected.'); return; }
  var fileKeys = files.map(function(p) { return p.replace(/^s3:\/\/[^\/]+\//, ''); });
  var bucket = files[0].replace(/^s3:\/\//, '').split('/')[0];
  var deleteOrig = document.getElementById('tf-delete-original') ? document.getElementById('tf-delete-original').checked : false;
  var progress = document.getElementById('tf-convert-progress');
  if (!progress) return;
  progress.style.display = 'block';
  progress.innerHTML = '<p style="color:var(--text-muted);">Starting conversion…</p>';
  fetch('/convert?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({bucket: bucket, files: fileKeys, delete_original: deleteOrig})
  })
  .then(function(r) { return r.json(); })
  .then(function(job) { tfPollConvert(job.job_id, bucket); })
  .catch(function(e) { progress.innerHTML = '<span style="color:var(--red);">Error: ' + e.message + '</span>'; });
}

function tfPollConvert(jobId, bucket) {
  fetch('/convert/status/' + encodeURIComponent(jobId))
    .then(function(r) { return r.json(); })
    .then(function(job) {
      var el = document.getElementById('tf-convert-progress');
      if (!el) return;
      var pct = job.total > 0 ? Math.round(job.completed / job.total * 100) : 0;
      var html = '<div style="margin-bottom:0.5rem;">';
      html += '<div style="display:flex;align-items:center;gap:0.75rem;margin-bottom:0.5rem;">';
      html += '<div style="flex:1;height:8px;background:var(--surface-2);border-radius:4px;overflow:hidden;">';
      html += '<div style="height:100%;width:' + pct + '%;background:var(--primary);border-radius:4px;transition:width 0.5s;"></div></div>';
      html += '<span style="font-size:0.85rem;color:var(--text-muted);">' + job.completed + '/' + job.total + '</span></div>';
      html += '<table style="font-size:0.8rem;"><thead><tr><th>File</th><th>Status</th></tr></thead><tbody>';
      (job.results || []).forEach(function(r) {
        var icon = r.status === 'done' ? '✅' : r.status === 'error' ? '❌' : r.status === 'running' ? '⏳' : '⬜';
        html += '<tr><td>' + escHtml(r.file) + '</td><td>' + icon + ' ' + r.status + (r.error ? ': ' + escHtml(r.error) : '') + '</td></tr>';
      });
      html += '</tbody></table></div>';
      if (job.status === 'running') {
        html += '<p style="font-size:0.8rem;color:var(--text-muted);">Running…</p>';
        el.innerHTML = html;
        setTimeout(function() { tfPollConvert(jobId, bucket); }, 2000);
      } else {
        html += '<button class="btn btn-secondary" style="font-size:0.8rem;padding:0.25rem 0.75rem;" onclick="tfLoadBucket(\'' + escJs(bucket) + '\')">Done — Refresh</button>';
        el.innerHTML = html;
      }
    });
}

function fmtSize(b) {
  if (!b) return '0B';
  var u = ['B','KB','MB','GB','TB'], i = Math.floor(Math.log(b) / Math.log(1024));
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i];
}
