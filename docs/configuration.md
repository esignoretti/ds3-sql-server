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
