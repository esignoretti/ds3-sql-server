# DS3 SQL Server — Design Document

**Date**: 2026-05-22
**Status**: Draft

## 1. Overview

DS3 SQL Server is a lightweight, sidecar container that enables SQL querying of Cubbit DS3 buckets. It runs alongside the DS3 Gateway in production, authenticates users via Cubbit IAM, and uses DuckDB to query Parquet and CSV files directly from S3. It provides a simple Web UI for browsing and querying, a REST API for programmatic access, and a CLI for terminal-based workflows.

Inspired by Amazon Athena but scoped to a lean MVP subset — `SELECT`-only queries against data already stored in DS3 buckets.

### Design Tenets

1. **Lean and simple** — single Go binary, embedded DuckDB, no external dependencies
2. **Athena-like** — query data in-place, no loading/importing step
3. **Stateless** — no persistent database, no background jobs, no cache (for MVP)
4. **CLI-first** — API and CLI are primary interfaces; Web UI is a convenience
5. **IAM-native** — users authenticate with Cubbit credentials, no separate user store

## 2. Tech Stack

| Component | Choice | Rationale |
|---|---|---|
| Language | Go 1.22+ |    |
| SQL Engine | DuckDB (via CGo) | Battle-tested, native S3 reader, Parquet/CSV support |
| DuckDB bindings | `github.com/marcboeker/go-duckdb` | `database/sql` compatible |
| HTTP Router | `chi` | Lightweight, idiomatic Go |
| Frontend | `html/template` + HTMX | No build step, server-rendered |
| Auth | Cubbit IAM (Ed25519 challenge-response) | Reuse patterns from S3lytics |
| S3 SDK | `aws-sdk-go-v2` | Official SDK with custom DS3 endpoint |
| CLI | Cobra | Standard Go CLI framework |
| Config | YAML + env vars | Endpoint, IAM URL, listen address |
| Container | Distroless Go + DuckDB shared lib | Multi-stage build, ~15MB |

## 3. Architecture

```
┌──────────────────────────────────────────────────────┐
│  DS3 SQL Server (single Go binary)                   │
│                                                      │
│  ┌────────────────────────────────────────────────┐  │
│  │  HTTP Server (chi)                              │  │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────────┐  │  │
│  │  │ Static   │ │ Templates│ │ API Handlers   │  │  │
│  │  │ (/static)│ │ (/views) │ │ /auth, /buckets│  │  │
│  │  │          │ │          │ │ /query, /schema│  │  │
│  │  └──────────┘ └──────────┘ └───────┬────────┘  │  │
│  └─────────────────────────────────────┼──────────┘  │
│                                        │             │
│  ┌─────────────────────────────────────▼──────────┐  │
│  │  Services Layer                               │  │
│  │  ┌──────────┐ ┌──────────┐ ┌────────────────┐  │  │
│  │  │ Auth Svc │ │ DuckDB   │ │ S3 Discovery   │  │  │
│  │  │ (Cubbit  │ │ Engine   │ │ (list buckets  │  │  │
│  │  │  IAM Go) │ │ (CGo)    │ │  & prefixes)   │  │  │
│  │  └──────────┘ └────┬─────┘ └────────────────┘  │  │
│  └─────────────────────┼─────────────────────────┘  │
│                        │                            │
│  ┌─────────────────────▼─────────────────────────┐  │
│  │  DuckDB (in-process)                          │  │
│  │  ├─ httpfs extension                          │  │
│  │  ├─ parquet extension                         │  │
│  │  └─ In-memory, ephemeral per query            │  │
│  └───────────────────────────────────────────────┘  │
│                        │                            │
│  ┌─────────────────────▼─────────────────────────┐  │
│  │  S3 Client (aws-sdk-go-v2)                    │  │
│  │  ├─ ListBuckets / ListObjectsV2               │  │
│  │  └─ HeadObject (schema inference)             │  │
│  └───────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────┘
```

### Component Responsibilities

- **Auth Service**: Implements Cubbit IAM challenge-response protocol (Ed25519 signing, JWT management, token refresh). Middleware validates JWTs on protected routes.
- **DuckDB Engine**: Creates in-memory DuckDB instances per query. Configures `httpfs` S3 credentials pointing to the DS3 Gateway. Executes SQL and returns structured results.
- **S3 Discovery**: Uses `aws-sdk-go-v2` with custom DS3 Gateway endpoint to list buckets and prefix contents. Powers the browse UI and object enumeration.
- **Web UI**: Login page → bucket browser → query editor → results table. HTMX for dynamic interactions.
- **REST API**: JSON endpoints consumed by both the CLI and Web UI.

### Deployment Model

- Runs as a **sidecar container** alongside the DS3 Gateway in the same Kubernetes pod
- Communicates with DS3 Gateway over `localhost` (same network namespace)
- Exposes port 8080 internally (not exposed to the internet)
- Optional: exposed via Ingress for remote CLI access (with TLS termination)

## 4. Authentication (Cubbit IAM)

The auth flow reuses the Cubbit IAM protocol:

```
1. POST /auth/login
   Body: {email, password}
     ├─ POST {iam_url}/challenge → {salt, challenge}
     ├─ key = SHA256(password + salt) → Ed25519 seed
     ├─ signature = Ed25519.Sign(challenge)
     └─ POST {iam_url}/signin → JWT + refresh token
   Response: {token, refresh_token, expires_at}

2. POST /auth/refresh
   Body: {refresh_token}
   Response: {token, refresh_token, expires_at}

3. GET /auth/me
   Header: Authorization: Bearer {token}
   Response: Account info (access key, secret key, gateway endpoint)
```

The `/auth/me` response includes the S3 credentials (access key, secret key, DS3 Gateway endpoint) that the DuckDB engine uses for S3 access. These are cached in-memory for the session lifetime.

## 5. REST API

### Endpoints

```
POST   /auth/login        Email + password → JWT token
POST   /auth/refresh      Refresh token → new JWT
GET    /auth/me           Current session info + S3 credentials

GET    /buckets           List accessible S3 buckets
GET    /buckets/{bucket}?prefix=...  List objects at prefix

POST   /query             Execute SQL, return JSON results (bucket field scopes IAM credentials to the target bucket)
POST   /schema            Return columns/types for a S3 path

GET    /health            Liveness probe
```

### GET /buckets

```json
// Response (200)
{
  "buckets": [
    {"name": "my-bucket", "creation_date": "2026-01-15T10:00:00Z"},
    {"name": "data-lake", "creation_date": "2026-03-01T08:30:00Z"}
  ]
}
```

### GET /buckets/{bucket}

Query params: `prefix` (optional), `delimiter` (default `/`), `max_keys` (default 100)

```json
// Response (200)
{
  "prefixes": ["logs/", "exports/", "backups/"],
  "objects": [
    {"key": "logs/2026-05-01.parquet", "size": 2457600, "last_modified": "2026-05-01T12:00:00Z"},
    {"key": "logs/2026-05-02.parquet", "size": 3102720, "last_modified": "2026-05-02T12:00:00Z"}
  ],
  "is_truncated": false
}
```

### POST /query

The `bucket` field scopes the IAM session to a specific bucket, ensuring the DuckDB httpfs extension has the correct credentials for S3 access. The SQL itself references the full S3 path.

```json
// Request
{
  "sql": "SELECT level, count(*) as cnt FROM read_parquet('s3://my-bucket/logs/*.parquet') GROUP BY level ORDER BY cnt DESC LIMIT 10",
  "bucket": "my-bucket"
}

// Response (200)
{
  "columns": ["level", "cnt"],
  "types": ["VARCHAR", "BIGINT"],
  "rows": [
    ["ERROR", 1523],
    ["WARN", 892],
    ["INFO", 445]
  ],
  "row_count": 3,
  "elapsed_ms": 342
}

// Error response (400/500)
{
  "error": "HTTP error listing s3://my-bucket/logs/: 403 Forbidden",
  "elapsed_ms": 120
}
```

### POST /schema

```json
// Request
{
  "path": "s3://my-bucket/logs/*.parquet"
}

// Response (200)
{
  "columns": [
    {"name": "id", "type": "BIGINT", "nullable": true},
    {"name": "level", "type": "VARCHAR", "nullable": false},
    {"name": "message", "type": "VARCHAR", "nullable": false},
    {"name": "timestamp", "type": "TIMESTAMP", "nullable": false}
  ],
  "elapsed_ms": 210
}
```

## 6. Query Execution Flow

```
1. User submits SQL via CLI, Web UI, or direct API call
2. Server validates JWT, extracts S3 credentials from session
3. Server creates in-memory DuckDB instance
4. DuckDB loads httpfs + parquet extensions
5. Server configures httpfs with DS3 Gateway endpoint + credentials
6. DuckDB executes SQL (e.g. SELECT * FROM read_parquet('s3://...'))
7. Server collects column names, types, and all rows
8. In-memory DuckDB is discarded (ephemeral per-query)
9. Server returns JSON response
```

Key behaviors:
- **Stateless per query**: No DuckDB state persists between queries
- **No intermediate storage**: Files are streamed by DuckDB through httpfs, no local temp files
- **Configurable limits**: `max_rows` (default 10,000), `max_execution_seconds` (default 60)
- **Error handling**: DuckDB errors (syntax, schema mismatch, IAM permissions) returned as structured errors

## 7. CLI

```
ds3sql login                                # Interactive IAM login
ds3sql logout                               # Clear stored credentials
ds3sql status                               # Show current user and server health

ds3sql buckets                              # List buckets (GET /buckets)
ds3sql ls s3://bucket/prefix                # List objects (GET /buckets/{bucket}?prefix=...)
ds3sql schema s3://bucket/prefix/*.parquet  # Show columns/types (POST /schema)
ds3sql query "SELECT ..."                   # Run SQL, print table (POST /query)
```

CLI behavior:
- Reads config from `~/.ds3sql/config` (host, port, token)
- If server is not running, local mode falls back? — No, MVP requires server
- Output: formatted table for `query`, columns for `schema`, lists for `buckets`/`ls`
- JSON output flag (`--json`) for piping

## 8. Web UI

### Pages

1. **Login page** — Email + password form → IAM auth → redirect to browse
2. **Browse page** — Bucket tree sidebar (expandable prefixes), main area shows file list with sizes/dates
3. **Query page** — SQL editor textarea, Run button, results table below
4. **Result page** — Shows query results as an HTML table with column headers, row count, and duration

### UX Flow

```
Login → Browse (bucket tree on left)
         ├─ Click prefix → file list on right
         ├─ Click "Query this prefix" → editor pre-filled with SELECT * FROM read_parquet('s3://...')
         └─ Run query → results table renders below
```

### Frontend Stack

- `html/template` for server-rendered pages
- HTMX for dynamic updates (file listing, query results)
- No build step, no JS framework

## 9. Security

- **IAM auth only**: No shared secrets, no API keys stored server-side
- **Token scope**: JWT carries session-bound S3 credentials, refreshed automatically
- **Query sandbox**: DuckDB runs in-process with no filesystem write access
- **Sidecar isolation**: Listens on localhost only in production; no external network exposure
- **CORS**: Disabled or locked to trusted origins
- **No data persistence**: Server stores no data between restarts
- **Rate limiting**: Per-user query rate limits (configurable)

## 10. Container & Deployment

### Dockerfile (multi-stage)

```
Stage 1: golang:1.22-bookworm
  - Build Go binary with CGO_ENABLED=1
  - Link against libduckdb (from DuckDB CGo bindings)

Stage 2: gcr.io/distroless/base
  - Copy Go binary + libduckdb.so
  - USER nobody
  - ENTRYPOINT ["/ds3sql-server"]
```

### Kubernetes Sidecar

```yaml
containers:
  - name: ds3-gateway
    image: cubbit/ds3-gateway:latest
    ...
  - name: ds3-sql-server
    image: cubbit/ds3-sql-server:latest
    ports:
      - containerPort: 8080
    env:
      - name: LISTEN_ADDR
        value: ":8080"
      - name: IAM_URL
        value: "https://iam.cubbit.eu"
      - name: DS3_GATEWAY_URL
        value: "http://localhost:9000"
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
      limits:
        memory: "512Mi"
        cpu: "500m"
```

### Server Configuration

Config file resolved from `--config` flag, `DS3SQL_CONFIG` env var, or default `~/.ds3sql/server.yaml`. All values also configurable via env vars (`DS3SQL_*`).

```yaml

```yaml
# ds3sql-server.yaml
listen_addr: ":8080"
iam_url: "https://iam.cubbit.eu"
ds3_gateway_url: "http://localhost:9000"

auth:
  token_expiry: 24h
  refresh_token_expiry: 720h

query:
  max_rows: 10000
  max_execution_seconds: 60
  max_result_bytes: 104857600  # 100MB

rate_limit:
  queries_per_minute: 10
```

## 11. Non-Goals (Deferred)

| Feature | Reason |
|---|---|
| Result caching | Stateless is simpler for MVP |
| INSERT / UPDATE / DELETE | Read-only engine first |
| DDL (CREATE TABLE, etc.) | Query S3 data in-place |
| Multi-tenancy | Single-user session |
| Query history / saved queries | Out of Browse-only scope |
| SQL auto-complete | Nice-to-have, not MVP |
| Avro / ORC formats | Parquet + CSV covers 90% |
| Streaming large results | In-memory for MVP, paginate later |
| WebSocket / live queries | REST request-response is sufficient |

## 12. Project Structure

```
ds3-sql-server/
├── cmd/
│   ├── ds3sql-server/          # Server binary
│   │   └── main.go
│   └── ds3sql/                 # CLI binary
│       └── main.go
├── internal/
│   ├── auth/                   # Cubbit IAM client
│   │   ├── auth.go
│   │   ├── challenge.go
│   │   └── middleware.go
│   ├── query/                  # DuckDB engine
│   │   ├── engine.go
│   │   └── schema.go
│   ├── s3/                     # S3 discovery client
│   │   ├── client.go
│   │   └── listing.go
│   ├── api/                    # HTTP handlers
│   │   ├── auth_handler.go
│   │   ├── query_handler.go
│   │   ├── bucket_handler.go
│   │   ├── schema_handler.go
│   │   └── router.go
│   ├── web/                    # Web UI
│   │   ├── templates/
│   │   │   ├── login.html
│   │   │   ├── browse.html
│   │   │   └── query.html
│   │   └── static/
│   │       └── style.css
│   └── config/                 # Configuration
│       └── config.go
├── Dockerfile
├── Makefile
├── go.mod
├── go.sum
└── .gitignore
```
