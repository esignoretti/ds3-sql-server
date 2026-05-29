package analysis

import (
	"database/sql"
	"time"
)

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type AnalysisRequest struct {
	Columns []ColumnInfo `json:"columns"`
	Rows    [][]any      `json:"rows"`
}

type ColumnAnalysis struct {
	Type      string        `json:"type"`
	Stats     any           `json:"stats"`
	Histogram []Bin         `json:"histogram,omitempty"`
	TopValues []ValueCount  `json:"top_values,omitempty"`
}

type Bin struct {
	BinStart float64 `json:"bin_start"`
	BinEnd   float64 `json:"bin_end"`
	Count    int     `json:"count"`
}

type ValueCount struct {
	Value string  `json:"value"`
	Count int     `json:"count"`
	Pct   float64 `json:"pct"`
}

type Correlation struct {
	ColA  string  `json:"col_a"`
	ColB  string  `json:"col_b"`
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

type AnalysisResult struct {
	Columns     map[string]ColumnAnalysis `json:"columns"`
	Correlations []Correlation             `json:"correlations"`
	Summary     []string                   `json:"summary"`
	ElapsedMs   int64                      `json:"elapsed_ms"`
	Error       string                     `json:"error,omitempty"`
}

type Engine struct {
	pool chan *sql.DB
}

func NewEngine(pool chan *sql.DB) *Engine {
	return &Engine{pool: pool}
}

func (e *Engine) Analyze(req AnalysisRequest) *AnalysisResult {
	start := time.Now()
	_ = start
	return &AnalysisResult{
		Columns:   make(map[string]ColumnAnalysis),
		ElapsedMs: 0,
		Error:     "not yet implemented",
	}
}
