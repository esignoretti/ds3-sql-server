package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type DiskStore struct {
	baseDir string
}

func NewDiskStore(baseDir string) (*DiskStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create report dir: %w", err)
	}
	return &DiskStore{baseDir: baseDir}, nil
}

func (s *DiskStore) path(id string) string {
	return filepath.Join(s.baseDir, id+".json")
}

func (s *DiskStore) List() ([]ReportSummary, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("read report dir: %w", err)
	}
	var summaries []ReportSummary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			continue
		}
		var r Report
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		rowCount := 0
		if r.QueryRows != nil {
			rowCount = len(r.QueryRows)
		}
		summaries = append(summaries, ReportSummary{
			ID: r.ID, Title: r.Title, CreatedAt: r.CreatedAt, RowCount: rowCount,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

func (s *DiskStore) Save(report *Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(s.path(report.ID), data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func (s *DiskStore) Get(id string) (*Report, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", id, err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", id, err)
	}
	return &r, nil
}

func (s *DiskStore) Delete(id string) error {
	if err := os.Remove(s.path(id)); err != nil {
		return fmt.Errorf("delete report %s: %w", id, err)
	}
	return nil
}
