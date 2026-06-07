# DS3 SQL Server

A lightweight, stateless sidecar service that enables SQL querying of data stored in [Cubbit DS3](https://cubbit.io) S3-compatible object storage. Inspired by Amazon Athena — `SELECT`-only queries against Parquet, CSV, JSON, and TSV files directly from your DS3 buckets.

## Features

- **Query S3 data with SQL** — DuckDB-powered engine runs `SELECT` queries against Parquet, CSV, JSON, and TSV files
- **CREATE TABLE … AS SELECT** — CTAS write path creates managed Parquet tables from query results, with partition and storage-class support
- **Batch load** — Ingest CSV, TSV, JSON, and Parquet sources into managed tables with append or overwrite mode
- **Cron-driven schedules** — Recurring CTAS and query execution via standard cron expressions
- **Multiple interfaces** — REST API, CLI (`ds3sql`), and Web UI (HTMX, no build step)
- **Stateless** — no persistent database, no caching, no background jobs; per-query DuckDB instances are created on demand and discarded after each request
- **Sidecar deployment** — sits alongside the DS3 Gateway, listens on localhost by default
- **Cubbit IAM authentication** — challenge-response protocol with Ed25519 signatures; per-project S3 credentials are automatically provisioned via Cubbit Keyvault
- **Schema inference** — discover column names and types of any S3 path before querying
- **Bucket browsing** — list buckets and objects with prefix/delimiter navigation
- **Paginated results** — both CLI and Web UI support configurable page sizes with next/prev navigation
- **Connection pooling** — warm DuckDB pool eliminates per-query setup overhead (configurable pool size)
- **Storage-class tiering** — route managed table data to SSD or HDD DS3 buckets
- **Managed & external tables** — register external S3 locations or let the write path manage your data lifecycle

## Quick Start

### Server

```bash
# Build and run with default config
make run

# Or build manually
make build-server
./ds3sql-server
```

### CLI

```bash
# Build the CLI
make build-cli

# Authenticate
./ds3sql login

# List buckets
./ds3sql buckets

# Browse objects
./ds3sql ls my-bucket

# Run a query
./ds3sql query "SELECT count(*) FROM read_csv_auto('s3://my-bucket/data.csv')"

# --- Write path (Phase 3) ---

# CTAS: create a managed Parquet table from a SELECT
ds3sql tables create-as sales.daily \
  --as "SELECT dt, region, sum(n) AS total FROM sales.raw GROUP BY 1, 2" \
  --partition-by dt \
  --storage-class ssd

# Batch load: ingest CSV files into a managed table
ds3sql load \
  --source 's3://incoming/events/*.csv' \
  --into sales.events \
  --format csv \
  --mode append

# Schedule: run a CTAS every hour
ds3sql schedules create \
  --cron "0 * * * *" \
  --sql "CREATE TABLE sales.hourly AS SELECT * FROM sales.events" \
  --into sales.hourly

# List schedules
ds3sql schedules ls
```

### Docker

```bash
docker build -t ds3sql-server .
docker run -p 8080:8080 ds3sql-server
```

## Documentation

- [API Reference](docs/api.md) — all REST endpoints with request/response schemas
- [CLI Reference](docs/cli.md) — command reference for `ds3sql`
- [Configuration](docs/configuration.md) — server config file and environment variables
- [Architecture](docs/architecture.md) — system design and component overview
- [Deployment](docs/deployment.md) — Kubernetes sidecar deployment guide
- [Examples](docs/examples.md) — example queries and usage patterns

## Configuration

Default config file location: `~/.ds3sql/server.yaml`

```yaml
listen_addr: ":8080"
iam_url: "https://api.eu00wi.cubbit.services"
ds3_gateway_url: "http://localhost:9000"

auth:
  token_expiry: 24h
  refresh_token_expiry: 720h

query:
  max_rows: 10000
  max_execution_seconds: 60
  max_result_bytes: 104857600
  pool_size: 4
  threads: 0
  memory_limit: "2GB"

rate_limit:
  queries_per_minute: 10
```

Environment variable overrides: `DS3SQL_LISTEN_ADDR`, `DS3SQL_IAM_URL`, `DS3SQL_DS3_GATEWAY_URL`, `DS3SQL_POOL_SIZE`, `DS3SQL_THREADS`, `DS3SQL_MEMORY_LIMIT`.

## Tech Stack

| Component | Choice |
|-----------|--------|
| Language | Go 1.26 |
| SQL Engine | DuckDB (in-process OLAP) |
| HTTP Router | chi |
| S3 SDK | AWS SDK v2 |
| CLI Framework | Cobra |
| Frontend | Go html/template + HTMX |
| Auth | Cubbit IAM challenge-response (Ed25519) |

## License

MIT — see [LICENSE](LICENSE) for details.
