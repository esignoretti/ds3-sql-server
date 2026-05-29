package report

import (
	"time"
)

type ChartConfig struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	XColumn   string `json:"x_column"`
	YColumn   string `json:"y_column"`
	GroupBy   string `json:"group_by,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Title     string `json:"title,omitempty"`
	SortOrder string `json:"sort_order,omitempty"`
	MaxGroups int    `json:"max_groups,omitempty"`
}

type Report struct {
	ID           string        `json:"id"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	Title        string        `json:"title"`
	SQL          string        `json:"sql"`
	ProjectID    string        `json:"project_id"`
	QueryColumns []ColumnInfo  `json:"query_columns"`
	QueryRows    [][]any       `json:"query_rows"`
	Analysis     any           `json:"analysis"`
	Charts       []ChartConfig `json:"charts"`
}

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ReportSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	RowCount  int       `json:"row_count"`
}

type Store interface {
	List() ([]ReportSummary, error)
	Save(report *Report) error
	Get(id string) (*Report, error)
	Delete(id string) error
}
