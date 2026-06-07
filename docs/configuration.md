# Configuration

## Server Configuration File

The server reads configuration from `~/.ds3sql/server.yaml` by default. Override with `--config` flag or `DS3SQL_CONFIG` environment variable.

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

## Environment Variables

All config fields can be overridden via environment variables:

| Variable | Overrides | Default |
|----------|-----------|---------|
| `DS3SQL_LISTEN_ADDR` | `listen_addr` | `:8080` |
| `DS3SQL_IAM_URL` | `iam_url` | `https://api.eu00wi.cubbit.services` |
| `DS3SQL_DS3_GATEWAY_URL` | `ds3_gateway_url` | `http://localhost:9000` |
| `DS3SQL_POOL_SIZE` | `query.pool_size` | `4` |
| `DS3SQL_THREADS` | `query.threads` | `0` (auto) |
| `DS3SQL_MEMORY_LIMIT` | `query.memory_limit` | `2GB` |

## Storage Configuration

The write path (Phase 3) uses storage classes to map logical tiers to physical DS3 buckets. Configured under the `storage` key:

```yaml
storage:
  classes:
    ssd:
      bucket: ds3-fast
      endpoint: ""
    hdd:
      bucket: ds3-cold
      endpoint: ""
```

Each storage class has:

| Field | Description |
|-------|-------------|
| `bucket` | The DS3 bucket where managed table data is stored |
| `endpoint` | Optional DS3 Gateway endpoint for this class; empty means use the session's gateway endpoint |

The default configuration defines two classes:

- **ssd** (`ds3-fast`): Intended for hot data, typically backed by NVMe or SSD storage on the DS3 Gateway.
- **hdd** (`ds3-cold`): Intended for warm/cold data, typically backed by HDD or cheaper object storage.

Custom classes can be added; the CTAS parser accepts any class name via `STORAGE 'classname'`.

### Environment Variable Overrides

```bash
# Override SSD bucket and endpoint
export DS3SQL_STORAGE_SSD_BUCKET=my-ssd-bucket
export DS3SQL_STORAGE_SSD_ENDPOINT=https://ssd-gateway.example.com

# Override HDD bucket and endpoint
export DS3SQL_STORAGE_HDD_BUCKET=my-hdd-bucket
export DS3SQL_STORAGE_HDD_ENDPOINT=https://hdd-gateway.example.com
```

| Variable | Overrides |
|----------|-----------|
| `DS3SQL_STORAGE_SSD_BUCKET` | `storage.classes.ssd.bucket` |
| `DS3SQL_STORAGE_SSD_ENDPOINT` | `storage.classes.ssd.endpoint` |
| `DS3SQL_STORAGE_HDD_BUCKET` | `storage.classes.hdd.bucket` |
| `DS3SQL_STORAGE_HDD_ENDPOINT` | `storage.classes.hdd.endpoint` |

## Scheduler Role

The cron-driven scheduler runs only on nodes with `role: coordinator` or `role: all`. Worker-only nodes do not run the scheduler tick loop. Configure role via:

```yaml
role: all   # default — runs scheduler, query engine, and API
```

```bash
# CLI override
ds3sql-server --role coordinator
```

Env override: `DS3SQL_ROLE=coordinator`

## CLI Configuration

The CLI stores session state in `~/.ds3sql/config` (JSON). This file is managed automatically by `ds3sql login` and `ds3sql logout`.

```json
{
  "host": "localhost",
  "port": "8080",
  "token": "eyJhbGciOiJFUzI1NiIs..."
}
```

## CLI Flags

Server flags:

| Flag | Description |
|------|-------------|
| `--config` | Path to config file |
| `--port` | Override listen port |

CLI flags:

| Flag | Description |
|------|-------------|
| `--host` | Server host (default `localhost`) |
| `--port` | Server port (default `8080`) |
| `--json` | Output JSON |
