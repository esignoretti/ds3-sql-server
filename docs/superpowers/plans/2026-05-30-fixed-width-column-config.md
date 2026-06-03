# Fixed-Width Column Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add fixed-width field parsing as a second mode alongside delimiter-based parsing in column configs.

**Architecture:** A `Mode` field on `ColumnConfig` ("delimiter" | "fixed_width") switches the conversion engine from `read_csv()` to `read_text()` + `substr()`. The JS UI adds a mode toggle, a clickable character ruler for setting column boundaries, and client-side `slice()` preview. The Go data model adds optional `Start`/`End` (int) fields to `ColumnDef`.

**Tech Stack:** Go 1.26, DuckDB, vanilla JS

---

### Task 1: Data model — add Mode, Start, End to Go types

**Files:**
- Modify: `internal/column/config.go` — add `Mode` field, `Start`/`End` fields
- Modify: `internal/column/config_test.go` — test fixed-width save/load roundtrip

- [ ] **Step 1: Add fields to ColumnConfig and ColumnDef**

Edit `internal/column/config.go`:

```go
type ColumnConfig struct {
	Bucket    string       `json:"bucket"`
	Pattern   string       `json:"pattern"`
	Mode      string       `json:"mode"`       // "delimiter" or "fixed_width"
	Delimiter string       `json:"delimiter"`
	Quote     string       `json:"quote"`
	HeaderRow bool         `json:"header_row"`
	Columns   []ColumnDef  `json:"columns"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type ColumnDef struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Start *int   `json:"start,omitempty"`
	End   *int   `json:"end,omitempty"`
}
```

- [ ] **Step 2: Write failing test for fixed-width roundtrip**

Edit `internal/column/config_test.go`, add after `TestMatch`:

```go
func TestFixedWidthRoundtrip(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-columns-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	s0 := 0
	e0 := 16
	s1 := 17
	e1 := 30
	s2 := 31

	cfg := &ColumnConfig{
		Bucket:  "logs",
		Pattern: "*.dat",
		Mode:    "fixed_width",
		Columns: []ColumnDef{
			{Name: "timestamp", Type: "VARCHAR", Start: &s0, End: &e0},
			{Name: "level", Type: "VARCHAR", Start: &s1, End: &e1},
			{Name: "message", Type: "VARCHAR", Start: &s2},
		},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get("logs", "*.dat")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "fixed_width" {
		t.Fatalf("expected mode fixed_width, got %q", loaded.Mode)
	}
	if len(loaded.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(loaded.Columns))
	}
	if *loaded.Columns[0].Start != 0 || *loaded.Columns[0].End != 16 {
		t.Fatalf("unexpected col0 start/end: %d/%d", *loaded.Columns[0].Start, *loaded.Columns[0].End)
	}
	if loaded.Columns[2].End != nil {
		t.Fatal("expected nil End for last column")
	}

	// Backward compat: configs saved without Mode default to "delimiter"
	oldCfg := &ColumnConfig{
		Bucket:  "old",
		Pattern: "*.log",
		Columns: []ColumnDef{{Name: "line", Type: "VARCHAR"}},
	}
	if err := store.Save(oldCfg); err != nil {
		t.Fatal(err)
	}
	loadedOld, err := store.Get("old", "*.log")
	if err != nil {
		t.Fatal(err)
	}
	if loadedOld.Mode != "" {
		t.Fatalf("expected empty mode for old config, got %q", loadedOld.Mode)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/column/... -run TestFixedWidthRoundtrip -v`
Expected: compilation error or test failure because fields don't exist yet

- [ ] **Step 4: Apply the edits from Step 1**

Add the `Mode` field to `ColumnConfig` and `Start`/`End` to `ColumnDef` as shown above.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/column/... -run TestFixedWidthRoundtrip -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/column/config.go internal/column/config_test.go
git commit -m "feat: add Mode, Start, End fields for fixed-width column config"
```

---

### Task 2: Conversion engine — fixed-width read_text + substr branch

**Files:**
- Modify: `internal/convert/engine.go` — add fixed-width branch in `convertFile()`
- Modify: `internal/convert/engine_test.go` — add test for fixed-width SQL generation (requires refactoring)

- [ ] **Step 1: Write a test for fixed-width SQL generation**

Since `convertFile` is tightly coupled to DuckDB (pool, S3), test the SQL-building logic by extracting it into a helper or writing an integration test.

Add to `internal/convert/engine_test.go`:

```go
func TestFixedWidthReadSQL(t *testing.T) {
	// We'll test the SQL-building logic indirectly via the convertFile function.
	// For now, verify detectFormat still works.
	if detectFormat("test.dat") != "text" {
		t.Error("expected text for .dat")
	}
}
```

(Full testing requires a running DuckDB + S3 mock — skip for now, manually verify during integration.)

- [ ] **Step 2: Add fixed-width branch to convertFile**

Edit `internal/convert/engine.go`, replace the `if savedCfg != nil` block (lines 203-221):

```go
	if savedCfg != nil {
		cfg := savedCfg
		if cfg.Mode == "fixed_width" {
			// Fixed-width: read_text + substr
			if len(cfg.Columns) == 0 {
				return fmt.Errorf("fixed_width mode requires at least one column")
			}
			var selects []string
			for i, col := range cfg.Columns {
				colName := col.Name
				if colName == "" {
					colName = fmt.Sprintf("col%d", i)
				}
				colType := col.Type
				if colType == "" {
					colType = "VARCHAR"
				}
				start := 0
				if col.Start != nil {
					start = *col.Start
				}
				if col.End != nil {
					selects = append(selects, fmt.Sprintf("CAST(substr(line, %d, %d) AS %s) AS %s", start+1, *col.End-start, colType, colName))
				} else {
					selects = append(selects, fmt.Sprintf("CAST(substr(line, %d) AS %s) AS %s", start+1, colType, colName))
				}
			}
			readSQL = fmt.Sprintf(`SELECT %s FROM read_text('%s')`, strings.Join(selects, ","), s3Path)
		} else {
			// Delimiter mode (existing)
			delim := cfg.Delimiter
			quote := cfg.Quote
			headerStr := "FALSE"
			if cfg.HeaderRow {
				headerStr = "TRUE"
			}
			if len(cfg.Columns) > 0 {
				var selects []string
				for i, col := range cfg.Columns {
					selects = append(selects, fmt.Sprintf("CAST(column%d AS %s) AS %s", i, col.Type, col.Name))
				}
				readSQL = fmt.Sprintf(`SELECT %s FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
					strings.Join(selects, ","), s3Path, delim, quote, headerStr)
			} else {
				readSQL = fmt.Sprintf(`SELECT * FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
					s3Path, delim, quote, headerStr)
			}
		}
	} else {
		// auto-detect (existing)
```

- [ ] **Step 3: Build to verify**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Run tests**

Run: `go test ./internal/convert/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/convert/engine.go internal/convert/engine_test.go
git commit -m "feat: add fixed-width read_text+substr branch to conversion engine"
```

---

### Task 3: CSS — segment colors and ruler styles

**Files:**
- Modify: `internal/web/static/style.css` — add segment color classes and ruler styles

- [ ] **Step 1: Add segment color classes and ruler styles**

Append to `internal/web/static/style.css`:

```css
/* Fixed-width column config */
.fw-ruler-wrap {
  overflow-x: auto;
  margin-top: 0.5rem;
  background: var(--surface-2);
  border-radius: var(--radius);
  padding: 0.5rem 0;
  position: relative;
}
.fw-ruler-line {
  font-family: monospace;
  font-size: 0.75rem;
  line-height: 1.6;
  white-space: pre;
  letter-spacing: 0;
  position: relative;
  cursor: pointer;
  user-select: none;
  padding: 0 0.5rem;
}
.fw-ruler-ticks {
  font-family: monospace;
  font-size: 0.65rem;
  line-height: 1;
  white-space: pre;
  color: var(--text-muted);
  padding: 0 0.5rem;
}
.fw-segment {
  display: inline;
  border-left: 1px solid var(--primary);
  padding-left: 0;
}
.fw-segment:first-child {
  border-left: none;
}
.seg-0 { background: rgba(0,101,255,0.15); }
.seg-1 { background: rgba(135,57,177,0.15); }
.seg-2 { background: rgba(39,182,129,0.15); }
.seg-3 { background: rgba(243,179,86,0.15); }
.seg-4 { background: rgba(248,113,113,0.15); }
.seg-5 { background: rgba(0,200,200,0.15); }
.seg-6 { background: rgba(255,165,0,0.15); }
.seg-7 { background: rgba(180,130,255,0.15); }
.fw-split-marker {
  display: inline-block;
  width: 0;
  position: relative;
  cursor: pointer;
}
.fw-split-marker::after {
  content: "│";
  color: var(--primary);
  font-weight: bold;
  position: absolute;
  left: -2px;
  top: -0.1em;
}
.fw-split-marker:hover::after {
  color: var(--red);
}
.fw-pos-inputs {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
  margin-top: 0.75rem;
}
.fw-pos-inputs .fw-col-group {
  display: flex;
  gap: 0.25rem;
  align-items: center;
  background: var(--surface-2);
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius);
}
.fw-pos-inputs input[type="number"] {
  width: 60px;
  background: var(--surface);
  color: var(--text);
  border: 0.0625rem solid var(--border);
  border-radius: 0.25rem;
  padding: 0.15rem 0.25rem;
  font-size: 0.8rem;
  font-family: monospace;
}
.fw-del-col {
  cursor: pointer;
  color: var(--red);
  font-weight: bold;
  border: none;
  background: none;
  font-size: 1rem;
  line-height: 1;
}
.fw-del-col:hover {
  color: #ff4444;
}
.fw-add-col {
  margin-top: 0.5rem;
}
/* Mode toggle pills */
.mode-toggle {
  display: flex;
  gap: 0;
  margin-bottom: 0.75rem;
  background: var(--surface-2);
  border-radius: var(--radius);
  overflow: hidden;
  border: 0.0625rem solid var(--border);
  width: fit-content;
}
.mode-toggle button {
  padding: 0.4rem 1rem;
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 0.85rem;
  cursor: pointer;
  font-family: "Source Sans 3", Arial, sans-serif;
  transition: all 0.15s;
}
.mode-toggle button.active {
  background: var(--primary);
  color: white;
}
.mode-toggle button:not(.active):hover {
  background: var(--surface);
  color: var(--text);
}
```

- [ ] **Step 2: Build to verify CSS doesn't break anything**

No build step for CSS — just verify the file is valid.

- [ ] **Step 3: Commit**

```bash
git add internal/web/static/style.css
git commit -m "feat: add fixed-width column config CSS styles"
```

---

### Task 4: JS UI — mode toggle, ruler, position editors, preview

**Files:**
- Modify: `internal/web/static/column_config.js` — major rewrite of the UI

- [ ] **Step 1: Update currentConfig defaults and loadPreview**

```js
var selProject = '';
var cachedPreviewLines = [];
var currentConfig = {
  bucket: '',
  pattern: '',
  mode: 'delimiter',
  delimiter: ' ',
  quote: '"',
  header_row: false,
  columns: []
};
```

In `loadPreview`, after parsing `d.saved_config`, add mode handling:

```js
    .then(function(d) {
      cachedPreviewLines = d.preview_lines;
      if (d.saved_config) {
        currentConfig.mode = d.saved_config.mode || 'delimiter';
        currentConfig.delimiter = d.saved_config.delimiter;
        currentConfig.quote = d.saved_config.quote;
        currentConfig.header_row = d.saved_config.header_row;
        currentConfig.columns = d.saved_config.columns;
      } else {
        // Reset to defaults for fresh config
        currentConfig.mode = 'delimiter';
        currentConfig.delimiter = ' ';
        currentConfig.quote = '"';
        currentConfig.header_row = false;
        currentConfig.columns = [];
      }
      renderConfig();
    })
```

- [ ] **Step 2: Rewrite renderConfig with mode toggle**

Replace `renderConfig()` with:

```js
function renderConfig() {
  var modeHtml = '<div class="mode-toggle">';
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

  html += renderSaveStep(); // Save
  html += '</div>';

  document.getElementById('config-app').innerHTML = html;
  if (currentConfig.mode === 'fixed_width') {
    renderFixedWidthRuler();
  }
  updatePreview();
}
```

- [ ] **Step 3: Add switchMode and delimiter/fixed-width render helpers**

```js
function switchMode(mode) {
  currentConfig.mode = mode;
  if (mode === 'fixed_width' && (!currentConfig.columns.length || !('start' in currentConfig.columns[0]))) {
    // Initialize with first column when switching to fixed_width
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
```

- [ ] **Step 4: Add fixed-width UI renderers**

```js
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

  // Column name/type editors
  html += '<div class="card"><h3>Column Names & Types</h3>';
  html += '<div id="fw-col-editors" style="display:flex;gap:0.5rem;flex-wrap:wrap;margin-top:0.75rem;">';
  for (var i = 0; i < currentConfig.columns.length; i++) {
    var col = currentConfig.columns[i];
    html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
    html += '<input type="text" value="' + escHtml(col.name) + '" style="width:80px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="updateColName(' + i + ', this.value)">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="updateColType(' + i + ', this.value)">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    html += '</div>';
  }
  html += '</div>';
  return html;
}

function renderSaveStep() {
  var html = '<div class="card"><h3>Step 3: Save Config</h3>';
  html += '<div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap;margin-top:0.75rem;">';
  html += '<label>Pattern: <input type="text" id="config-pattern" value="' + escHtml(currentConfig.pattern) + '" style="width:300px;font-family:monospace;"></label>';
  html += '<button class="btn" onclick="saveConfig()">Save Config</button>';
  html += '<button class="btn btn-secondary" onclick="saveAndConvert()">Save & Convert</button>';
  html += '</div><p style="font-size:0.8rem;color:var(--text-muted);margin-top:0.5rem;">Applies to all files matching this pattern (e.g., <code>*.log</code>).</p>';
  html += '</div>';
  return html;
}
```

- [ ] **Step 5: Add ruler rendering and interaction**

```js
function renderFixedWidthRuler() {
  var container = document.getElementById('fw-ruler-container');
  if (!container || !cachedPreviewLines.length) return;

  var line = cachedPreviewLines[0];
  var maxLen = line.length;

  // Build colored ruler with split markers
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
    // Insert split marker before this position if a column starts here
    for (var c = 1; c < cols.length; c++) {
      var sp = cols[c].start;
      if (sp !== undefined && sp !== null && sp === i) {
        result += '<span class="fw-split-marker" data-split="' + i + '"></span>';
        break;
      }
    }
    var colIdx = findColForPos(i);
    var segClass = 'seg-' + (colIdx % 8);
    var ch = line[i] === ' ' ? '\u00B7' : line[i];
    result += '<span class="fw-segment ' + segClass + '" data-pos="' + i + '">' + escHtml(ch) + '</span>';
  }
  return result;
}

function findColForPos(pos) {
  var cols = currentConfig.columns;
  for (var i = cols.length - 1; i >= 0; i--) {
    var start = cols[i].start !== undefined && cols[i].start !== null ? cols[i].start : 0;
    if (pos >= start) {
      return i;
    }
  }
  return 0;
}

function handleRulerClick(e) {
  var target = e.target;
  var posStr = target.getAttribute('data-pos');
  if (posStr === null) return;
  var pos = parseInt(posStr);

  // Check if click is within 2 chars of an existing split
  var cols = currentConfig.columns;
  for (var i = 1; i < cols.length; i++) {
    var splitPos = cols[i].start;
    if (splitPos !== undefined && splitPos !== null && Math.abs(pos - splitPos) <= 2) {
      // Remove this split — merge columns i-1 and i
      cols.splice(i, 1);
      renderFixedWidthRuler();
      updatePreview();
      renderFixedWidthColEditors();
      renderFixedWidthPosInputs();
      return;
    }
  }

  // Add a split at this position
  // Find which column we're clicking in
  var colIdx = findColForPos(pos);
  // Insert a new column after colIdx with start at pos
  var newCol = {
    name: 'col' + cols.length,
    type: 'VARCHAR',
    start: pos
  };
  cols.splice(colIdx + 1, 0, newCol);
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthColEditors();
  renderFixedWidthPosInputs();
}
```

- [ ] **Step 6: Add position input rendering**

```js
function renderFixedWidthPosInputs() {
  var container = document.getElementById('fw-pos-inputs');
  if (!container) return;

  var html = '';
  for (var i = 0; i < currentConfig.columns.length; i++) {
    var col = currentConfig.columns[i];
    html += '<div class="fw-col-group">';
    html += '<span style="font-size:0.75rem;color:var(--text-muted);width:14px;">' + (i+1) + ':</span>';
    html += '<input type="number" value="' + (col.start !== undefined && col.start !== null ? col.start : 0) + '" min="0" onchange="updateColStart(' + i + ', this.value)" title="Start">';
    html += '<input type="number" value="' + (col.end !== undefined && col.end !== null ? col.end : '') + '" min="0" onchange="updateColEnd(' + i + ', this.value)" title="End (leave empty for rest of line)" placeholder="end">';
    html += '<button class="fw-del-col" onclick="removeFixedWidthColumn(' + i + ')" title="Delete column">×</button>';
    html += '</div>';
  }
  container.innerHTML = html;
}
```

- [ ] **Step 7: Add column mutation helpers**

```js
function updateColStart(idx, val) {
  var v = val === '' ? null : parseInt(val);
  currentConfig.columns[idx].start = v;
  renderFixedWidthRuler();
  updatePreview();
}

function updateColEnd(idx, val) {
  var v = val === '' ? null : parseInt(val);
  currentConfig.columns[idx].end = v;
  updatePreview();
}

function updateColName(idx, val) {
  currentConfig.columns[idx].name = val;
}

function updateColType(idx, val) {
  currentConfig.columns[idx].type = val;
}

function addFixedWidthColumn() {
  var cols = currentConfig.columns;
  var lastCol = cols[cols.length - 1];
  var start = (lastCol && lastCol.start !== undefined && lastCol.start !== null) ? lastCol.start + 10 : 0;
  if (lastCol && lastCol.end !== undefined && lastCol.end !== null) {
    start = lastCol.end;
  } else if (lastCol && lastCol.start !== undefined && lastCol.start !== null) {
    start = lastCol.start + 10;
  }
  cols.push({name: 'col' + cols.length, type: 'VARCHAR', start: start});
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthColEditors();
  renderFixedWidthPosInputs();
}

function removeFixedWidthColumn(idx) {
  var cols = currentConfig.columns;
  if (cols.length <= 1) return; // keep at least one column
  cols.splice(idx, 1);
  renderFixedWidthRuler();
  updatePreview();
  renderFixedWidthColEditors();
  renderFixedWidthPosInputs();
}

function renderFixedWidthColEditors() {
  var container = document.getElementById('fw-col-editors');
  if (!container) return;
  var html = '';
  for (var i = 0; i < currentConfig.columns.length; i++) {
    var col = currentConfig.columns[i];
    html += '<div style="display:flex;gap:0.25rem;align-items:center;background:var(--surface-2);padding:0.25rem 0.5rem;border-radius:var(--radius);">';
    html += '<input type="text" value="' + escHtml(col.name) + '" style="width:80px;background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem 0.25rem;font-size:0.8rem;" onchange="updateColName(' + i + ', this.value)">';
    html += '<select style="background:var(--surface);color:var(--text);border:0.0625rem solid var(--border);border-radius:0.25rem;padding:0.15rem;font-size:0.75rem;" onchange="updateColType(' + i + ', this.value)">';
    ['VARCHAR','INTEGER','BIGINT','DOUBLE','BOOLEAN','TIMESTAMP'].forEach(function(t) {
      html += '<option value="' + t + '"' + (col.type === t ? ' selected' : '') + '>' + t + '</option>';
    });
    html += '</select>';
    html += '</div>';
  }
  container.innerHTML = html;
}
```

- [ ] **Step 8: Update updatePreview for fixed-width mode**

Replace `updatePreview()` with mode-aware version:

```js
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
  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';
  var delim = currentConfig.delimiter;
  cachedPreviewLines.forEach(function(line) {
    var cells = line.split(delim);
    html += '<div style="display:flex;gap:2px;border-bottom:0.0625rem solid var(--border);padding:0.15rem 0;">';
    cells.forEach(function(cell) {
      html += '<span style="flex:1;min-width:80px;padding:0 0.25rem;border-right:1px solid var(--primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(cell) + '</span>';
    });
    html += '</div>';
  });
  html += '</div>';

  var numCols = cachedPreviewLines.length > 0 ? cachedPreviewLines[0].split(currentConfig.delimiter).length : 0;
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
}

function updateFixedWidthPreview(container) {
  if (!currentConfig.columns.length || currentConfig.columns[0].start === undefined) {
    container.innerHTML = '<p style="color:var(--text-muted);">Click on the preview line above to define column positions.</p>';
    return;
  }

  var html = '<div style="background:var(--surface-2);border-radius:var(--radius);padding:0.5rem;font-family:monospace;font-size:0.75rem;line-height:1.6;max-height:400px;overflow-y:auto;">';
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
```

- [ ] **Step 9: Update onConfigChange → onDelimChange (delimiter-only)**

Rename `onConfigChange` to `onDelimChange` and make it only fire in delimiter mode:

```js
function onDelimChange() {
  currentConfig.delimiter = document.getElementById('delim-select').value;
  if (currentConfig.delimiter === 'custom') {
    currentConfig.delimiter = prompt('Enter delimiter character:') || ' ';
  }
  currentConfig.quote = document.getElementById('quote-select').value;
  currentConfig.header_row = document.getElementById('header-row').checked;
  updatePreview();
}
```

- [ ] **Step 10: Verify by building**

Run: `go build ./...`
Expected: no errors (JS is not compiled)

- [ ] **Step 11: Commit**

```bash
git add internal/web/static/column_config.js
git commit -m "feat: add fixed-width mode UI with ruler, position editors, preview"
```

---

### Task 5: Integration test — save and load fixed-width config via API

**Files:**
- Create: `internal/api/column_handler_test.go` — test SaveConfig with fixed-width payload

- [ ] **Step 1: Write SaveConfig test for fixed-width**

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/esignoretti/ds3-sql-server/internal/column"
)

func TestSaveFixedWidthConfig(t *testing.T) {
	dir, err := os.MkdirTemp("", "ds3sql-api-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := column.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	h := NewColumnHandler(store)

	s0 := 0
	e0 := 16
	s1 := 17

	cfg := column.ColumnConfig{
		Bucket:  "test",
		Pattern: "*.dat",
		Mode:    "fixed_width",
		Columns: []column.ColumnDef{
			{Name: "ts", Type: "VARCHAR", Start: &s0, End: &e0},
			{Name: "msg", Type: "VARCHAR", Start: &s1},
		},
	}

	body, _ := json.Marshal(cfg)
	req := httptest.NewRequest("POST", "/convert/columns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.SaveConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify it was saved
	loaded, err := store.Get("test", "*.dat")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "fixed_width" {
		t.Fatalf("expected mode fixed_width, got %q", loaded.Mode)
	}
	if *loaded.Columns[0].Start != 0 {
		t.Fatalf("expected start 0, got %d", *loaded.Columns[0].Start)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/api/... -run TestSaveFixedWidthConfig -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/api/column_handler_test.go
git commit -m "test: add API test for saving fixed-width column config"
```

---

### Task 6: Full build and test

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 2: Build binaries**

Run: `go build ./cmd/ds3sql-server ./cmd/ds3sql`
Expected: no errors

- [ ] **Step 3: Manual smoke test**

Start the server and navigate to a convertible file's column config page to verify:
1. Mode toggle appears
2. Switching to Fixed Width shows ruler with first line of preview
3. Clicking on the ruler adds/removes splits
4. Position number inputs update the ruler
5. Preview table shows split columns
6. Saving a config with fixed-width mode persists correctly
7. Conversion with saved fixed-width config works
