package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type DiskStore struct {
	baseDir string
}

func NewDiskStore(baseDir string) (*DiskStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create report dir: %w", err)
	}
	return &DiskStore{baseDir: baseDir}, nil
}

func (s *DiskStore) path(projectID, id string) string {
	return filepath.Join(s.baseDir, projectID, id+".json")
}

func (s *DiskStore) validateID(id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid report id: %q", id)
	}
	return nil
}

func (s *DiskStore) List(projectID string) ([]ReportSummary, error) {
	dir := filepath.Join(s.baseDir, projectID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// If the project directory doesn't exist yet, return empty list.
		return []ReportSummary{}, nil
	}
	var summaries []ReportSummary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		data, err := os.ReadFile(s.path(projectID, id))
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
	if err := s.validateID(report.ID); err != nil {
		return err
	}
	if report.ProjectID == "" {
		return fmt.Errorf("report missing project_id")
	}
	if err := s.validateID(report.ProjectID); err != nil {
		return err
	}
	dir := filepath.Join(s.baseDir, report.ProjectID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create project report dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(s.path(report.ProjectID, report.ID), data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func (s *DiskStore) Get(projectID, id string) (*Report, error) {
	if err := s.validateID(projectID); err != nil {
		return nil, err
	}
	if err := s.validateID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path(projectID, id))
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", id, err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", id, err)
	}
	return &r, nil
}

func (s *DiskStore) Delete(projectID, id string) error {
	if err := s.validateID(projectID); err != nil {
		return err
	}
	if err := s.validateID(id); err != nil {
		return err
	}
	if err := os.Remove(s.path(projectID, id)); err != nil {
		return fmt.Errorf("delete report %s: %w", id, err)
	}
	return nil
}
