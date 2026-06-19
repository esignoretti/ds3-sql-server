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

var tfPrefix = '';

function tfLoadBucket(bucket) {
  tfPrefix = '';
  tfListAt(bucket, '');
}

function tfListAt(bucket, prefix) {
  var list = document.getElementById('tf-file-list');
  if (!list) return;
  list.innerHTML = '<p style="color:var(--text-muted);">Loading…</p>';
  fetch('/buckets/' + encodeURIComponent(bucket) + '?project=' + encodeURIComponent(tabState.browse.project) + '&prefix=' + encodeURIComponent(prefix))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { list.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }

      var html = '';
      // Breadcrumb / navigation
      if (prefix) {
        var parent = prefix.split('/').filter(Boolean).slice(0, -1).join('/');
        parent = parent ? parent + '/' : '';
        html += '<div class="tf-nav" style="margin-bottom:0.35rem;">' +
          '<a href="#" onclick="tfListAt(\'' + escJs(bucket) + '\',\'\');return false;" style="color:var(--primary);font-size:0.85rem;">📁 ' + escHtml(bucket) + '</a>';
        var parts = prefix.split('/').filter(Boolean);
        var cumulative = '';
        parts.forEach(function(p) {
          cumulative += p + '/';
          html += ' / <a href="#" onclick="tfListAt(\'' + escJs(bucket) + '\',\'' + escJs(cumulative) + '\');return false;" style="color:var(--primary);font-size:0.85rem;">' + escHtml(p) + '</a>';
        });
        html += '</div>';
      } else {
        html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.35rem;">📁 ' + escHtml(bucket) + '</div>';
      }

      // Subdirectories
      (d.prefixes || []).forEach(function(p) {
        var name = p.replace(/\/$/, '').split('/').pop();
        html += '<div class="tf-dir" onclick="tfListAt(\'' + escJs(bucket) + '\',\'' + escJs(p) + '\')" style="cursor:pointer;padding:0.25rem 0.25rem;font-size:0.85rem;">📁 ' + escHtml(name) + '/</div>';
      });

      // Files
      var all = d.objects || [];
      var convertible = all.filter(function(o) {
        var l = o.key.toLowerCase();
        return !(l.endsWith('.parquet') || l.endsWith('.csv') || l.endsWith('.json') || l.endsWith('.jsonl') || l.endsWith('.tsv'));
      });

      if (convertible.length) {
        html += '<div style="font-size:0.85rem;color:var(--text-muted);margin-top:0.35rem;margin-bottom:0.25rem;">' + convertible.length + ' convertible file(s):</div>';
        convertible.forEach(function(o) {
          var s3path = 's3://' + bucket + '/' + o.key;
          var isSelected = tabState.browse.selectedFiles.indexOf(s3path) >= 0;
          html += '<div class="tf-file-row' + (isSelected ? ' tf-selected' : '') + '">' +
            '<span onclick="tfToggleFile(\'' + escAttr(s3path) + '\',this)" style="cursor:pointer;">' + (isSelected ? '☑' : '☐') + ' ' + escHtml(o.key.split('/').pop()) + '</span>' +
            ' <span style="color:var(--text-muted);font-size:0.75rem;">' + fmtSize(o.size) + '</span>' +
            ' <a href="#" onclick="tfConfigure(\'' + escJs(bucket) + '\',\'' + escJs(o.key) + '\');return false;" style="color:var(--primary);font-size:0.75rem;text-decoration:none;">[configure]</a></div>';
        });
      }

      if (!html) html = '<p style="color:var(--text-muted);">Empty.</p>';
      list.innerHTML = html;
    });
}

function tfToggleFile(path, spanEl) {
  var row = spanEl.parentElement;
  var idx = tabState.browse.selectedFiles.indexOf(path);
  if (idx >= 0) {
    tabState.browse.selectedFiles.splice(idx, 1);
    row.classList.remove('tf-selected');
    spanEl.textContent = '☐ ' + spanEl.textContent.slice(2);
  } else {
    tabState.browse.selectedFiles.push(path);
    row.classList.add('tf-selected');
    spanEl.textContent = '☑ ' + spanEl.textContent.slice(2);
  }
  renderTransformTab();
  updateTabBadges();
  if (tabState.browse.selectedFiles.length > 0) tfStep(0);
}

function tfStep(n) {
  var steps = document.querySelectorAll('#tf-steps .step');
  steps.forEach(function(s, i) {
    s.classList.remove('active', 'done');
    if (i < n) s.classList.add('done');
    else if (i === n) s.classList.add('active');
  });
  // Show schedule and post-action sections based on step
  var schedArea = document.getElementById('tf-schedule-area');
  var postArea = document.getElementById('tf-postaction-area');
  if (schedArea) schedArea.style.display = n >= 2 ? 'block' : 'none';
  if (postArea) postArea.style.display = n >= 3 ? 'block' : 'none';
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
  tfStep(1);
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

// ── Schedule & Post-action toggles (called from HTML) ──────────

function tfToggleSchedule() {
  var modeEls = document.getElementsByName('tf-schedule-mode');
  var isSchedule = false;
  modeEls.forEach(function(el) { if (el.checked && el.value === 'schedule') isSchedule = true; });
  var cronArea = document.getElementById('tf-cron-area');
  var schedBtn = document.getElementById('tf-schedule-btn');
  if (cronArea) cronArea.style.display = isSchedule ? 'block' : 'none';
  if (schedBtn) schedBtn.style.display = isSchedule ? 'inline-block' : 'none';
  tfStep(isSchedule ? 2 : 1);
}

function tfCronPresetChange() {
  var preset = document.getElementById('tf-cron-preset').value;
  var customInput = document.getElementById('tf-cron-custom');
  if (preset) {
    customInput.value = preset;
    tfUpdateCronPreview(preset);
  }
}

function tfClearCronPreset() {
  document.getElementById('tf-cron-preset').value = '';
  tfUpdateCronPreview(document.getElementById('tf-cron-custom').value);
}

function tfUpdateCronPreview(expr) {
  var preview = document.getElementById('tf-cron-preview');
  if (!preview) return;
  if (!expr) { preview.textContent = ''; return; }
  var descs = {
    '* * * * *': 'Every minute',
    '0 * * * *': 'Every hour',
    '0 0 * * *': 'Daily at midnight',
    '0 0 * * 0': 'Weekly on Sunday',
    '0 0 1 * *': 'Monthly on 1st'
  };
  preview.textContent = descs[expr] || 'Custom: ' + expr;
}

function tfTogglePostAction() {
  var modeEls = document.getElementsByName('tf-post-action');
  var isMove = false, isKeep = true;
  modeEls.forEach(function(el) {
    if (el.checked) {
      if (el.value === 'move') isMove = true;
      if (el.value !== '') isKeep = false;
    }
  });
  var moveArea = document.getElementById('tf-move-area');
  if (moveArea) moveArea.style.display = isMove ? 'flex' : 'none';
  tfStep(isKeep ? 0 : isMove ? 3 : 2);

  if (isMove && tabState.browse.project) {
    var sel = document.getElementById('tf-move-bucket');
    if (sel && sel.options.length <= 1) {
      sel.innerHTML = '<option value="">Loading buckets...</option>';
      fetch('/buckets?project=' + encodeURIComponent(tabState.browse.project))
        .then(function(r) { return r.json(); })
        .then(function(d) {
          if (d.error) { sel.innerHTML = '<option value="">' + escHtml(d.error) + '</option>'; return; }
          var html = '<option value="">Select target bucket...</option>';
          (d.buckets || []).forEach(function(b) { html += '<option value="' + escAttr(b.name) + '">' + escHtml(b.name) + '</option>'; });
          sel.innerHTML = html;
        });
    }
  }
}

function tfStartSchedule() {
  var files = tabState.browse.selectedFiles.filter(function(p) { return !isQueryable(p); });
  if (!files.length) { alert('No convertible files selected.'); return; }
  var cronCustom = document.getElementById('tf-cron-custom');
  var cron = cronCustom ? cronCustom.value.trim() : '';
  if (!cron) { alert('Please enter a cron expression.'); return; }

  var postAction = '', moveBucket = '', movePrefix = '';
  document.getElementsByName('tf-post-action').forEach(function(el) { if (el.checked) postAction = el.value; });
  if (postAction === 'move') {
    moveBucket = document.getElementById('tf-move-bucket') ? document.getElementById('tf-move-bucket').value : '';
    movePrefix = document.getElementById('tf-move-prefix') ? document.getElementById('tf-move-prefix').value : '';
    if (!moveBucket) { alert('Please select a target bucket for move.'); return; }
  }

  var bucket = files[0].replace(/^s3:\/\//, '').split('/')[0];
  var firstKey = files[0].replace(/^s3:\/\/[^\/]+\//, '');
  var parts = firstKey.split('/');
  var fileName = parts.pop();
  var ext = fileName.includes('.') ? fileName.split('.').pop() : '*';
  var source = 's3://' + bucket + '/' + (parts.length ? parts.join('/') + '/' : '') + '*.' + ext;

  var progress = document.getElementById('tf-convert-progress');
  if (progress) { progress.style.display = 'block'; progress.innerHTML = '<p style="color:var(--text-muted);">Creating schedule...</p>'; }

  fetch('/schedules?project=' + encodeURIComponent(tabState.browse.project), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({cron: cron, type: 'convert', source: source, format: 'text', post_action: postAction, move_bucket: moveBucket, move_prefix: movePrefix, sql: ''})
  })
  .then(function(r) { return r.json(); })
  .then(function(sch) {
    if (sch.error) throw new Error(sch.error);
    if (progress) progress.innerHTML = '<p style="color:var(--green-500);">Schedule created! <a href="#" onclick="navigateToTab(\'schedules\');return false;" class="btn-link">View schedules</a></p>';
    updateTabBadges();
  })
  .catch(function(e) {
    if (progress) progress.innerHTML = '<span style="color:var(--red);">Error: ' + e.message + '</span>';
    else alert('Error: ' + e.message);
  });
}

function fmtSize(b) {
  if (!b) return '0B';
  var u = ['B','KB','MB','GB','TB'], i = Math.floor(Math.log(b) / Math.log(1024));
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + u[i];
}
