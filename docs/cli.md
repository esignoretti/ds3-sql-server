# CLI Reference

The `ds3sql` CLI provides terminal-based access to the DS3 SQL Server. It communicates with the server via REST API.

## Global Flags

| Flag | Description |
|------|-------------|
| `--host` | Server host (default `localhost`) |
| `--port` | Server port (default `8080`) |
| `--json` | Output raw JSON instead of formatted tables |

## Commands

### login

Authenticate with Cubbit IAM credentials. Prompts for email and password interactively.

```bash
ds3sql login
```

Stores the session token in `~/.ds3sql/config`.

### logout

Clear stored credentials.

```bash
ds3sql logout
```

### status

Show current session status, server health, and authenticated user.

```bash
ds3sql status
```

### buckets

List all accessible S3 buckets.

```bash
ds3sql buckets
```

Optional flags:
| Flag | Description |
|------|-------------|
| `--project` | Filter by project name |

### ls

List objects and prefixes in a bucket.

```bash
ds3sql ls my-bucket
ds3sql ls my-bucket --prefix logs/2026/
```

Optional flags:
| Flag | Description |
|------|-------------|
| `--prefix` | Object key prefix |
| `--project` | Filter by project name |

### schema

Infer and display the schema (columns, types, nullability) of an S3 path.

```bash
ds3sql schema s3://my-bucket/logs/*.parquet
```

Optional flags:
| Flag | Description |
|------|-------------|
| `--project` | Filter by project name |

### query

Execute a SQL query and display results as a formatted table.

```bash
ds3sql query "SELECT count(*) FROM read_csv_auto('s3://my-bucket/data.csv')"
ds3sql query -f query.sql
```

Optional flags:
| Flag | Description |
|------|-------------|
| `-f`, `--file` | Read SQL from a file |
| `--json` | Output results as JSON |
| `--project` | Filter by project name |

### help

Display help for any command.

```bash
ds3sql help
ds3sql help query
```

## Configuration

CLI configuration is stored at `~/.ds3sql/config` as JSON:

```json
{
  "host": "localhost",
  "port": "8080",
  "token": "eyJhbGciOiJFUzI1NiIs..."
}
```

This file is created by `ds3sql login` and deleted by `ds3sql logout`.
