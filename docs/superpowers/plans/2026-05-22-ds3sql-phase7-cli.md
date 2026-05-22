# DS3 SQL Server — Phase 7: CLI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `ds3sql` CLI using Cobra. Commands: `login`, `logout`, `status`, `buckets`, `ls`, `schema`, `query`. The CLI talks to a running DS3 SQL Server via REST API.

**Architecture:** Cobra commands that call the server's REST API. Config stored in `~/.ds3sql/config` (host, port, token). Table-formatted output for humans, `--json` flag for programmatic use.

**Tech Stack:** Go 1.22+, `github.com/spf13/cobra`

---

### Task 1: CLI skeleton with config

**Files:**
- Create: `DS3-SQL Server/cmd/ds3sql/main.go`
- Create: `DS3-SQL Server/cmd/ds3sql/config.go`

- [ ] **Step 1: Write the CLI entry point**

`DS3-SQL Server/cmd/ds3sql/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ds3sql",
	Short: "DS3 SQL Server CLI — query Cubbit S3 buckets with SQL",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Write CLI config management**

`DS3-SQL Server/cmd/ds3sql/config.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CLIConfig struct {
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ds3sql", "config")
}

func loadConfig() (*CLIConfig, error) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("not logged in (use 'ds3sql login')")
	}

	var cfg CLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

func saveConfig(cfg *CLIConfig) error {
	path := configPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func clearConfig() error {
	path := configPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func serverURL(cfg *CLIConfig) string {
	return fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)
}
```

- [ ] **Step 3: Add cobra dependency**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go get github.com/spf13/cobra
```

Expected: `go: added github.com/spf13/cobra vX.Y.Z`

---

### Task 2: CLI commands

**Files:**
- Create: `DS3-SQL Server/cmd/ds3sql/login.go`
- Create: `DS3-SQL Server/cmd/ds3sql/logout.go`
- Create: `DS3-SQL Server/cmd/ds3sql/status.go`
- Create: `DS3-SQL Server/cmd/ds3sql/buckets.go`
- Create: `DS3-SQL Server/cmd/ds3sql/ls.go`
- Create: `DS3-SQL Server/cmd/ds3sql/schema.go`
- Create: `DS3-SQL Server/cmd/ds3sql/query.go`

- [ ] **Step 1: Write login command**

`DS3-SQL Server/cmd/ds3sql/login.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

func init() {
	loginCmd.Flags().String("host", "localhost", "DS3 SQL Server host")
	loginCmd.Flags().Int("port", 8080, "DS3 SQL Server port")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Cubbit IAM",
	RunE: func(cmd *cobra.Command, args []string) error {
		host, _ := cmd.Flags().GetString("host")
		port, _ := cmd.Flags().GetInt("port")

		fmt.Print("Email: ")
		var email string
		fmt.Scanln(&email)

		fmt.Print("Password: ")
		var password string
		// In production use terminal.ReadPassword
		fmt.Scanln(&password)

		body, _ := json.Marshal(map[string]string{
			"email":    email,
			"password": password,
		})

		resp, err := http.Post(fmt.Sprintf("http://%s:%d/auth/login", host, port), "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("login request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("login failed (%d): %s", resp.StatusCode, string(b))
		}

		var result struct {
			Token        string `json:"token"`
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}

		cfg := &CLIConfig{
			Host:  host,
			Port:  port,
			Token: result.Token,
		}

		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Println("Logged in successfully")
		return nil
	},
}
```

- [ ] **Step 2: Write logout command**

`DS3-SQL Server/cmd/ds3sql/logout.go`:

```go
package main

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := clearConfig(); err != nil {
			return err
		}
		fmt.Println("Logged out")
		return nil
	},
}
```

- [ ] **Step 3: Write status command**

`DS3-SQL Server/cmd/ds3sql/status.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show connection status and server health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		// Health check
		resp, err := http.Get(serverURL(cfg) + "/health")
		if err != nil {
			return fmt.Errorf("server unreachable: %w", err)
		}
		defer resp.Body.Close()

		var health struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&health)

		fmt.Printf("Server:   %s\n", serverURL(cfg))
		fmt.Printf("Health:   %s\n", health.Status)
		fmt.Printf("Token:    %s…\n", cfg.Token[:min(8, len(cfg.Token))])

		// Me endpoint
		meResp, err := authedGet(cfg, "/auth/me")
		if err == nil {
			var me struct {
				Email string `json:"email"`
			}
			json.Unmarshal(meResp, &me)
			fmt.Printf("User:     %s\n", me.Email)
		}

		return nil
	},
}

func authedGet(cfg *CLIConfig, path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", serverURL(cfg)+path, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func authedPost(cfg *CLIConfig, path string, body []byte) ([]byte, error) {
	req, _ := http.NewRequest("POST", serverURL(cfg)+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 4: Write buckets command**

`DS3-SQL Server/cmd/ds3sql/buckets.go`:

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(bucketsCmd)
}

var bucketsCmd = &cobra.Command{
	Use:   "buckets",
	Short: "List accessible S3 buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		data, err := authedGet(cfg, "/buckets")
		if err != nil {
			return err
		}

		var result struct {
			Buckets []struct {
				Name         string `json:"name"`
				CreationDate string `json:"creation_date"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		fmt.Printf("%-30s %s\n", "NAME", "CREATED")
		fmt.Println("----------------------------------------------")
		for _, b := range result.Buckets {
			fmt.Printf("%-30s %s\n", b.Name, b.CreationDate)
		}
		return nil
	},
}
```

- [ ] **Step 5: Write ls command**

`DS3-SQL Server/cmd/ds3sql/ls.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	lsCmd.Flags().StringP("prefix", "p", "", "Prefix to filter by")
	rootCmd.AddCommand(lsCmd)
}

var lsCmd = &cobra.Command{
	Use:   "ls <bucket>",
	Short: "List objects in a bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		bucket := args[0]
		prefix, _ := cmd.Flags().GetString("prefix")

		url := fmt.Sprintf("/buckets/%s", bucket)
		if prefix != "" {
			url += "?prefix=" + prefix
		}

		data, err := authedGet(cfg, url)
		if err != nil {
			return err
		}

		var result struct {
			Prefixes    []string `json:"prefixes"`
			Objects     []struct {
				Key          string `json:"key"`
				Size         int64  `json:"size"`
				LastModified string `json:"last_modified"`
			} `json:"objects"`
			IsTruncated bool `json:"is_truncated"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		for _, p := range result.Prefixes {
			fmt.Printf("📁 %-50s\n", strings.TrimSuffix(p, "/")+"/")
		}
		for _, o := range result.Objects {
			size := formatBytes(o.Size)
			fmt.Printf("📄 %-50s %10s  %s\n", o.Key, size, o.LastModified)
		}
		if result.IsTruncated {
			fmt.Println("... (truncated, use --prefix to narrow)")
		}
		return nil
	},
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
```

- [ ] **Step 6: Write schema command**

`DS3-SQL Server/cmd/ds3sql/schema.go`:

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(schemaCmd)
}

var schemaCmd = &cobra.Command{
	Use:   "schema <s3-path>",
	Short: "Show columns and types for a S3 path",
	Args:  cobra.ExactArgs(1),
	Long: `Show schema for Parquet/CSV files at an S3 path.
Example: ds3ql schema 's3://my-bucket/logs/*.parquet'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		body, _ := json.Marshal(map[string]string{"path": args[0]})
		data, err := authedPost(cfg, "/schema", body)
		if err != nil {
			return err
		}

		var result struct {
			Columns []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Nullable bool   `json:"nullable"`
			} `json:"columns"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if result.Error != "" {
			return fmt.Errorf("schema error: %s", result.Error)
		}

		fmt.Printf("%-30s %-20s %s\n", "COLUMN", "TYPE", "NULLABLE")
		fmt.Println("----------------------------------------------")
		for _, c := range result.Columns {
			nullable := "NO"
			if c.Nullable {
				nullable = "YES"
			}
			fmt.Printf("%-30s %-20s %s\n", c.Name, c.Type, nullable)
		}
		return nil
	},
}
```

- [ ] **Step 7: Write query command**

`DS3-SQL Server/cmd/ds3sql/query.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	queryCmd.Flags().Bool("json", false, "Output as JSON")
	queryCmd.Flags().StringP("file", "f", "", "Read SQL from file")
	rootCmd.AddCommand(queryCmd)
}

var queryCmd = &cobra.Command{
	Use:   "query <sql>",
	Short: "Execute SQL query against S3 data",
	Args:  cobra.RangeArgs(0, 1),
	Long: `Execute a SQL query against data in S3.
Examples:
  ds3ql query "SELECT count(*) FROM read_parquet('s3://bucket/*.parquet')"
  ds3ql query -f query.sql`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		var sql string
		if file, _ := cmd.Flags().GetString("file"); file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			sql = string(data)
		} else if len(args) > 0 {
			sql = args[0]
		} else {
			return fmt.Errorf("provide SQL as argument or with --file")
		}

		body, _ := json.Marshal(map[string]string{"sql": sql})
		data, err := authedPost(cfg, "/query", body)
		if err != nil {
			return err
		}

		var result struct {
			Columns   []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"columns"`
			Rows      [][]any `json:"rows"`
			RowCount  int     `json:"row_count"`
			ElapsedMs int64   `json:"elapsed_ms"`
			Error     string  `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if result.Error != "" {
			return fmt.Errorf("query error: %s", result.Error)
		}

		if isJSON, _ := cmd.Flags().GetBool("json"); isJSON {
			fmt.Println(string(data))
			return nil
		}

		// Table output
		if result.RowCount == 0 {
			fmt.Println("No rows returned")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

		// Header
		headers := make([]string, len(result.Columns))
		for i, c := range result.Columns {
			headers[i] = c.Name
		}
		fmt.Fprintln(w, strings.Join(headers, "\t"))

		// Separator
		seps := make([]string, len(result.Columns))
		for i := range seps {
			seps[i] = "---"
		}
		fmt.Fprintln(w, strings.Join(seps, "\t"))

		// Rows
		for _, row := range result.Rows {
			cells := make([]string, len(row))
			for i, cell := range row {
				if cell == nil {
					cells[i] = "NULL"
				} else {
					cells[i] = fmt.Sprintf("%v", cell)
				}
			}
			fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
		w.Flush()

		fmt.Printf("\n(%d rows, %dms)\n", result.RowCount, result.ElapsedMs)
		return nil
	},
}
```

- [ ] **Step 8: Build the CLI**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && go build ./cmd/ds3sql/
```

Expected: no errors, `ds3sql` binary created.

- [ ] **Step 9: Test help output**

```bash
cd /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server && ./ds3sql --help
```

Expected: shows all commands.

- [ ] **Step 10: Commit**

```bash
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server add -A && \
git -C /Users/esignoretti/Documents/OpenCode/DS3-SQL\ Server commit -m "feat: CLI with login, buckets, ls, schema, query commands"
```
