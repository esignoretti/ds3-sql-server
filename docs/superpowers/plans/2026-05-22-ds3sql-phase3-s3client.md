# DS3 SQL Server — Phase 3: S3 Discovery Client

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement S3 bucket and object listing using `aws-sdk-go-v2` with a custom DS3 Gateway endpoint. Powers the browse UI and CLI `ls`/`buckets` commands.

**Architecture:** Thin wrapper around the AWS S3 SDK with a custom endpoint resolver pointing to the DS3 Gateway. Uses the user's access key + secret key from the auth session.

**Tech Stack:** Go 1.22+, `github.com/aws/aws-sdk-go-v2/service/s3`, `github.com/aws/aws-sdk-go-v2/config`

---

### Task 1: S3 client wrapper

**Files:**
- Create: `DS3-SQL Server/internal/s3/client.go`
- Create: `DS3-SQL Server/internal/s3/listing.go`
- Create: `DS3-SQL Server/internal/s3/client_test.go`

- [ ] **Step 1: Write the S3 client factory**

`DS3-SQL Server/internal/s3/client.go`:

```go
package s3

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	client *awss3.Client
}

func NewClient(ctx context.Context, accessKey, secretKey, endpoint string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("us-east-1"),
	)
	if err != nil {
		return nil, err
	}

	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return &Client{client: client}, nil
}
```

- [ ] **Step 2: Write bucket and object listing**

`DS3-SQL Server/internal/s3/listing.go`:

```go
package s3

import (
	"context"
	"fmt"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type BucketInfo struct {
	Name         string
	CreationDate string
}

type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified string
}

type ListResult struct {
	Prefixes    []string
	Objects     []ObjectInfo
	IsTruncated bool
}

func (c *Client) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	result, err := c.client.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}

	buckets := make([]BucketInfo, 0, len(result.Buckets))
	for _, b := range result.Buckets {
		date := ""
		if b.CreationDate != nil {
			date = b.CreationDate.Format("2006-01-02T15:04:05Z")
		}
		buckets = append(buckets, BucketInfo{
			Name:         *b.Name,
			CreationDate: date,
		})
	}
	return buckets, nil
}

func (c *Client) ListObjects(ctx context.Context, bucket, prefix, delimiter string, maxKeys int32) (*ListResult, error) {
	if delimiter == "" {
		delimiter = "/"
	}
	if maxKeys <= 0 {
		maxKeys = 100
	}

	input := &awss3.ListObjectsV2Input{
		Bucket:    &bucket,
		Prefix:    &prefix,
		Delimiter: &delimiter,
		MaxKeys:   &maxKeys,
	}

	result, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	var resp ListResult

	for _, cp := range result.CommonPrefixes {
		if cp.Prefix != nil {
			resp.Prefixes = append(resp.Prefixes, *cp.Prefix)
		}
	}

	for _, obj := range result.Contents {
		modified := ""
		if obj.LastModified != nil {
			modified = obj.LastModified.Format("2006-01-02T15:04:05Z")
		}
		resp.Objects = append(resp.Objects, ObjectInfo{
			Key:          *obj.Key,
			Size:         obj.Size,
			LastModified: modified,
		})
	}

	resp.IsTruncated = result.IsTruncated

	return &resp, nil
}
```

- [ ] **Step 3: Write test (requires mock S3 or real DS3)**

`DS3-SQL Server/internal/s3/client_test.go`:

```go
package s3

import (
	"context"
	"os"
	"testing"
)

func skipIfNoCreds(t *testing.T) {
	t.Helper()
	if os.Getenv("DS3SQL_TEST_ACCESS_KEY") == "" {
		t.Skip("set DS3SQL_TEST_ACCESS_KEY/DS3SQL_TEST_SECRET_KEY/DS3SQL_TEST_ENDPOINT for integration tests")
	}
}

func TestListBucketsIntegration(t *testing.T) {
	skipIfNoCreds(t)

	ctx := context.Background()
	client, err := NewClient(ctx,
		os.Getenv("DS3SQL_TEST_ACCESS_KEY"),
		os.Getenv("DS3SQL_TEST_SECRET_KEY"),
		os.Getenv("DS3SQL_TEST_ENDPOINT"),
	)
	if err != nil {
		t.Fatal(err)
	}

	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("found %d buckets", len(buckets))
	for _, b := range buckets {
		t.Logf("  %s (%s)", b.Name, b.CreationDate)
	}
}
```

- [ ] **Step 4: Add dependencies**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get github.com/aws/aws-sdk-go-v2 github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/credentials github.com/aws/aws-sdk-go-v2/service/s3
```

Expected: modules added.

- [ ] **Step 5: Build verification**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./internal/s3/
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add internal/s3/ && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: S3 discovery client for DS3 Gateway"
```

---

### Task 2: S3 bucket API handler

**Files:**
- Create: `DS3-SQL Server/internal/api/bucket_handler.go`

- [ ] **Step 1: Write bucket handler**

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
)

type BucketHandler struct {
	s3Client *s3.Client
}

func NewBucketHandler(s3Client *s3.Client) *BucketHandler {
	return &BucketHandler{s3Client: s3Client}
}

func (h *BucketHandler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.s3Client.ListBuckets(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"buckets": buckets})
}

func (h *BucketHandler) ListObjects(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	prefix := r.URL.Query().Get("prefix")

	result, err := h.s3Client.ListObjects(r.Context(), bucket, prefix, "/", 100)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
```

- [ ] **Step 2: Wire into main.go**

Add to the protected route group in `cmd/ds3sql-server/main.go`:

```go
// S3 client (needs session with credentials)
s3Handler := func(w http.ResponseWriter, r *http.Request) *s3.Client {
	session := auth.GetSession(r)
	client, err := s3.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
	if err != nil {
		http.Error(w, `{"error":"failed to create s3 client"}`, http.StatusInternalServerError)
		return nil
	}
	return client
}
```

However, this pattern requires the handler to create an S3 client per request. A better approach: inject it via the handler since the S3 client is session-specific. Add the bucket routes:

```go
r.Group(func(r chi.Router) {
	r.Use(auth.Middleware(sessionStore))

	r.Get("/auth/me", authHandler.Me)

	r.Get("/buckets", func(w http.ResponseWriter, r *http.Request) {
		session := auth.GetSession(r)
		client, err := s3.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
		if err != nil {
			http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
			return
		}
		h := api.NewBucketHandler(client)
		h.ListBuckets(w, r)
	})

	r.Get("/buckets/{bucket}", func(w http.ResponseWriter, r *http.Request) {
		session := auth.GetSession(r)
		client, err := s3.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
		if err != nil {
			http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
			return
		}
		h := api.NewBucketHandler(client)
		h.ListObjects(w, r)
	})
})
```

- [ ] **Step 3: Build verification**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql-server/
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: bucket listing API handlers"
```
