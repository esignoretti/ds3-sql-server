// Schedules management for DS3 SQL Server
// Lists schedules, shows delete/edit buttons, and allows schedule creation from transform tab.

function loadSchedules() {
  var projectId = tabState.browse.project;
  if (!projectId) {
    document.getElementById('schedules-list').innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project first.</p>';
    return;
  }
  var list = document.getElementById('schedules-list');
  if (!list) return;
  list.innerHTML = '<p style="color:var(--text-muted);">Loading schedules...</p>';

  fetch('/schedules?project=' + encodeURIComponent(projectId))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { list.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var schedules = d.schedules || [];
      if (!schedules.length) {
        list.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No schedules configured. Use the Transform tab to create one.</p>';
        return;
      }
      var html = '<table style="width:100%;font-size:0.85rem;border-collapse:collapse;">';
      html += '<thead><tr style="border-bottom:0.0625rem solid var(--border);">' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">Type</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">Cron</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">SQL / Source</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">Post-Action</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">Next Run</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;">Running</th>' +
        '<th style="text-align:left;padding:0.25rem 0.5rem;"></th>' +
        '</tr></thead><tbody>';
      schedules.forEach(function(s) {
        var typeLabel = s.type || 'query';
        var sourceOrSql = s.type === 'convert' ? (s.source || '') : (s.sql || '');
        var postAction = s.post_action || '';
        var nextRun = s.next_run_at ? new Date(s.next_run_at).toLocaleString() : '-';
        var running = s.running ? 'Yes' : 'No';
        html += '<tr style="border-bottom:0.0625rem solid var(--border);">';
        html += '<td style="padding:0.25rem 0.5rem;">' + escHtml(typeLabel) + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;font-family:monospace;">' + escHtml(s.cron) + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;max-width:250px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="' + escAttr(sourceOrSql) + '">' + escHtml(sourceOrSql) + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;">' + escHtml(postAction || '-') + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;">' + escHtml(nextRun) + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;">' + running + '</td>';
        html += '<td style="padding:0.25rem 0.5rem;">' +
          '<button class="btn btn-secondary" style="font-size:0.75rem;padding:0.15rem 0.5rem;margin-right:0.25rem;" onclick="editSchedule(\'' + escJs(s.id) + '\')">Edit</button>' +
          '<button class="btn btn-secondary" style="font-size:0.75rem;padding:0.15rem 0.5rem;color:var(--red);" onclick="deleteSchedule(\'' + escJs(s.id) + '\')">Delete</button>' +
          '</td>';
        html += '</tr>';
      });
      html += '</tbody></table>';
      list.innerHTML = html;
    })
    .catch(function(e) {
      var list = document.getElementById('schedules-list');
      if (list) list.innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>';
    });
}

function deleteSchedule(id) {
  if (!confirm('Delete this schedule?')) return;
  var projectId = tabState.browse.project;
  fetch('/schedules/' + encodeURIComponent(id) + '?project=' + encodeURIComponent(projectId), {method: 'DELETE'})
    .then(function(r) {
      if (!r.ok) return r.json().then(function(d) { throw new Error(d.error); });
      loadSchedules();
      updateTabBadges();
    })
    .catch(function(e) { alert('Error: ' + e.message); });
}

function editSchedule(id) {
  // Navigate to transform tab and pre-fill from this schedule
  // For now, just switch to transform tab
  navigateToTab('transform');
  // Could be extended to pre-fill form fields
}

// ── Functions used by transform.js for schedule creation ──────────

function tfToggleSchedule() {
  var modeEls = document.getElementsByName('tf-schedule-mode');
  var isSchedule = false;
  modeEls.forEach(function(el) { if (el.checked && el.value === 'schedule') isSchedule = true; });
  document.getElementById('tf-cron-area').style.display = isSchedule ? 'block' : 'none';
  var schedBtn = document.getElementById('tf-schedule-btn');
  if (schedBtn) schedBtn.style.display = isSchedule ? 'inline-block' : 'none';
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
  // Simple human-readable description
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
  var isMove = false;
  modeEls.forEach(function(el) { if (el.checked && el.value === 'move') isMove = true; });
  document.getElementById('tf-move-area').style.display = isMove ? 'block' : 'none';

  // Load bucket list when move is shown
  if (isMove && tabState.browse.project) {
    var sel = document.getElementById('tf-move-bucket');
    if (sel && sel.options.length <= 1) {
      sel.innerHTML = '<option value="">Loading buckets...</option>';
      fetch('/buckets?project=' + encodeURIComponent(tabState.browse.project))
        .then(function(r) { return r.json(); })
        .then(function(d) {
          if (d.error) { sel.innerHTML = '<option value="">' + escHtml(d.error) + '</option>'; return; }
          var html = '<option value="">Select bucket...</option>';
          (d.buckets || []).forEach(function(b) { html += '<option value="' + escAttr(b.name) + '">' + escHtml(b.name) + '</option>'; });
          sel.innerHTML = html;
        });
    }
  }
}

function tfStartSchedule() {
  // Collect values from the form and create a schedule via API
  var files = tabState.browse.selectedFiles.filter(function(p) { return !isQueryable(p); });
  if (!files.length) { alert('No convertible files selected.'); return; }

  // Determine cron expression
  var cronCustom = document.getElementById('tf-cron-custom');
  var cron = cronCustom ? cronCustom.value.trim() : '';
  if (!cron) { alert('Please enter a cron expression.'); return; }

  // Determine post-action
  var postAction = '';
  var moveBucket = '';
  var movePrefix = '';
  var modeEls = document.getElementsByName('tf-post-action');
  modeEls.forEach(function(el) {
    if (el.checked) {
      postAction = el.value;
    }
  });
  if (postAction === 'move') {
    moveBucket = document.getElementById('tf-move-bucket') ? document.getElementById('tf-move-bucket').value : '';
    movePrefix = document.getElementById('tf-move-prefix') ? document.getElementById('tf-move-prefix').value : '';
    if (!moveBucket) { alert('Please select a target bucket for move.'); return; }
  }

  // Build source from selected files
  var bucket = files[0].replace(/^s3:\/\//, '').split('/')[0];
  // Use a glob pattern derived from the files
  var firstKey = files[0].replace(/^s3:\/\/[^\/]+\//, '');
  var parts = firstKey.split('/');
  var fileName = parts.pop();
  var ext = fileName.includes('.') ? fileName.split('.').pop() : '*';
  var source = 's3://' + bucket + '/' + (parts.length ? parts.join('/') + '/' : '') + '*.' + ext;

  // Show progress
  var progress = document.getElementById('tf-convert-progress');
  if (progress) {
    progress.style.display = 'block';
    progress.innerHTML = '<p style="color:var(--text-muted);">Creating schedule...</p>';
  }

  var projectId = tabState.browse.project;
  fetch('/schedules?project=' + encodeURIComponent(projectId), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      cron: cron,
      type: 'convert',
      source: source,
      format: 'text',
      post_action: postAction,
      move_bucket: moveBucket,
      move_prefix: movePrefix,
      sql: ''
    })
  })
  .then(function(r) { return r.json(); })
  .then(function(sch) {
    if (sch.error) { throw new Error(sch.error); }
    if (progress) progress.innerHTML = '<p style="color:var(--green);">Schedule created! <a href="#" onclick="navigateToTab(\'schedules\');return false;" style="color:var(--primary);">View schedules</a></p>';
    updateTabBadges();
  })
  .catch(function(e) {
    if (progress) progress.innerHTML = '<span style="color:var(--red);">Error: ' + e.message + '</span>';
    else alert('Error: ' + e.message);
  });
}

// Called when schedules tab is activated
function renderSchedulesTab() {
  var projectId = tabState.browse.project;
  if (!projectId) {
    document.getElementById('schedules-list').innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project in the Browse or Transform tab first.</p>';
    return;
  }
  loadSchedules();
}
