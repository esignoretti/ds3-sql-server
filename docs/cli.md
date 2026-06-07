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

### tables

Manage datasets and tables within the catalog.

```bash
ds3sql tables list <dataset>
ds3sql tables describe <dataset.table>
```

Sub-commands:

| Command | Description |
|---------|-------------|
| `list <dataset>` | List all tables in a dataset |
| `describe <dataset.table>` | Show table schema, stats, and storage class |
| `create-as <dataset.table>` | Execute a CTAS (see below) |
| `register <dataset>` | Register an external table (see below) |

#### tables create-as

Execute a `CREATE TABLE … AS SELECT` statement and register the result as a managed table.

```bash
ds3sql tables create-as sales.daily \
  --as "SELECT dt, region, sum(n) AS total FROM sales.raw GROUP BY 1, 2" \
  --partition-by dt \
  --storage-class ssd
```

Flags:

| Flag | Description |
|------|-------------|
| `-a`, `--as` | The inner `SELECT` query (required) |
| `--partition-by` | Comma-separated partition columns |
| `--storage-class` | Storage tier: `ssd` (default) or `hdd` |

#### tables register

Register an existing S3 location as an external table.

```bash
ds3sql tables register sales \
  --name orders \
  --location "s3://my-bucket/orders/*.parquet" \
  --format parquet \
  --storage-class hdd
```

Flags:

| Flag | Description |
|------|-------------|
| `--name` | Table name |
| `--location` | S3 path or glob |
| `--format` | File format: `parquet`, `csv`, `tsv`, `json` (default `parquet`) |
| `--storage-class` | Storage tier hint: `ssd` or `hdd` (default `hdd`) |
| `--partition-columns` | Optional partition column names |

### load

Ingest data from a source into a managed table in a single batch.

```bash
ds3sql load \
  --source "s3://incoming/events/*.csv" \
  --into sales.events \
  --format csv \
  --mode append
```

Flags:

| Flag | Description |
|------|-------------|
| `--source` | S3 glob path of source files (required) |
| `--into` | Target managed table as `dataset.table` (required) |
| `--format` | Source format: `csv` (default), `tsv`, `json`, `parquet` |
| `--partition-by` | Comma-separated output partition columns |
| `--mode` | `append` (default) or `overwrite` |

### schedules

Manage cron-driven schedules.

```bash
ds3sql schedules create \
  --cron "0 * * * *" \
  --sql "CREATE TABLE sales.hourly AS SELECT * FROM sales.events" \
  --into sales.hourly

ds3sql schedules ls

ds3sql schedules rm <id>
```

Sub-commands:

| Command | Description |
|---------|-------------|
| `create` | Create a new schedule (flags below) |
| `ls` | List all schedules for the current project |
| `rm <id>` | Delete a schedule by ID |

Flags for `schedules create`:

| Flag | Description |
|------|-------------|
| `--cron` | Standard 5-field cron expression (required) |
| `--sql` | SQL to execute on each tick (required) |
| `--into` | Optional target `dataset.table` for CTAS |

### help

Display help for any command.

```bash
ds3sql help
ds3sql help query
ds3sql help tables
ds3sql help load
ds3sql help schedules
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
