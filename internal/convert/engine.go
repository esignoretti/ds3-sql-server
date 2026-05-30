package convert

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/column"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
	"github.com/google/uuid"
)

type ConvertRequest struct {
	Bucket         string   `json:"bucket"`
	Files          []string `json:"files"`
	DeleteOriginal bool     `json:"delete_original"`
	Endpoint       string   `json:"-"`
	AccessKey      string   `json:"-"`
	SecretKey      string   `json:"-"`
}

type Engine struct {
	pool     chan *sql.DB
	workers  int
	jobs     *JobStore
	colStore *column.Store
}

func NewEngine(pool chan *sql.DB, workers int, colStore *column.Store) *Engine {
	poolSize := cap(pool)
	if workers < 1 {
		workers = 1
	}
	if workers > poolSize {
		workers = poolSize
	}
	return &Engine{
		pool:     pool,
		workers:  workers,
		jobs:     NewJobStore(),
		colStore: colStore,
	}
}

func (e *Engine) JobStore() *JobStore {
	return e.jobs
}

func detectFormat(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".syslog") || strings.HasSuffix(lower, ".syslog.1"):
		return "syslog"
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonl"):
		return "json"
	case strings.HasSuffix(lower, ".log"):
		return "log"
	case strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".out") || strings.HasSuffix(lower, ".err"):
		return "text"
	default:
		return "text"
	}
}

func convertibleExt(ext string) bool {
	lower := strings.ToLower(ext)
	switch lower {
	case ".parquet", ".csv", ".tsv", ".json", ".jsonl":
		return false
	default:
		return true
	}
}

func (e *Engine) Start(req ConvertRequest) (*Job, error) {
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("no files provided")
	}

	jobID := uuid.New().String()
	results := make([]FileResult, len(req.Files))
	for i, f := range req.Files {
		results[i] = FileResult{File: f, Status: "pending"}
	}

	job := &Job{
		ID:        jobID,
		Bucket:    req.Bucket,
		Total:     len(req.Files),
		Status:    "running",
		Results:   results,
		CreatedAt: time.Now(),
	}
	e.jobs.Set(jobID, job)

	ctx := context.Background()
	go e.run(ctx, job, req)

	return job, nil
}

func (e *Engine) run(ctx context.Context, job *Job, req ConvertRequest) {
	files := make(chan int, len(req.Files))
	for i := range req.Files {
		files <- i
	}
	close(files)

	var wg sync.WaitGroup
	for w := 0; w < e.workers; w++ {
		wg.Add(1)
		go e.worker(ctx, &wg, job, req, files)
	}
	wg.Wait()

	job.mu.Lock()
	allDone := true
	hasError := false
	for _, r := range job.Results {
		if r.Status == "error" {
			hasError = true
			allDone = false
		}
		if r.Status != "done" && r.Status != "error" {
			allDone = false
		}
	}
	if allDone && !hasError {
		job.Status = "done"
	} else if hasError {
		job.Status = "error"
	} else {
		job.Status = "done"
	}
	job.mu.Unlock()
}

func (e *Engine) worker(ctx context.Context, wg *sync.WaitGroup, job *Job, req ConvertRequest, files chan int) {
	defer wg.Done()

	for idx := range files {
		file := req.Files[idx]

		job.mu.Lock()
		job.Results[idx].Status = "running"
		job.mu.Unlock()

		start := time.Now()
		err := e.convertFile(file, req.Bucket, req.Endpoint, req.AccessKey, req.SecretKey)
		elapsed := time.Since(start).Milliseconds()

		job.mu.Lock()
		job.Results[idx].ElapsedMs = elapsed
		if err != nil {
			job.Results[idx].Status = "error"
			job.Results[idx].Error = err.Error()
		} else {
			job.Results[idx].Status = "done"
			job.Results[idx].Converted = file + ".parquet"
			job.Completed++

			if req.DeleteOriginal {
				delErr := e.deleteOriginal(ctx, req.Bucket, file, req.Endpoint, req.AccessKey, req.SecretKey)
				if delErr != nil {
					job.Results[idx].Error = "converted but delete failed: " + delErr.Error()
				}
			}
		}
		job.mu.Unlock()
	}
}

func (e *Engine) convertFile(file, bucket, endpoint, accessKey, secretKey string) error {
	db := <-e.pool
	defer func() { e.pool <- db }()

	useSSL := true
	rawEndpoint := endpoint
	if idx := strings.Index(rawEndpoint, "://"); idx >= 0 {
		useSSL = strings.HasPrefix(rawEndpoint[:idx], "https")
		rawEndpoint = rawEndpoint[idx+3:]
	}
	useSSLStr := "false"
	if useSSL {
		useSSLStr = "true"
	}

	secretSQL := "CREATE OR REPLACE SECRET ds3_s3 (TYPE S3, KEY_ID '" + accessKey + "', SECRET '" + secretKey + "', ENDPOINT '" + rawEndpoint + "', REGION 'us-east-1', USE_SSL " + useSSLStr + ", URL_STYLE 'path')"
	if _, err := db.Exec(secretSQL); err != nil {
		return fmt.Errorf("set s3 credentials: %w", err)
	}

	s3Path := "s3://" + bucket + "/" + file
	outputPath := "s3://" + bucket + "/" + file + ".parquet"

	// Check for saved column config first
	savedCfg := e.colStore.Match(bucket, file)

	var (
		readSQL string
		sqlErr  error
	)
	if savedCfg != nil {
		cfg := savedCfg
		if cfg.Mode == "fixed_width" {
			readSQL, sqlErr = buildFixedWidthSQL(cfg, s3Path)
			if sqlErr != nil {
				return sqlErr
			}
		} else {
			delim := cfg.Delimiter
			quote := cfg.Quote
			headerStr := "FALSE"
			if cfg.HeaderRow {
				headerStr = "TRUE"
			}
			if len(cfg.Columns) > 0 {
				var selects []string
				for i, col := range cfg.Columns {
					selects = append(selects, fmt.Sprintf("CAST(column%d AS %s) AS %s", i, col.Type, col.Name))
				}
				readSQL = fmt.Sprintf(`SELECT %s FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
					strings.Join(selects, ","), s3Path, delim, quote, headerStr)
			} else {
				readSQL = fmt.Sprintf(`SELECT * FROM read_csv('%s', DELIM='%s', QUOTE='%s', HEADER=%s, all_varchar=true, ignore_errors=true, null_padding=true)`,
					s3Path, delim, quote, headerStr)
			}
		}
	} else {
		f := detectFormat(file)
		switch f {
		case "syslog", "log":
			readSQL = fmt.Sprintf(`SELECT * FROM read_csv('%s', DELIM=' ', QUOTE='"', HEADER=FALSE, all_varchar=true, ignore_errors=true, null_padding=true)`, s3Path)
		case "json":
			readSQL = fmt.Sprintf("SELECT * FROM read_json_auto('%s')", s3Path)
		default:
			readSQL = fmt.Sprintf("SELECT * FROM read_csv_auto('%s', HEADER=FALSE)", s3Path)
		}
	}

	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (FORMAT PARQUET)", readSQL, outputPath)

	if _, err := db.Exec(copySQL); err != nil {
		return fmt.Errorf("convert %s: %w", file, err)
	}

	return nil
}

func buildFixedWidthSQL(cfg *column.ColumnConfig, s3Path string) (string, error) {
	if len(cfg.Columns) == 0 {
		return "", fmt.Errorf("fixed_width mode requires at least one column")
	}
	var selects []string
	for i, col := range cfg.Columns {
		colName := col.Name
		if colName == "" {
			colName = fmt.Sprintf("col%d", i)
		}
		colType := col.Type
		if colType == "" {
			colType = "VARCHAR"
		}
		start := 0
		if col.Start != nil {
			start = *col.Start
		}
		if col.End != nil {
			selects = append(selects, fmt.Sprintf("CAST(substr(text, %d, %d) AS %s) AS \"%s\"", start+1, *col.End-start, colType, strings.ReplaceAll(colName, "\"", "\"\"")))
		} else {
			selects = append(selects, fmt.Sprintf("CAST(substr(text, %d) AS %s) AS \"%s\"", start+1, colType, strings.ReplaceAll(colName, "\"", "\"\"")))
		}
	}
	return fmt.Sprintf(`SELECT %s FROM read_text('%s')`, strings.Join(selects, ","), s3Path), nil
}

func (e *Engine) deleteOriginal(ctx context.Context, bucket, file, endpoint, accessKey, secretKey string) error {
	client, err := s3.NewClient(ctx, accessKey, secretKey, endpoint)
	if err != nil {
		return fmt.Errorf("create s3 client: %w", err)
	}
	return client.DeleteObject(ctx, bucket, file)
}
