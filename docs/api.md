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

## Health

### GET /health

Liveness probe. No authentication required.

**Response (200):**
```json
{
  "status": "ok"
}
```
