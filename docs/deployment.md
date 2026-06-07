# Deployment

## Building

```bash
# Build both binaries
make build

# Build only the server
make build-server

# Build only the CLI
make build-cli
```

Binaries are produced at the project root: `ds3sql-server` and `ds3sql`.

## Docker

```bash
# Build the container
docker build -t ds3sql-server .

# Run locally
docker run -p 8080:8080 \
  -e DS3SQL_IAM_URL=https://api.eu00wi.cubbit.services \
  -e DS3SQL_DS3_GATEWAY_URL=http://host.docker.internal:9000 \
  ds3sql-server
```

The Docker image uses a multi-stage build with `gcr.io/distroless/base-debian12` for a minimal footprint (~15MB). The server runs as `nobody`.

## Kubernetes Sidecar

DS3 SQL Server is designed to run as a sidecar container alongside the DS3 Gateway in the same pod:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ds3-gateway
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ds3-gateway
  template:
    metadata:
      labels:
        app: ds3-gateway
    spec:
      containers:
        - name: ds3-gateway
          image: cubbit/ds3-gateway:latest
          ports:
            - containerPort: 9000
        - name: ds3-sql-server
          image: cubbit/ds3-sql-server:latest
          ports:
            - containerPort: 8080
          env:
            - name: DS3SQL_LISTEN_ADDR
              value: ":8080"
            - name: DS3SQL_IAM_URL
              value: "https://api.eu00wi.cubbit.services"
            - name: DS3SQL_DS3_GATEWAY_URL
              value: "http://localhost:9000"
          resources:
            requests:
              memory: "128Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
```

## High-Availability Coordinator (Postgres)

For production deployments requiring coordinator-level fault tolerance, multiple coordinator processes can share a single Postgres metastore:

```bash
# Coordinator 1
DS3SQL_METASTORE_DRIVER=postgres \
DS3SQL_METASTORE_DSN="postgres://ds3:secret@db.internal:5432/ds3sql?sslmode=require" \
ds3sql-server --role=coordinator

# Coordinator 2 (same Postgres database)
DS3SQL_METASTORE_DRIVER=postgres \
DS3SQL_METASTORE_DSN="postgres://ds3:secret@db.internal:5432/ds3sql?sslmode=require" \
ds3sql-server --role=coordinator
```

Each coordinator runs the cron-driven scheduler and API independently, sharing the same datasets, tables, jobs, cache index, and schedules via the Postgres backend. The SQLite driver remains the default for single-process development and small deployments — no external database required.

When running with a Postgres metastore, both coordinators must use the same `DS3SQL_CLUSTER_SHARED_SECRET` if worker nodes are registered.

**CI / Testing:** The conformance suite runs against both backends. Set `DS3SQL_TEST_POSTGRES_DSN` to a Postgres URI to also execute the Postgres-backed tests:

```bash
DS3SQL_TEST_POSTGRES_DSN="postgres://test:test@localhost:5432/ds3sql_test?sslmode=disable" \
  go test ./internal/metastore/...
```

## Configuration

See the [Configuration Reference](configuration.md) for all available settings.

## Security

### Worker data-plane authentication

The coordinator→worker control channel is authenticated with a shared secret. This is **required** for `role=coordinator` and `role=worker` — the server will refuse to start without `cluster.shared_secret` (or `DS3SQL_CLUSTER_SHARED_SECRET`).

The secret comparison uses `crypto/subtle.ConstantTimeCompare` to prevent timing side-channels.

### S3 credential safety

All S3 access keys, secret keys, and endpoints passed to the DuckDB engine are single-quote-escaped (`'` → `''`) before being embedded in SQL statements, preventing SQL injection through credential values.

### Multi-tenant isolation

- **Job/schedule/report access**: All jobs, schedules, and reports are scoped to the caller's project. API handlers verify ownership before returning or mutating data.
- **Managed data locations**: Managed table storage paths include the project ID (`_managed/<projectID>/<dataset>/<table>/`), preventing cross-project data collisions at the object-storage level.
- **Session-derived context**: Project context is derived from the authenticated session, not from client-supplied request bodies.

### TLS / mTLS

The worker data-plane runs on the same HTTP listener as the public API. For production deployments, terminate TLS at a reverse proxy (e.g. Envoy, ingress-nginx) or bind the listener to a private network interface. Full mTLS between coordinator and worker is a future enhancement.

## Resource Requirements

| Resource | Request | Limit |
|----------|---------|-------|
| CPU | 100m | 500m |
| Memory | 128Mi | 512Mi |

Actual usage depends on query volume and complexity. Parquet queries with large files may require higher limits.
