# API Reference

All API endpoints are served from the DS3 SQL Server HTTP server. Authenticated endpoints require either a `Bearer` token (Authorization header) or a session cookie.

## Authentication

### POST /auth/login

Authenticate with Cubbit IAM credentials.

**Request:**
```json
{
  "email": "user@example.com",
  "password": "your-password",
  "totp_code": "123456",
  "tenant_id": "tenant-uuid"
}
```

`totp_code` and `tenant_id` are optional.

**Response (200):**
```json
{
  "token": "eyJhbGciOiJFUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJFUzI1NiIs...",
  "expires_at": "2026-05-23T12:00:00Z",
  "user": {
    "email": "user@example.com",
    "id": "user-uuid"
  }
}
```

### POST /auth/refresh

Exchange a refresh token for a new JWT.

**Request:**
```json
{
  "refresh_token": "eyJhbGciOiJFUzI1NiIs..."
}
```

**Response (200):**
```json
{
  "token": "eyJhbGciOiJFUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJFUzI1NiIs...",
  "expires_at": "2026-05-23T12:00:00Z"
}
```

### GET /auth/logout

Clear the server-side session and cookie. Returns a redirect to `/login`.

### GET /auth/me

Return the currently authenticated user and their project credentials.

**Response (200):**
```json
{
  "email": "user@example.com",
  "projects": [
    {
      "name": "my-project",
      "url": "http://localhost:9000",
      "bucket": "my-bucket",
      "access_key": "AKIA...",
      "secret_key": "..."
    }
  ]
}
```

## Buckets

### GET /buckets

List all S3 buckets accessible to the authenticated user.

**Response (200):**
```json
{
  "buckets": [
    {"name": "my-bucket", "creation_date": "2026-01-15T10:00:00Z"},
    {"name": "data-lake", "creation_date": "2026-03-01T08:30:00Z"}
  ]
}
```

### GET /buckets/{bucket}

List objects and prefixes in a bucket.

**Query parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `prefix` | string | `""` | Filter objects by key prefix |
| `delimiter` | string | `/` | Delimiter for hierarchical listing |
| `max_keys` | int | `100` | Maximum number of keys to return |

**Response (200):**
```json
{
  "prefixes": ["logs/", "exports/", "backups/"],
  "objects": [
    {
      "key": "logs/2026-05-01.parquet",
      "size": 2457600,
      "last_modified": "2026-05-01T12:00:00Z"
    }
  ],
  "is_truncated": false
}
```

## Query

### POST /query

Execute a SQL query against S3 data using DuckDB.

**Request:**
```json
{
  "sql": "SELECT level, count(*) as cnt FROM read_parquet('s3://my-bucket/logs/*.parquet') GROUP BY level ORDER BY cnt DESC LIMIT 10",
  "bucket": "my-bucket"
}
```

The `bucket` field scopes IAM credentials to the target bucket. The SQL query references the full S3 path.

**Response (200):**
```json
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
```

**Error response (400/500):**
```json
{
  "error": "HTTP error listing s3://my-bucket/logs/: 403 Forbidden",
  "elapsed_ms": 120
}
```

## Schema

### POST /schema

Infer the schema (column names, types, nullability) of an S3 path.

**Request:**
```json
{
  "path": "s3://my-bucket/logs/*.parquet"
}
```

**Response (200):**
```json
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

## Jobs & Write Path

### POST /jobs

Submit a SQL query or write job. The endpoint distinguishes three modes:

**Plain query** (`SELECT` only) — synchronous fast path. Returns `200` with results.
```json
{
  "sql": "SELECT count(*) FROM read_parquet('s3://my-bucket/logs/*.parquet')"
}
```

**CTAS** (CREATE TABLE … AS SELECT) — async write path. Returns `202` + a queued job.
```json
{
  "sql": "CREATE TABLE sales.daily PARTITION BY (dt) STORAGE 'ssd' AS SELECT dt, region, sum(n) AS total FROM sales.raw GROUP BY 1, 2"
}
```

Supported CTAS grammar:
```
CREATE TABLE [IF NOT EXISTS] <dataset>.<table>
  [PARTITION BY (col1, col2, ...)]
  [STORAGE 'ssd' | 'hdd']
  AS SELECT ...
```

**Batch load** — async write path. Returns `202` + a queued job.
```json
{
  "type": "load",
  "source": "s3://incoming/events/*.csv",
  "into": "sales.events",
  "format": "csv",
  "partition_by": ["dt"],
  "mode": "append"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | — | `"load"` to select load mode; omit for CTAS/query |
| `source` | string | — | S3 glob path of source files |
| `into` | string | — | Target `dataset.table` name |
| `format` | string | `"csv"` | Source format: `csv`, `tsv`, `json`, `parquet` |
| `partition_by` | string[] | `[]` | Partition columns for the output |
| `mode` | string | `"append"` | `"append"` or `"overwrite"` |

**Response (200 — plain query):**
```json
{
  "id": "job-uuid",
  "type": "query",
  "status": "done",
  "columns": ["level", "cnt"],
  "types": ["VARCHAR", "BIGINT"],
  "rows": [["ERROR", 1523]],
  "row_count": 1,
  "elapsed_ms": 342
}
```

**Response (202 — CTAS / load queued):**
```json
{
  "id": "job-uuid",
  "type": "ctas",
  "status": "queued",
  "project_id": "project-uuid",
  "created_at": "2026-06-07T12:00:00Z"
}
```

When the job reaches `"done"` status, a CTAS job will include `"into_table": "dataset.table"` in its response.

### GET /jobs

List recent jobs for the authenticated project.

**Response (200):**
```json
{
  "jobs": [
    {
      "id": "job-uuid",
      "type": "ctas",
      "status": "done",
      "into_table": "sales.daily",
      "created_at": "2026-06-07T12:00:00Z"
    }
  ]
}
```

### GET /jobs/{id}

Poll a specific job by ID.

**Response (200):**
```json
{
  "id": "job-uuid",
  "type": "ctas",
  "status": "done",
  "into_table": "sales.daily",
  "project_id": "project-uuid",
  "created_at": "2026-06-07T12:00:00Z"
}
```

Returns `404` if the job does not exist.

### DELETE /jobs/{id}

Cancel a queued or running job.

**Response (202):** empty body.

Returns `404` if the job is not found or already finished.

## Schedules

### POST /schedules

Create a cron-driven schedule that executes a SQL statement on a recurring basis.

**Request:**
```json
{
  "cron": "0 * * * *",
  "sql": "CREATE TABLE sales.hourly AS SELECT * FROM sales.events",
  "into_table": "sales.hourly"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `cron` | string | Standard 5-field cron expression (minute, hour, day-of-month, month, day-of-week) |
| `sql` | string | SQL statement to execute on each tick |
| `into_table` | string | Optional target table name for CTAS (included in job metadata) |

**Response (201):**
```json
{
  "id": "schedule-uuid",
  "project_id": "project-uuid",
  "cron": "0 * * * *",
  "sql": "CREATE TABLE sales.hourly AS SELECT * FROM sales.events",
  "into_table": "sales.hourly",
  "next_run_at": "2026-06-07T13:00:00Z",
  "running": false,
  "created_at": "2026-06-07T12:00:00Z"
}
```

**Misfire policy:** If a schedule is still running when its next tick fires (`running: true`), the tick is skipped. No overlapping runs are allowed.

### GET /schedules

List all schedules for the authenticated project.

**Response (200):**
```json
{
  "schedules": [
    {
      "id": "schedule-uuid",
      "cron": "0 * * * *",
      "sql": "CREATE TABLE sales.hourly AS SELECT * FROM sales.events",
      "into_table": "sales.hourly",
      "next_run_at": "2026-06-07T13:00:00Z",
      "running": false
    }
  ]
}
```

### DELETE /schedules/{id}

Delete a schedule by ID.

**Response (204):** empty body (no content).

Returns `404` if the schedule does not exist.

## Datasets & Tables

### POST /datasets

Create a new dataset (logical schema).

**Request:**
```json
{
  "name": "sales"
}
```

**Response (201):**
```json
{
  "project_id": "project-uuid",
  "name": "sales",
  "created_at": "2026-06-07T12:00:00Z"
}
```

### GET /datasets

List all datasets for the authenticated project.

### POST /datasets/{dataset}/tables

Register an external table pointing to existing S3 data.

**Request:**
```json
{
  "name": "orders",
  "location": "s3://my-bucket/orders/*.parquet",
  "format": "parquet",
  "storage_class": "hdd",
  "partition_columns": []
}
```

**Response (201):** the registered table with inferred schema and row count.

### GET /datasets/{dataset}/tables

List all tables in a dataset.

### GET /datasets/{dataset}/tables/{table}

Describe a table (schema, stats, storage class).

### DELETE /datasets/{dataset}/tables/{table}

Drop a table registration.

- For **managed** tables (created by CTAS or load): the underlying Parquet data files are also deleted.
- For **external** tables: only the registration is removed; the source data is preserved.

**Response (204):** empty body.

## Health

### GET /health

Liveness probe. No authentication required.

**Response (200):**
```json
{
  "status": "ok"
}
```
