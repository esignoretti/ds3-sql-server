# DS3 SQL Server — Phase 1: Project Skeleton

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Initialize the Go module, create full directory structure, add Makefile, `.gitignore`, config loading, and a compilable `main.go` that starts an HTTP server with a health endpoint.

**Architecture:** Single Go binary under `cmd/ds3sql-server/main.go`. All application logic under `internal/`. Config loaded from YAML file or env vars. Chi router used for HTTP.

**Tech Stack:** Go 1.22+, chi router, `gopkg.in/yaml.v3` for config

---

### Task 1: Initialize module, directory tree, .gitignore, and go.mod

**Files:**
- Create: `DS3-SQL Server/go.mod`
- Create: `DS3-SQL Server/.gitignore`

- [ ] **Step 1: Initialize the Go module**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go mod init github.com/esignoretti/ds3-sql-server
```

Expected output: `go: creating new go.mod: module github.com/esignoretti/ds3-sql-server`

- [ ] **Step 2: Create the directory tree**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && \
mkdir -p cmd/ds3sql-server && \
mkdir -p cmd/ds3sql && \
mkdir -p internal/auth && \
mkdir -p internal/query && \
mkdir -p internal/s3 && \
mkdir -p internal/api && \
mkdir -p internal/web/templates && \
mkdir -p internal/web/static && \
mkdir -p internal/config
```

Expected: no errors, directories created.

- [ ] **Step 3: Write .gitignore**

```bash
cat > .gitignore << 'EOF'
# Binaries
*.exe
*.exe~
*.dll
*.so
*.dylib
*.out

# Go
*.test
*.test.exe
*.prof
go.work
go.work.sum

# IDE
.idea/
.vscode/
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db

# Project
/ds3sql-server
/ds3sql
tmp/

# Superpowers
.superpowers/
EOF
```

Expected: file written, no errors.

---

### Task 2: Config package

**Files:**
- Create: `DS3-SQL Server/internal/config/config.go`

- [ ] **Step 1: Write config package**

Write the config struct and loader:

```go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr   string `yaml:"listen_addr"`
	IAMURL       string `yaml:"iam_url"`
	DS3GatewayURL string `yaml:"ds3_gateway_url"`

	Auth   AuthConfig   `yaml:"auth"`
	Query  QueryConfig  `yaml:"query"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

type AuthConfig struct {
	TokenExpiry        time.Duration `yaml:"token_expiry"`
	RefreshTokenExpiry time.Duration `yaml:"refresh_token_expiry"`
}

type QueryConfig struct {
	MaxRows            int   `yaml:"max_rows"`
	MaxExecutionSecs   int   `yaml:"max_execution_seconds"`
	MaxResultBytes     int64 `yaml:"max_result_bytes"`
}

type RateLimitConfig struct {
	QueriesPerMinute int `yaml:"queries_per_minute"`
}

func Default() *Config {
	return &Config{
		ListenAddr:     ":8080",
		IAMURL:         "https://iam.cubbit.eu",
		DS3GatewayURL:  "http://localhost:9000",
		Auth: AuthConfig{
			TokenExpiry:        24 * time.Hour,
			RefreshTokenExpiry: 720 * time.Hour,
		},
		Query: QueryConfig{
			MaxRows:          10000,
			MaxExecutionSecs: 60,
			MaxResultBytes:   104857600,
		},
		RateLimit: RateLimitConfig{
			QueriesPerMinute: 10,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = os.Getenv("DS3SQL_CONFIG")
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			candidate := home + "/.ds3sql/server.yaml"
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// Env var overrides
	if v := os.Getenv("DS3SQL_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("DS3SQL_IAM_URL"); v != "" {
		cfg.IAMURL = v
	}
	if v := os.Getenv("DS3SQL_DS3_GATEWAY_URL"); v != "" {
		cfg.DS3GatewayURL = v
	}

	return cfg, nil
}
```

- [ ] **Step 2: Add dependency**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get gopkg.in/yaml.v3
```

Expected: `go: added gopkg.in/yaml.v3 vX.Y.Z`

---

### Task 3: Main server entry point

**Files:**
- Create: `DS3-SQL Server/cmd/ds3sql-server/main.go`

- [ ] **Step 1: Write main.go with chi server and /health endpoint**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/esignoretti/ds3-sql-server/internal/config"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Printf("DS3 SQL Server listening on %s\n", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-done
	fmt.Println("\nshutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}
```

- [ ] **Step 2: Add chi dependency**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get github.com/go-chi/chi/v5
```

Expected: `go: added github.com/go-chi/chi/v5 vX.Y.Z`

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql-server/
```

Expected: no errors, binary `ds3sql-server` created.

- [ ] **Step 4: Run health check**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && ./ds3sql-server &
sleep 1
curl -s http://localhost:8080/health
kill %1 2>/dev/null
```

Expected: `{"status":"ok"}`

- [ ] **Step 5: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server init
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: project skeleton with chi server and config"
```

Expected: initial commit created.

---

### Task 4: Makefile

**Files:**
- Create: `DS3-SQL Server/Makefile`

- [ ] **Step 1: Write Makefile**

```makefile
BINARY_SERVER=ds3sql-server
BINARY_CLI=ds3sql
GOBUILD=go build
GOTEST=go test
GOVET=go vet
GOFMT=gofmt -l -s

.PHONY: all build build-server build-cli test vet fmt clean run

all: build

build: build-server build-cli

build-server:
	$(GOBUILD) -o $(BINARY_SERVER) ./cmd/ds3sql-server/

build-cli:
	$(GOBUILD) -o $(BINARY_CLI) ./cmd/ds3sql/

test:
	$(GOTEST) -v -race ./...

vet:
	$(GOVET) ./...

fmt:
	$(GOFMT) ./

clean:
	rm -f $(BINARY_SERVER) $(BINARY_CLI)
	rm -rf tmp/

run: build-server
	./$(BINARY_SERVER)
```

- [ ] **Step 2: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add Makefile
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "chore: add Makefile"
```

Expected: commit created.

---

### Task 5: Dockerfile

**Files:**
- Create: `DS3-SQL Server/Dockerfile`

- [ ] **Step 1: Write multi-stage Dockerfile**

```dockerfile
FROM golang:1.22-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /ds3sql-server ./cmd/ds3sql-server

FROM gcr.io/distroless/base-debian12

COPY --from=builder /ds3sql-server /ds3sql-server

EXPOSE 8080
USER nobody

ENTRYPOINT ["/ds3sql-server"]
```

- [ ] **Step 2: Verify Docker build (optional, requires Docker)**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && docker build -t ds3sql-server:latest .
```

Expected: image built successfully.

- [ ] **Step 3: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add Dockerfile
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "chore: add Dockerfile"
```

Expected: commit created.
