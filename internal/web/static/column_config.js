var selProject = ''; // fallback; prefer tabState.browse.project
var cachedPreviewLines = [];
var currentConfig = {
  bucket: '',
  pattern: '',
  profile_name: '',
  mode: 'delimiter',
  delimiter: ' ',
  quote: '"',
  header_row: false,
  columns: []
};
var savedConfigsForBucket = [];
var PANEL_COLORS = ['#0065FF','#27B681','#F3B356','#8739B1','#f87171','#06b6d4','#a78bfa','#fb923c','#34d399','#f472b6'];

function loadPreview(bucket, file) {
  currentConfig.bucket = bucket;
  var parts = file.split('/');
  var filename = parts.pop();
  var extIdx = filename.lastIndexOf('.');
  currentConfig.pattern = (parts.length ? parts.join('/') + '/' : '') + '*.' + (extIdx >= 0 ? filename.slice(extIdx + 1) : '*');

  // Load saved configs for this bucket
  fetch('/convert/columns?bucket=' + encodeURIComponent(bucket))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      savedConfigsForBucket = d.configs || [];
    })
    .catch(function() { savedConfigsForBucket = []; });

  fetch('/convert/preview?project=' + encodeURIComponent(typeof tabState !== 'undefined' && tabState.browse.project ? tabState.browse.project : selProject) + '&bucket=' + encodeURIComponent(bucket) + '&file=' + encodeURIComponent(file) + '&lines=25')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      cachedPreviewLines = d.preview_lines;
      if (d.saved_config) {
        currentConfig.mode = d.saved_config.mode || 'delimiter';
        currentConfig.delimiter = d.saved_config.delimiter;
        currentConfig.quote = d.saved_config.quote;
        currentConfig.header_row = d.saved_config.header_row;
        currentConfig.columns = d.saved_config.columns;
        currentConfig.profile_name = d.saved_config.profile_name || '';
      } else {
        currentConfig.mode = 'delimiter';
        currentConfig.delimiter = ' ';
        currentConfig.quote = '"';
        currentConfig.header_row = false;
        currentConfig.columns = [];
        currentConfig.profile_name = '';
      }
      renderConfig();
    })
    .catch(function(e) { document.getElementById('config-app').innerHTML = '<p style="color:var(--red);">Error: ' + e.message + '</p>'; });
}

function renderConfig() {
  var modeHtml = '<div class="fw-mode-toggle">';
  modeHtml += '<button id="mode-delimiter" class="' + (currentConfig.mode === 'delimiter' ? 'active' : '') + '" onclick="switchMode(\'delimiter\')">Delimiter</button>';
  modeHtml += '<button id="mode-fixed" class="' + (currentConfig.mode === 'fixed_width' ? 'active' : '') + '" onclick="switchMode(\'fixed_width\')">Fixed Width</button>';
  modeHtml += '</div>';

  var html = '<div class="config-layout">';
  html += modeHtml;

  if (currentConfig.mode === 'delimiter') {
    html += renderDelimiterStep1();
    html += renderDelimiterStep2();
  } else {
    html += renderFixedWidthStep1();
    html += renderFixedWidthStep2();
  }

  html += renderSaveStep();
  html += '</div>';

  document.getElementById('config-app').innerHTML = html;
  if (currentConfig.mode === 'fixed_width') {
    renderFixedWidthRuler();
    renderFixedWidthColDefs();
  }
  updatePreview();
}

function switchMode(mode) {
  currentConfig.mode = mode;
  if (mode === 'fixed_width' && (!currentConfig.columns.length || !('start' in currentConfig.columns[0]))) {
    currentConfig.columns = [{name: 'col0', type: 'VARCHAR', start: 0}];
  }
  renderConfig();
}

function renderDelimiterStep1() {
  var html = '<div class="card"><h3>Step 1: Delimiter & Quote</h3>';
  html += '<div style="display:flex;gap:1rem;flex-wrap:wrap;align-items:center;margin-top:0.75rem;">';
  html += '<label>Delimiter: <select id="delim-select" onchange="onDelimChange()">';
  var delims = [[' ', 'Space'], ['\t', 'Tab'], [',', 'Comma'], ['|', 'Pipe'], [';', 'Semicolon']];
  delims.forEach(function(d) {
    html += '<option value="' + d[0] + '"' + (currentConfig.delimiter === d[0] ? ' selected' : '') + '>' + d[1] + ' (' + (d[0] === ' ' ? 'space' : d[0] === '\t' ? 'tab' : d[0]) + ')</option>';
  });
  html += '<option value="custom">Custom</option>';
  html += '</select></label>';
  html += '<label>Quote: <select id="quote-select" onchange="onDelimChange()">';
  [['"', 'Double quote'], ["'", 'Single quote'], ['', 'None']].forEach(function(q) {
    html += '<option value="' + q[0] + '"' + (currentConfig.quote === q[0] ? ' selected' : '') + '>' + q[1] + '</option>';
  });
  html += '</select></label>';
  html += '<label><input type="checkbox" id="header-row" onchange="onDelimChange()" ' + (currentConfig.header_row ? 'checked' : '') + '> Header row</label>';
  html += '</div></div>';
  return html;
}

function renderDelimiterStep2() {
  var html = '<div class="card"><h3>Step 2: Preview & Name Columns</h3>';
  html += '<div id="preview-table" style="overflow-x:auto;margin-top:0.75rem;">';
  html += '<p style="color:var(--text-muted);">Configure delimiter above to preview</p></div></div>';
  return html;
}

function renderFixedWidthStep1() {
  var html = '<div class="card"><h3>Step 1: Column Positions</h3>';
  html += '<p style="font-size:0.85rem;color:var(--text-muted);margin-bottom:0.5rem;">Click on the preview line to add or remove column splits. Adjust positions below.</p>';
  html += '<div id="fw-ruler-container"><p style="color:var(--text-muted);">Loading preview...</p></div>';
  html += '</div>';
  return html;
}

function renderFixedWidthStep2() {
  var html = '<div class="card"><h3>Step 2: Preview & Name Columns</h3>';
  html += '<div id="preview-table" style="overflow-x:auto;margin-top:0.75rem;"></div>';
  html += '</div>';

  html += '<div class="card"><h3>Column Definitions</h3>';
  html += '<div id="fw-col-defs" style="display:flex;flex-direction:column;gap:0.5rem;margin-top:0.75rem;"></div>';
  html += '</div>';
  return html;
}

function renderSaveStep() {
  var html = '<div class="card"><h3>Step 3: Save Config & Convert</h3>';

  // Saved profiles selector
  if (savedConfigsForBucket.length) {
    html += '<div style="margin-bottom:0.75rem;">';
    html += '<label style="font-size:0.85rem;color:var(--text-muted);margin-right:0.5rem;">Load saved config:</label>';
    html += '<select id="profile-selector" onchange="loadProfile(this.value)" style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:var(--radius);padding:0.3rem 0.5rem;font-size:0.85rem;min-width:200px;">';
    html += '<option value="">-- Select a saved config --</option>';
    for (var pi = 0; pi < savedConfigsForBucket.length; pi++) {
      var pc = savedConfigsForBucket[pi];
      var label = (pc.profile_name || pc.pattern);
      html += '<option value="' + pi + '">' + escHtml(label) + '</option>';
    }
    html += '</select>';
    html += '</div>';
  }

  html += '<div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap;margin-top:0.75rem;">';
  html += '<label style="font-size:0.85rem;color:var(--text-muted);">Profile name:';
  html += ' <input type="text" id="profile-name" value="' + escHtml(currentConfig.profile_name || '') + '" placeholder="e.g. Apache logs" style="width:200px;font-family:monospace;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.3rem 0.5rem;" onchange="currentConfig.profile_name=this.value"></label>';
  html += '<label style="font-size:0.85rem;color:var(--text-muted);">Pattern:';
  html += ' <input type="text" id="config-pattern" value="' + escHtml(currentConfig.pattern) + '" style="width:300px;font-family:monospace;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.3rem 0.5rem;"></label>';
  html += '<button class="btn" onclick="saveConfig()">Save Config</button>';
  html += '<button class="btn btn-secondary" onclick="saveAndConvert()">Save & Convert</button>';
  html += '</div><p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.5rem;">Applies to all files matching this pattern (e.g., <code>*.log</code>).</p>';
  html += '</div>';
  return html;
}

function renderFixedWidthRuler() {
  var container = document.getElementById('fw-ruler-container');
  if (!container || !cachedPreviewLines.length) return;

  var line = cachedPreviewLines[0];
  var maxLen = line.length;

  var rulerHtml = colorizeRulerSegments(line, maxLen);

  var tickLine = '';
  for (var i = 0; i < maxLen; i++) {
    if (i % 10 === 0) {
      var num = String(i);
      tickLine += num;
      i += num.length - 1;
    } else {
      tickLine += '\u00B7';
    }
  }

  container.innerHTML = '<div class="fw-ruler-wrap">' +
    '<div class="fw-ruler-line" id="fw-ruler-line">' + rulerHtml + '</div>' +
    '<div class="fw-ruler-ticks">' + escHtml(tickLine) + '</div>' +
    '</div>' +
    '<div id="fw-pos-inputs" class="fw-pos-inputs"></div>' +
    '<button class="btn btn-secondary fw-add-col" onclick="addFixedWidthColumn()">+ Column</button>';

  document.getElementById('fw-ruler-line').addEventListener('click', handleRulerClick);
  renderFixedWidthPosInputs();
}

function colorizeRulerSegments(line, maxLen) {
  var cols = currentConfig.columns;
  var result = '';
  for (var i = 0; i < maxLen; i++) {
    for (var c = 1; c < cols.length; c++) {
      var sp = cols[c].start;
      if (sp !== undefined && sp !== null && sp === i) {
        result += '<span class="fw-split-marker" data-split="' + i + '"></span>';
        break;
      }
    }
    var colIdx = findColForPos(i);
    var ch = line[i] === ' ' ? '\u00B7' : line[i];
    if (colIdx === -1) {
      // Gap — no column covers this position, show dimmed without background
      result += '<span class="fw-segment" style="opacity:0.35;" data-pos="' + i + '">' + escHtml(ch) + '</span>';
    } else {
      var segClass = 'fw-seg-' + (colIdx % 8);
      result += '<span class="fw-segment ' + segClass + '" data-pos="' + i + '">' + escHtml(ch) + '</span>';
    }
  }
  return result;
}

function findColForPos(pos) {
  var cols = currentConfig.columns;
  for (var i = cols.length - 1; i >= 0; i--) {
    var start = cols[i].start !== undefined && cols[i].start !== null ? cols[i].start : 0;
    var end = cols[i].end !== undefined && cols[i].end !== null ? cols[i].end : Infinity;
    if (pos >= start && pos < end) {
      return i;
    }
  }
  return -1; // gap
}

function handleRulerClick(e) {
  var target = e.target;
  var posStr = target.getAttribute('data-pos');
  if (posStr === null) return;
  var pos = parseInt(posStr);

  var cols = currentConfig.columns;
  for (var i = 1; i < cols.length; i++) {
    var splitPos = cols[i].start;
    if (splitPos !== undefined && splitPos !== null && Math.abs(pos - splitPos) <= 2) {
      cols.splice(i, 1);
      renderFixedWidthRuler();
      updatePreview();
      renderFixedWidthColDefs();
      return;
    }
  }

  var colIdx = findColForPos(pos);
  if (colIdx === -1) {
    // Clicked in a gap — add column after the previous column
    var insertAfter = 0;
    for (var i = cols.length - 1; i >= 0; i--) {
      var s = cols[i].start !== undefined && cols[i].start !== null ? cols[i].start : 0;
      if (s <= pos) {
        insertAfter = i;
        break;
      }
    }
    var newCol = {
      name: 'col' + cols.length,
      type: 'VARCHAR',
      start: pos
    };
    cols.splice(insertAfter + 1, 0, newCol);
  } else {
    // Split existing column
    var newCol = {
      name: 'col' + cols.length,
      type: 'VARCHAR',
      start: pos
    };
    // Set end of previous column to this position
    if (cols[colIdx].end === undefined || cols[colIdx].end === null || cols[colIdx].end > pos) {
      cols[colIdx].end = pos;
    }
    cols.splice(colIdx + 1, 0, newCol);
  }
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthColDefs();
  renderFixedWidthPosInputs();
}

function renderFixedWidthPosInputs() {
  var container = document.getElementById('fw-pos-inputs');
  if (!container) return;

  var html = '';
  for (var i = 0; i < currentConfig.columns.length; i++) {
    var col = currentConfig.columns[i];
    html += '<div class="fw-col-group">';
    html += '<span style="font-size:0.75rem;color:var(--text-muted);width:12px;">' + (i+1) + ':</span>';
    html += '<input type="number" value="' + (col.start !== undefined && col.start !== null ? col.start : 0) + '" min="0" onchange="updateColStart(' + i + ', this.value)" title="Start" style="width:55px;">';
    html += '<input type="number" value="' + (col.end !== undefined && col.end !== null ? col.end : '') + '" min="0" onchange="updateColEnd(' + i + ', this.value)" title="End" placeholder="end" style="width:55px;">';
    html += '<button class="fw-del-col" onclick="removeFixedWidthColumn(' + i + ')" title="Delete column">\u00D7</button>';
    html += '</div>';
  }
  container.innerHTML = html;
}

function updateColStart(idx, val) {
  var v = val === '' ? null : parseInt(val);
  currentConfig.columns[idx].start = v;
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthColDefs();
}

function updateColEnd(idx, val) {
  var v = (val === '' || val === null) ? null : parseInt(val);
  currentConfig.columns[idx].end = v;
  updatePreview();
}

function updateColName(idx, val) {
  currentConfig.columns[idx].name = val;
}

function updateColType(idx, val) {
  currentConfig.columns[idx].type = val;
}

function updateColTypeWithFormat(idx, val) {
  currentConfig.columns[idx].type = val;
  renderFixedWidthColDefs();
}

function updateColFormat(idx, val) {
  currentConfig.columns[idx].format = val;
}

function addFixedWidthColumn() {
  var cols = currentConfig.columns;
  var lastCol = cols[cols.length - 1];
  var start = 0;
  if (lastCol && lastCol.end !== undefined && lastCol.end !== null) {
    start = lastCol.end;
  } else if (lastCol && lastCol.start !== undefined && lastCol.start !== null) {
    start = lastCol.start + 10;
  }
  cols.push({name: 'col' + cols.length, type: 'VARCHAR', start: start});
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthPosInputs();
  renderFixedWidthColDefs();
}

function removeFixedWidthColumn(idx) {
  var cols = currentConfig.columns;
  if (cols.length <= 1) return;
  cols.splice(idx, 1);
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthPosInputs();
  renderFixedWidthColDefs();
}

function renderFixedWidthColDefs() {
  var container = document.getElementById('fw-col-defs');
  if (!container) return;
  var html = '';
  for (var i = 0; i < currentConfig.columns.length; i++) {
    var col = currentConfig.columns[i];
    var showFormat = col.type === 'TIMESTAMP' || col.type === 'DATE' || col.type === 'TIME';
    html += '<div style="display:flex;gap:0.5rem;align-items:center;background:var(--surface-2);padding:0.5rem;border-radius:var(--radius);flex-wrap:wrap;">';
    html += '<span style="font-size:0.8rem;color:var(--text-muted);font-weight:600;min-width:2rem;">' + (i+1) + '.</span>';
    html += '<input type="text" value="' + escHtml(col.name) + '" placeholder="name" style="width:120px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.25rem 0.4rem;font-size:0.8rem;font-family:monospace;" onchange="updateColName(' + i + ', this.value)">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.2rem;font-size:0.75rem;" onchange="updateColTypeWithFormat(' + i + ', this.value)">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP','DATE','TIME'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    if (showFormat) {
      html += '<input type="text" value="' + escHtml(col.format || '') + '" placeholder="strptime format e.g. %Y-%m-%d" style="width:200px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.2rem 0.3rem;font-size:0.8rem;font-family:monospace;" onchange="updateColFormat(' + i + ', this.value)">';
    }
    html += '<span style="font-size:0.8rem;color:var(--text-muted);">Start:</span>';
    html += '<input type="number" value="' + (col.start !== undefined && col.start !== null ? col.start : 0) + '" min="0" style="width:60px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.2rem 0.3rem;font-size:0.8rem;font-family:monospace;" onchange="updateColStart(' + i + ', this.value)">';
    html += '<span style="font-size:0.8rem;color:var(--text-muted);">End:</span>';
    html += '<input type="number" value="' + (col.end !== undefined && col.end !== null ? col.end : '') + '" min="0" style="width:60px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.2rem 0.3rem;font-size:0.8rem;font-family:monospace;" placeholder="end" onchange="updateColEnd(' + i + ', this.value)">';
    html += '<button class="fw-del-col" onclick="removeFixedWidthColumn(' + i + ')" title="Delete column" style="margin-left:auto;">\u00D7</button>';
    html += '</div>';
  }
  container.innerHTML = html;
}

function updatePreview() {
  var container = document.getElementById('preview-table');
  if (!container) return;

  if (cachedPreviewLines.length === 0) {
    container.innerHTML = '<p style="color:var(--text-muted);">No preview data.</p>';
    return;
  }

  if (currentConfig.mode === 'delimiter') {
    updateDelimiterPreview(container);
  } else {
    updateFixedWidthPreview(container);
  }
}

function updateDelimiterPreview(container) {
  var delim = currentConfig.delimiter;
  var numCols = cachedPreviewLines.length > 0 ? cachedPreviewLines[0].split(delim).length : 0;

  // Sync columns
  if (currentConfig.columns.length !== numCols) {
    currentConfig.columns = [];
    for (var i = 0; i < numCols; i++) {
      currentConfig.columns.push({name: 'col' + i, type: 'VARCHAR'});
    }
  }

  // Compute inline distribution data from preview lines
  var distData = [];
  for (var ci = 0; ci < numCols; ci++) {
    var freq = {};
    var total = 0;
    cachedPreviewLines.forEach(function(line) {
      var cells = line.split(delim);
      if (cells[ci] !== undefined) {
        var v = cells[ci].trim();
        freq[v] = (freq[v] || 0) + 1;
        total++;
      }
    });
    var entries = Object.entries(freq).sort(function(a,b) { return b[1] - a[1]; }).slice(0, 5);
    var topTotal = entries.reduce(function(s, e) { return s + e[1]; }, 0);
    distData.push({entries: entries, total: total, topPct: total > 0 ? topTotal / total * 100 : 0});
  }

  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';

  // Column headers with inline distribution bars
  html += '<div style="display:flex;gap:2px;margin-bottom:0.25rem;">';
  for (var ci = 0; ci < numCols; ci++) {
    var col = currentConfig.columns[ci];
    html += '<div style="flex:1;min-width:80px;padding:0 0.25rem;">';
    // Column name + type badge
    html += '<div style="font-size:0.7rem;font-weight:600;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + escHtml(col.name) + ' <span style="color:var(--text-muted);font-weight:400;">' + col.type + '</span></div>';
    // Distribution bar
    if (distData[ci] && distData[ci].entries.length) {
      html += '<div style="display:flex;gap:1px;height:6px;margin-top:2px;border-radius:2px;overflow:hidden;">';
      distData[ci].entries.forEach(function(e) {
        var pct = distData[ci].total > 0 ? (e[1] / distData[ci].total * 100) : 0;
        html += '<div style="height:100%;width:' + pct + '%;background:' + (typeof PANEL_COLORS !== 'undefined' ? PANEL_COLORS[ci % PANEL_COLORS.length] : '#0065FF') + ';" title="' + (e[0] ? e[0].replace(/"/g,'&quot;').replace(/&/g,'&amp;') : '') + ': ' + e[1] + '"></div>';
      });
      html += '</div>';
    }
    html += '</div>';
  }
  html += '</div>';

  // Data rows
  cachedPreviewLines.forEach(function(line) {
    var cells = line.split(delim);
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    cells.forEach(function(cell, ci) {
      html += '<span style="flex:1;min-width:80px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
    });
    html += '</div>';
  });
  html += '</div>';

  // Column editor below
  html += '<div style="margin-top:0.75rem;"><h4 style="font-size:0.85rem;margin-bottom:0.5rem;">Column Names & Types</h4>';
  html += '<div style="display:flex;gap:0.5rem;flex-wrap:wrap;">';
  currentConfig.columns.forEach(function(col, idx) {
    html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
    html += '<input type="text" value="' + escHtml(col.name) + '" style="width:100px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="updateColName(' + idx + ', this.value)">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="updateColType(' + idx + ', this.value)">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    html += '</div>';
  });
  html += '</div></div>';

  container.innerHTML = html;
}

function updateFixedWidthPreview(container) {
  if (!currentConfig.columns.length || currentConfig.columns[0].start === undefined) {
    container.innerHTML = '<p style="color:var(--text-muted);">Click on the preview line above to define column positions.</p>';
    return;
  }

  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';

  // Column headers
  html += '<div style="display:flex;gap:2px;margin-bottom:0.25rem;">';
  for (var ci = 0; ci < currentConfig.columns.length; ci++) {
    var col = currentConfig.columns[ci];
    html += '<div style="flex:1;min-width:60px;padding:0 0.25rem;">';
    html += '<div style="font-size:0.7rem;font-weight:600;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">' + escHtml(col.name) + ' <span style="color:var(--text-muted);font-weight:400;">' + col.type + '</span></div>';
    html += '</div>';
  }
  html += '</div>';

  // Data rows
  cachedPreviewLines.forEach(function(line) {
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    for (var i = 0; i < currentConfig.columns.length; i++) {
      var col = currentConfig.columns[i];
      var start = col.start !== undefined && col.start !== null ? col.start : 0;
      var end = col.end !== undefined && col.end !== null ? col.end : line.length;
      var cell = line.slice(start, end);
      html += '<span style="flex:1;min-width:60px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
    }
    html += '</div>';
  });
  html += '</div>';
  container.innerHTML = html;
}

function onDelimChange() {
  currentConfig.delimiter = document.getElementById('delim-select').value;
  if (currentConfig.delimiter === 'custom') {
    currentConfig.delimiter = prompt('Enter delimiter character:') || ' ';
  }
  currentConfig.quote = document.getElementById('quote-select').value;
  currentConfig.header_row = document.getElementById('header-row').checked;
  updatePreview();
}

function loadProfile(idx) {
  if (idx === '') return;
  var pc = savedConfigsForBucket[parseInt(idx)];
  if (!pc) return;
  currentConfig.mode = pc.mode || 'delimiter';
  currentConfig.delimiter = pc.delimiter || ' ';
  currentConfig.quote = pc.quote || '"';
  currentConfig.header_row = pc.header_row || false;
  currentConfig.columns = pc.columns || [];
  currentConfig.pattern = pc.pattern || currentConfig.pattern;
  currentConfig.profile_name = pc.profile_name || '';
  // Also set the file pattern from the selected config
  var patternInput = document.getElementById('config-pattern');
  if (patternInput) patternInput.value = currentConfig.pattern;
  var profileInput = document.getElementById('profile-name');
  if (profileInput) profileInput.value = currentConfig.profile_name;
  renderConfig();
}

function saveConfig() {
  var patternInput = document.getElementById('config-pattern');
  if (patternInput) currentConfig.pattern = patternInput.value;
  var profileInput = document.getElementById('profile-name');
  if (profileInput) currentConfig.profile_name = profileInput.value;
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
  var patternInput = document.getElementById('config-pattern');
  if (patternInput) currentConfig.pattern = patternInput.value;
  var profileInput = document.getElementById('profile-name');
  if (profileInput) currentConfig.profile_name = profileInput.value;

  // Use tabState when available (SPA mode), fall back to URL params
  var file;
  var projectId;
  var bucket = currentConfig.bucket;
  if (typeof tabState !== 'undefined' && tabState.browse.project) {
    var convertible = tabState.browse.selectedFiles.filter(function(p) { return !isQueryable(p); });
    file = convertible.length ? convertible[0].replace(/^s3:\/\/[^\/]+\//, '') : null;
    projectId = tabState.browse.project;
  } else {
    var params = new URLSearchParams(window.location.search);
    file = params.get('file');
    projectId = params.get('project') || selProject;
  }
  if (!file) { alert('No file specified'); return; }

  // Show progress in the target page
  var statusDiv = document.createElement('div');
  statusDiv.className = 'card';
  statusDiv.innerHTML = '<h3>Converting...</h3><div id="conv-status"><p style="color:var(--text-muted);">Saving config...</p></div>';
  document.querySelector('.config-layout').appendChild(statusDiv);

  fetch('/convert/columns', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(currentConfig)
  })
  .then(function(r) { return r.json(); })
  .then(function(saveResult) {
    if (saveResult.error) {
      document.getElementById('conv-status').innerHTML = '<span class="error">Error saving config: ' + saveResult.error + '</span>';
      return;
    }
    document.getElementById('conv-status').innerHTML = '<p style="color:var(--text-muted);">Starting conversion...</p>';
    return fetch('/convert?project=' + encodeURIComponent(projectId), {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({bucket: bucket, files: [file], delete_original: false})
    });
  })
  .then(function(r) {
    if (!r) return;
    if (!r.ok) { return r.json().then(function(e) { throw new Error(e.error || 'conversion request failed'); }); }
    return r.json();
  })
  .then(function(job) {
    if (!job) return;
    if (job.error) { document.getElementById('conv-status').innerHTML = '<span class="error">' + job.error + '</span>'; return; }
    document.getElementById('conv-status').innerHTML = '<p style="color:var(--text-muted);">Conversion job started. Monitoring...</p>';
    pollSaveConvertStatus(job.job_id, projectId);
  })
  .catch(function(e) {
    var s = document.getElementById('conv-status');
    if (s) s.innerHTML = '<span class="error">' + e.message + '</span>';
    else alert(e.message);
  });
}

function pollSaveConvertStatus(jobId, projectId) {
  var div = document.getElementById('conv-status');
  if (!div) return;

  fetch('/convert/status/' + encodeURIComponent(jobId))
    .then(function(r) { return r.json(); })
    .then(function(job) {
      var pct = job.total > 0 ? Math.round(job.completed / job.total * 100) : 0;
      var html = '<div style="margin-bottom:0.75rem;">';
      html += '<div style="display:flex;align-items:center;gap:0.75rem;margin-bottom:0.5rem;">';
      html += '<div style="flex:1;height:8px;background:var(--surface-2);border-radius:4px;overflow:hidden;">';
      html += '<div style="height:100%;width:' + pct + '%;background:var(--primary);border-radius:4px;transition:width 0.5s;"></div></div>';
      html += '<span style="font-size:0.85rem;color:var(--text-muted);">' + job.completed + '/' + job.total + '</span>';
      html += '</div>';
      if (job.results && job.results.length) {
        job.results.forEach(function(r) {
          var icon = r.status === 'done' ? '✅' : r.status === 'error' ? '❌' : r.status === 'running' ? '⏳' : '⬜';
          var err = r.error ? ': ' + r.error : '';
          html += '<div style="font-size:0.85rem;">' + icon + ' ' + r.file + ' — ' + r.status + err + '</div>';
        });
      }
      html += '</div>';

      if (job.status === 'running') {
        html += '<p style="font-size:0.8rem;color:var(--text-muted);">Running...</p>';
        div.innerHTML = html;
        setTimeout(function() { pollSaveConvertStatus(jobId, projectId); }, 2000);
      } else if (job.status === 'done') {
        html += '<p style="color:var(--green);font-weight:600;">Conversion complete!</p>';
        html += '<button class="btn" onclick="if(typeof switchTab===\'function\'){switchTab(\'browse\')}else{window.location.href=\'/browse?project=' + encodeURIComponent(projectId) + '\'}">Go to Console</button>';
        div.innerHTML = html;
      } else {
        html += '<span class="error">Conversion failed</span>';
        div.innerHTML = html;
      }
    })
    .catch(function(e) {
      div.innerHTML = '<span class="error">Status poll error: ' + e.message + '</span>';
    });
}

function escHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
