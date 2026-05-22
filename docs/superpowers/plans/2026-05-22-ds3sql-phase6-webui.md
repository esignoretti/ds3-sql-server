# DS3 SQL Server — Phase 6: Web UI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Browse + Query Web UI with server-rendered templates and HTMX. Three pages: login, browse (bucket tree + file list), and query (SQL editor + results table).

**Architecture:** `html/template` for server-rendered HTML. HTMX for dynamic interactions (file listing, query results). Static CSS for styling. No JavaScript build step.

**Tech Stack:** Go `html/template`, HTMX (CDN), CSS

---

### Task 1: Web UI router and template rendering

**Files:**
- Create: `DS3-SQL Server/internal/web/handler.go`
- Create: `DS3-SQL Server/internal/web/templates/login.html`
- Create: `DS3-SQL Server/internal/web/templates/browse.html`
- Create: `DS3-SQL Server/internal/web/templates/query.html`
- Create: `DS3-SQL Server/internal/web/static/style.css`

- [ ] **Step 1: Write the web handler that renders templates**

`DS3-SQL Server/internal/web/handler.go`:

```go
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Handler struct {
	templates *template.Template
}

func NewHandler() (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{templates: tmpl}, nil
}

func (h *Handler) Static() http.Handler {
	staticSub, _ := fs.Sub(staticFS, "static")
	return http.FileServer(http.FS(staticSub))
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", nil)
}

func (h *Handler) BrowsePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "browse.html", nil)
}

func (h *Handler) QueryPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "query.html", nil)
}

func (h *Handler) render(w http.ResponseWriter, tmpl string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, tmpl, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 2: Write login template**

`DS3-SQL Server/internal/web/templates/login.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>DS3 SQL — Login</title>
    <link rel="stylesheet" href="/static/style.css">
    <script src="https://unpkg.com/htmx.org@1.9.12"></script>
</head>
<body>
    <div class="container center">
        <div class="card">
            <h1>DS3 SQL</h1>
            <p class="subtitle">Sign in with your Cubbit account</p>
            <form hx-post="/auth/login" hx-target="#result" hx-swap="innerHTML">
                <div class="form-group">
                    <label for="email">Email</label>
                    <input type="email" id="email" name="email" required class="input">
                </div>
                <div class="form-group">
                    <label for="password">Password</label>
                    <input type="password" id="password" name="password" required class="input">
                </div>
                <button type="submit" class="btn btn-primary">Sign In</button>
            </form>
            <div id="result"></div>
        </div>
    </div>

    <script>
        document.body.addEventListener('htmx:afterRequest', function(evt) {
            if (evt.detail.successful) {
                window.location.href = '/browse';
            }
        });
    </script>
</body>
</html>
```

- [ ] **Step 3: Write browse template**

`DS3-SQL Server/internal/web/templates/browse.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>DS3 SQL — Browse</title>
    <link rel="stylesheet" href="/static/style.css">
    <script src="https://unpkg.com/htmx.org@1.9.12"></script>
</head>
<body>
    <header class="navbar">
        <span class="brand">DS3 SQL</span>
        <nav>
            <a href="/browse">Browse</a>
            <a href="/query">Query</a>
            <a href="/auth/logout" class="text-muted">Logout</a>
        </nav>
    </header>

    <div class="container">
        <div class="browse-layout">
            <aside class="sidebar">
                <h3>Buckets</h3>
                <div id="bucket-list"
                     hx-get="/buckets"
                     hx-trigger="load"
                     hx-swap="innerHTML">
                    Loading...
                </div>
            </aside>
            <main class="content">
                <h3 id="current-path">Select a bucket or prefix</h3>
                <div id="file-list"></div>
            </main>
        </div>
    </div>

    <script>
        window.loadPrefix = function(bucket, prefix) {
            const el = document.getElementById('file-list');
            el.setAttribute('hx-get', '/buckets/' + bucket + '?prefix=' + encodeURIComponent(prefix));
            el.setAttribute('hx-trigger', 'load');
            el.innerHTML = 'Loading...';
            htmx.process(el);
            htmx.trigger(el, 'load');
            document.getElementById('current-path').textContent = bucket + '/' + prefix;
        };
    </script>
</body>
</html>
```

- [ ] **Step 4: Write query template**

`DS3-SQL Server/internal/web/templates/query.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>DS3 SQL — Query</title>
    <link rel="stylesheet" href="/static/style.css">
    <script src="https://unpkg.com/htmx.org@1.9.12"></script>
</head>
<body>
    <header class="navbar">
        <span class="brand">DS3 SQL</span>
        <nav>
            <a href="/browse">Browse</a>
            <a href="/query">Query</a>
            <a href="/auth/logout" class="text-muted">Logout</a>
        </nav>
    </header>

    <div class="container">
        <div class="query-layout">
            <div class="editor-panel">
                <h3>SQL Editor</h3>
                <textarea id="sql-editor" rows="8" class="input code"
                    placeholder="SELECT * FROM read_parquet('s3://bucket/prefix/*.parquet') LIMIT 10"></textarea>
                <div style="display:flex;gap:8px;margin-top:8px;">
                    <button class="btn btn-primary" onclick="runQuery()">▶ Run</button>
                    <button class="btn" onclick="clearResults()">Clear</button>
                </div>
                <div class="text-muted" style="margin-top:4px;font-size:12px;">
                    Ctrl+Enter to run
                </div>
            </div>
            <div class="results-panel">
                <h3>Results</h3>
                <div id="query-status"></div>
                <div id="query-results">
                    <p class="text-muted">Run a query to see results</p>
                </div>
            </div>
        </div>
    </div>

    <script>
        function runQuery() {
            const sql = document.getElementById('sql-editor').value;
            if (!sql) return;

            const status = document.getElementById('query-status');
            const results = document.getElementById('query-results');
            status.innerHTML = 'Running...';
            results.innerHTML = '';

            fetch('/query', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({sql: sql})
            })
            .then(r => r.json())
            .then(data => {
                if (data.error) {
                    status.innerHTML = '<span class="error">Error: ' + data.error + '</span>';
                    return;
                }
                status.innerHTML = '<span class="text-muted">' + data.row_count + ' rows in ' + data.elapsed_ms + 'ms</span>';

                if (data.row_count === 0) {
                    results.innerHTML = '<p class="text-muted">No rows returned</p>';
                    return;
                }

                let html = '<table class="table"><thead><tr>';
                data.columns.forEach(col => {
                    html += '<th>' + col.name + '<br><small>' + col.type + '</small></th>';
                });
                html += '</tr></thead><tbody>';
                data.rows.forEach(row => {
                    html += '<tr>';
                    row.forEach(cell => {
                        html += '<td>' + (cell === null ? '<span class="null">NULL</span>' : escapeHtml(String(cell))) + '</td>';
                    });
                    html += '</tr>';
                });
                html += '</tbody></table>';
                results.innerHTML = html;
            })
            .catch(err => {
                status.innerHTML = '<span class="error">Request failed: ' + err.message + '</span>';
            });
        }

        function clearResults() {
            document.getElementById('query-status').innerHTML = '';
            document.getElementById('query-results').innerHTML = '<p class="text-muted">Run a query to see results</p>';
        }

        function escapeHtml(s) {
            return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
        }

        document.addEventListener('keydown', function(e) {
            if (e.ctrlKey && e.key === 'Enter') {
                runQuery();
            }
        });
    </script>
</body>
</html>
```

- [ ] **Step 5: Write CSS**

`DS3-SQL Server/internal/web/static/style.css`:

```css
:root {
    --bg: #f8f9fa;
    --surface: #ffffff;
    --border: #dee2e6;
    --text: #1a1a2e;
    --text-muted: #6b7280;
    --primary: #6366f1;
    --primary-hover: #4f46e5;
    --error: #ef4444;
    --null: #9ca3af;
}

* { box-sizing: border-box; margin: 0; padding: 0; }

body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
    line-height: 1.5;
}

.container {
    max-width: 1200px;
    margin: 0 auto;
    padding: 16px;
}

.container.center {
    display: flex;
    justify-content: center;
    align-items: center;
    min-height: 100vh;
}

.card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 32px;
    width: 100%;
    max-width: 400px;
}

.card h1 { margin-bottom: 4px; }
.subtitle { color: var(--text-muted); margin-bottom: 24px; }

.navbar {
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    padding: 12px 24px;
    display: flex;
    justify-content: space-between;
    align-items: center;
}

.brand { font-weight: 700; font-size: 1.1rem; }
.navbar nav { display: flex; gap: 16px; }
.navbar a { color: var(--text); text-decoration: none; }
.navbar a:hover { color: var(--primary); }

.form-group { margin-bottom: 16px; }
.form-group label { display: block; margin-bottom: 4px; font-weight: 500; font-size: 0.9rem; }

.input {
    width: 100%;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    font-size: 0.9rem;
}

.input.code {
    font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
    font-size: 0.85rem;
    line-height: 1.4;
}

.btn {
    padding: 8px 20px;
    border: 1px solid var(--border);
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.9rem;
    background: var(--surface);
}

.btn-primary {
    background: var(--primary);
    color: white;
    border-color: var(--primary);
}

.btn-primary:hover { background: var(--primary-hover); }

.browse-layout {
    display: flex;
    gap: 16px;
    margin-top: 16px;
}

.sidebar {
    flex: 0 0 250px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    min-height: 400px;
}

.content {
    flex: 1;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    min-height: 400px;
}

.query-layout {
    display: flex;
    gap: 16px;
    margin-top: 16px;
}

.editor-panel {
    flex: 1;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
}

.results-panel {
    flex: 2;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    overflow-x: auto;
}

.table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
}

.table th, .table td {
    padding: 6px 10px;
    border: 1px solid var(--border);
    text-align: left;
}

.table th {
    background: var(--bg);
    font-weight: 600;
    white-space: nowrap;
}

.table th small {
    font-weight: 400;
    color: var(--text-muted);
}

.null { color: var(--null); font-style: italic; }
.error { color: var(--error); }
.text-muted { color: var(--text-muted); }
h3 { margin-bottom: 12px; }
```

- [ ] **Step 6: Wire web routes into main.go**

Add the web handler routes to `cmd/ds3sql-server/main.go`:

```go
// Web UI
webHandler, err := web.NewHandler()
if err != nil {
    log.Fatalf("failed to init web handler: %v", err)
}

// Public pages
r.Get("/login", webHandler.LoginPage)
r.Get("/static/*", webHandler.Static())

// Protected pages
r.Group(func(r chi.Router) {
    r.Use(auth.Middleware(sessionStore))
    r.Get("/browse", webHandler.BrowsePage)
    r.Get("/query", webHandler.QueryPage)
    r.Get("/", func(w http.ResponseWriter, r *http.Request) {
        http.Redirect(w, r, "/browse", http.StatusFound)
    })
})
```

- [ ] **Step 7: Add HTMX bucket listing endpoint**

Add a minimal API endpoint for HTMX to fetch the bucket list as HTML fragments. In `internal/api/bucket_handler.go`:

```go
func (h *BucketHandler) ListBucketsHTML(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.s3Client.ListBuckets(r.Context())
	if err != nil {
		w.Write([]byte("<p class='error'>Error loading buckets</p>"))
		return
	}

	for _, b := range buckets {
		w.Write([]byte("<div class='bucket-item' onclick=\"loadPrefix('" + b.Name + "', '')\">📁 " + b.Name + "</div>"))
	}
}
```

Wire in main.go:

```go
r.Get("/buckets", func(w http.ResponseWriter, r *http.Request) {
    session := auth.GetSession(r)
    client, err := s3client.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
    if err != nil {
        http.Error(w, "s3 client error", http.StatusInternalServerError)
        return
    }
    api.NewBucketHandler(client).ListBucketsHTML(w, r)
})
```

- [ ] **Step 8: Build verification**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql-server/
```

Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: Web UI with login, browse, and query pages"
```
