var selProject = '';
var currentConfig = {
  bucket: '',
  pattern: '',
  delimiter: ' ',
  quote: '"',
  header_row: false,
  columns: []
};

function loadPreview(bucket, file) {
  currentConfig.bucket = bucket;
  var parts = file.split('/');
  var filename = parts.pop();
  var extIdx = filename.lastIndexOf('.');
  currentConfig.pattern = (parts.length ? parts.join('/') + '/' : '') + '*.' + (extIdx >= 0 ? filename.slice(extIdx + 1) : '*');

  fetch('/convert/preview?project=' + encodeURIComponent(selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(file) + '&lines=25')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      if (d.saved_config) {
        currentConfig.delimiter = d.saved_config.delimiter;
        currentConfig.quote = d.saved_config.quote;
        currentConfig.header_row = d.saved_config.header_row;
        currentConfig.columns = d.saved_config.columns;
      }
      renderConfig(d);
    })
    .catch(function(e) { document.getElementById('config-app').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function renderConfig(d) {
  var html = '<div class="config-layout">';
  html += '<div class="card"><h3>Step 1: Delimiter & Quote</h3>';
  html += '<div style="display:flex;gap:1rem;flex-wrap:wrap;align-items:center;margin-top:0.75rem;">';
  html += '<label>Delimiter: <select id="delim-select" onchange="onConfigChange()">';
  var delims = [[' ', 'Space'], ['\t', 'Tab'], [',', 'Comma'], ['|', 'Pipe'], [';', 'Semicolon']];
  delims.forEach(function(d) {
    html += '<option value="' + d[0] + '"' + (currentConfig.delimiter === d[0] ? ' selected' : '') + '>' + d[1] + ' (' + (d[0] === ' ' ? 'space' : d[0] === '\t' ? 'tab' : d[0]) + ')</option>';
  });
  html += '<option value="custom">Custom</option>';
  html += '</select></label>';
  html += '<label>Quote: <select id="quote-select" onchange="onConfigChange()">';
  [['"', 'Double quote'], ["'", 'Single quote'], ['', 'None']].forEach(function(q) {
    html += '<option value="' + q[0] + '"' + (currentConfig.quote === q[0] ? ' selected' : '') + '>' + q[1] + '</option>';
  });
  html += '</select></label>';
  html += '<label><input type="checkbox" id="header-row" onchange="onConfigChange()" ' + (currentConfig.header_row ? 'checked' : '') + '> Header row</label>';
  html += '</div></div>';

  html += '<div class="card"><h3>Step 2: Preview & Name Columns</h3>';
  html += '<div id="preview-table" style="overflow-x:auto;margin-top:0.75rem;">';
  html += '<p style="color:var(--text-muted);">Configure delimiter above to preview</p></div></div>';

  html += '<div class="card"><h3>Step 3: Save Config</h3>';
  html += '<div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap;margin-top:0.75rem;">';
  html += '<label>Pattern: <input type="text" id="config-pattern" value="' + escHtml(currentConfig.pattern) + '" style="width:300px;font-family:monospace;"></label>';
  html += '<button class="btn" onclick="saveConfig()">Save Config</button>';
  html += '<button class="btn btn-secondary" onclick="saveAndConvert()">Save & Convert</button>';
  html += '</div><p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.5rem;">Applies to all files matching this pattern (e.g., <code>*.log</code>).</p>';
  html += '</div></div>';

  document.getElementById('config-app').innerHTML = html;
  updatePreview(d);
}

function onConfigChange() {
  currentConfig.delimiter = document.getElementById('delim-select').value;
  if (currentConfig.delimiter === 'custom') {
    currentConfig.delimiter = prompt('Enter delimiter character:') || ' ';
  }
  currentConfig.quote = document.getElementById('quote-select').value;
  currentConfig.header_row = document.getElementById('header-row').checked;
  updatePreview();
}

function updatePreview() {
  var container = document.getElementById('preview-table');
  if (!container) return;

  var bucket = currentConfig.bucket;
  var file = new URLSearchParams(window.location.search).get('file');
  if (!file) return;

  fetch('/convert/preview?project=' + encodeURIComponent(selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(file) + '&lines=25')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';
      var delim = currentConfig.delimiter;
      d.preview_lines.forEach(function(line) {
        var cells = line.split(delim);
        html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
        cells.forEach(function(cell, ci) {
          html += '<span style="flex:1;min-width:80px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
        });
        html += '</div>';
      });
      html += '</div>';

      var numCols = d.preview_lines.length > 0 ? d.preview_lines[0].split(currentConfig.delimiter).length : 0;
      if (currentConfig.columns.length !== numCols) {
        currentConfig.columns = [];
        for (var i = 0; i < numCols; i++) {
          currentConfig.columns.push({name: 'col' + i, type: 'VARCHAR'});
        }
      }
      html += '<div style="margin-top:0.75rem;"><h4 style="font-size:0.85rem;margin-bottom:0.5rem;">Column Names & Types</h4>';
      html += '<div style="display:flex;gap:0.5rem;flex-wrap:wrap;">';
      currentConfig.columns.forEach(function(col, idx) {
        html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
        html += '<input type="text" value="' + escHtml(col.name) + '" style="width:100px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="currentConfig.columns[' + idx + '].name=this.value">';
        html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="currentConfig.columns[' + idx + '].type=this.value">';
        ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
          html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
        });
        html += '</select>';
        html += '</div>';
      });
      html += '</div></div>';
      container.innerHTML = html;
    })
    .catch(function(e) {});
}

function saveConfig() {
  var patternInput = document.getElementById('config-pattern');
  if (patternInput) currentConfig.pattern = patternInput.value;
  fetch('/convert/columns', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(currentConfig)
  })
  .then(function(r) { return r.json(); })
  .then(function() { alert('Config saved!'); })
  .catch(function(e) { alert('Error saving: ' + e.message); });
}

function saveAndConvert() {
  saveConfig();
  setTimeout(function() { window.location.href = '/browse'; }, 500);
}

function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
