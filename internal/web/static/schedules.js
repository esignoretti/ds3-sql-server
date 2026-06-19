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

function renderSchedulesTab() {
  var projectId = tabState.browse.project;
  if (!projectId) {
    var el = document.getElementById('schedules-list');
    if (el) el.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project first.</p>';
    return;
  }
  loadSchedules();
}
