# Architecture

DS3 SQL Server is a single Go binary that provides SQL querying of S3 data via DuckDB. It runs as a sidecar alongside the Cubbit DS3 Gateway.

## System Diagram

```
┌──────────────────────────────────────────────────────┐
│  HTTP Server (chi router)                             │
│  ├── Static files (/static)                           │
│  ├── Templates (HTML pages)                           │
│  └── API Handlers (/auth, /buckets, /query, /schema)  │
├──────────────────────────────────────────────────────┤
│  Services Layer                                       │
│  ├── Auth Service (Cubbit IAM challenge-response)     │
│  ├── DuckDB Engine (CGo, in-process)                  │
│  └── S3 Discovery (bucket/prefix listing)             │
├──────────────────────────────────────────────────────┤
│  DuckDB (in-memory per query)                         │
│  ├── httpfs extension (S3 access)                     │
│  └── parquet extension                                │
├──────────────────────────────────────────────────────┤
│  S3 Client (aws-sdk-go-v2 → DS3 Gateway endpoint)     │
└──────────────────────────────────────────────────────┘
```

## Key Design Decisions

### Stateless Per-Query DuckDB

Each SQL query creates a fresh in-memory DuckDB instance, configures it with the user's S3 credentials, executes the SQL, collects results, and discards the instance. No state persists between queries. This makes the server horizontally scalable — any number of replicas can serve any request.

### Sidecar Deployment

The server is designed to run in the same Kubernetes pod as the DS3 Gateway, communicating over localhost. This eliminates network latency and keeps S3 traffic within the pod. The server binds to localhost by default and is not exposed externally.

### Cubbit IAM Authentication

Authentication uses Cubbit's IAM challenge-response protocol with Ed25519 signatures. Per-project S3 credentials are automatically provisioned via Cubbit Keyvault, so users don't need to manage API keys manually.

### Three Access Interfaces

- **REST API**: JSON endpoints for programmatic access and integration
- **CLI**: Cobra-based terminal client (`ds3sql`) for scripting and terminal workflows
- **Web UI**: Server-rendered HTML with HTMX for interactive browsing without a build step

## Query Flow

1. User submits SQL via CLI, Web UI, or API call
2. Server validates JWT, extracts S3 credentials from session
3. Server creates in-memory DuckDB instance
4. DuckDB loads `httpfs` + `parquet` extensions
5. Server configures httpfs with DS3 Gateway endpoint + credentials
6. DuckDB executes SQL against S3 files
7. Server collects column names, types, and rows
8. DuckDB instance is discarded
9. Server returns JSON response

## Component Overview

| Component | Responsibility |
|-----------|---------------|
| Auth Service | IAM challenge-response, JWT management, token refresh, credential reconciliation |
| DuckDB Engine | Per-query DuckDB lifecycle, S3 credential injection, SQL execution, schema inference |
| S3 Discovery | Bucket and prefix listing via aws-sdk-go-v2 |
| Web UI | Login, browse, and query pages via html/template + HTMX |
| REST API | JSON endpoints for all functionality |
