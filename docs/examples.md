# Examples

## Prerequisites

- A running DS3 SQL Server and DS3 Gateway
- A CSV or Parquet file uploaded to an S3 bucket
- Authenticated via `ds3sql login` or API

## Basic Queries

### List first 10 rows of a CSV

```sql
SELECT * FROM read_csv_auto('s3://my-bucket/customers-1000.csv') LIMIT 10;
```

### Query specific columns with filter

```sql
SELECT "First Name", "Last Name", Company, Country
FROM read_csv_auto('s3://my-bucket/customers-1000.csv')
WHERE Country = 'China'
LIMIT 20;
```

### Aggregate: count customers per country

```sql
SELECT Country, count(*) as cnt
FROM read_csv_auto('s3://my-bucket/customers-1000.csv')
GROUP BY Country
ORDER BY cnt DESC
LIMIT 10;
```

### Search for a specific customer

```sql
SELECT * FROM read_csv_auto('s3://my-bucket/customers-1000.csv')
WHERE "First Name" = 'John' OR "Last Name" = 'Doe';
```

### Count total rows

```sql
SELECT count(*) as total_customers
FROM read_csv_auto('s3://my-bucket/customers-1000.csv');
```

## Parquet Queries

### Query all columns from a Parquet file

```sql
SELECT * FROM read_parquet('s3://my-bucket/data/*.parquet') LIMIT 100;
```

### Aggregation on Parquet data

```sql
SELECT level, count(*) as cnt
FROM read_parquet('s3://my-bucket/logs/*.parquet')
GROUP BY level
ORDER BY cnt DESC
LIMIT 10;
```

### Date range filter on Parquet

```sql
SELECT timestamp, level, message
FROM read_parquet('s3://my-bucket/logs/*.parquet')
WHERE timestamp >= '2026-01-01' AND timestamp < '2026-02-01'
ORDER BY timestamp
LIMIT 100;
```

## Using the CLI

### Authenticate

```bash
ds3sql login
```

### Run a query

```bash
ds3sql query "SELECT count(*) FROM read_csv_auto('s3://my-bucket/data.csv')"
```

### Run a query from a file

```bash
ds3sql query -f query.sql
```

### Output as JSON

```bash
ds3sql query --json "SELECT * FROM read_parquet('s3://my-bucket/data.parquet') LIMIT 10"
```

## Using curl

### Authenticate

```bash
curl -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"your-password"}'
```

Set the token for subsequent requests:

```bash
export TOKEN="eyJhbGciOiJFUzI1NiIs..."
```

### List buckets

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/buckets
```

### Run a query

```bash
curl -X POST http://localhost:8080/query \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT count(*) FROM read_csv_auto('"'"'s3://my-bucket/data.csv'"'"')","bucket":"my-bucket"}'
```

### Infer schema

```bash
curl -X POST http://localhost:8080/schema \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path":"s3://my-bucket/data.parquet"}'
```
