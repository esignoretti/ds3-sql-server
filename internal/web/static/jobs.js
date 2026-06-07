// Jobs / history panel — DS3 SQL Server.
function loadJobsPanel() {
  var c = document.getElementById('jobs-panel-content');
  if (!c) return;
  if (!tabState.browse.project) {
    c.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">Select a project to see job history.</p>';
    return;
  }
  fetch('/jobs?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.error) { c.innerHTML = '<p style="color:var(--red);">' + escHtml(d.error) + '</p>'; return; }
      var jobs = d.jobs || [];
      if (!jobs.length) { c.innerHTML = '<p style="color:var(--text-muted);font-size:0.85rem;">No jobs yet.</p>'; return; }
      var html = '<table class="jobs-table"><thead><tr><th>Status</th><th>Type</th><th>Rows</th><th>SQL</th><th>When</th></tr></thead><tbody>';
      jobs.forEach(function(j) {
        var rows = (j.row_count != null) ? j.row_count : (j.result && j.result.row_count) || '';
        var sql = (j.sql || '').slice(0, 80);
        var when = j.created_at ? new Date(j.created_at).toLocaleString() : '';
        html += '<tr class="jobs-row" onclick="loadJob(\'' + escJs(j.id) + '\')">' +
          '<td><span class="job-status job-' + escAttr(j.status) + '">' + escHtml(j.status) + '</span></td>' +
          '<td>' + escHtml(j.type || 'query') + '</td>' +
          '<td>' + escHtml(String(rows)) + '</td>' +
          '<td class="jobs-sql">' + escHtml(sql) + '</td>' +
          '<td class="jobs-when">' + escHtml(when) + '</td></tr>';
      });
      html += '</tbody></table>';
      c.innerHTML = html;
    })
    .catch(function(e) { c.innerHTML = '<p style="color:var(--red);">Error: ' + escHtml(e.message) + '</p>'; });
}

function loadJob(id) {
  fetch('/jobs/' + encodeURIComponent(id) + '?project=' + encodeURIComponent(tabState.browse.project))
    .then(function(r) { return r.json(); })
    .then(function(j) {
      if (j.error && !j.sql) { alert(j.error); return; }
      var ed = document.getElementById('sql-editor');
      if (ed && j.sql) ed.value = j.sql;
      // If the job carries an inline result (sync fast-path), render it.
      if (j.result && j.result.columns) {
        tabState.query.results = j.result;
        tabState.query.currentPage = 0;
        var status = document.getElementById('query-status');
        if (status) status.innerHTML = (j.result.row_count || 0) + ' rows (from job ' + escHtml(id) + ')';
        if (typeof renderPage === 'function' && j.result.row_count) {
          document.getElementById('export-bar').style.display = 'flex';
          renderPage();
        }
      }
    });
}
