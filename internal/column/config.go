package column

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ColumnDef struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Start *int   `json:"start,omitempty"`
	End   *int   `json:"end,omitempty"`
}

type ColumnConfig struct {
	Bucket      string       `json:"bucket"`
	Pattern     string       `json:"pattern"`
	ProfileName string       `json:"profile_name,omitempty"`
	Mode        string       `json:"mode"`
	Delimiter   string       `json:"delimiter"`
	Quote       string       `json:"quote"`
	HeaderRow   bool         `json:"header_row"`
	Columns     []ColumnDef  `json:"columns"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type Store struct {
	baseDir string
}

func NewStore(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create column config dir: %w", err)
	}
	return &Store{baseDir: baseDir}, nil
}

func (s *Store) configPath(bucket string) string {
	dir := filepath.Join(s.baseDir, sanitizePath(bucket))
	os.MkdirAll(dir, 0755)
	return dir
}

func sanitizePath(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_").Replace(s)
}

func (s *Store) filePath(bucket, pattern string) string {
	return filepath.Join(s.configPath(bucket), sanitizePath(pattern)+".json")
}

func (s *Store) Save(cfg *ColumnConfig) error {
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = time.Now()
	}
	cfg.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal column config: %w", err)
	}
	if err := os.WriteFile(s.filePath(cfg.Bucket, cfg.Pattern), data, 0644); err != nil {
		return fmt.Errorf("write column config: %w", err)
	}
	return nil
}

func (s *Store) Get(bucket, pattern string) (*ColumnConfig, error) {
	data, err := os.ReadFile(s.filePath(bucket, pattern))
	if err != nil {
		return nil, fmt.Errorf("read column config: %w", err)
	}
	var cfg ColumnConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse column config: %w", err)
	}
	return &cfg, nil
}

func (s *Store) List(bucket string) ([]ColumnConfig, error) {
	dir := s.configPath(bucket)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read column config dir: %w", err)
	}
	var configs []ColumnConfig
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var cfg ColumnConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

func (s *Store) Delete(bucket, pattern string) error {
	return os.Remove(s.filePath(bucket, pattern))
}

// Match finds the best matching column config for a filename.
// Most specific pattern (longest) wins.
func (s *Store) Match(bucket, filename string) *ColumnConfig {
	configs, err := s.List(bucket)
	if err != nil || len(configs) == 0 {
		return nil
	}
	sort.Slice(configs, func(i, j int) bool {
		return len(configs[i].Pattern) > len(configs[j].Pattern)
	})
	for _, cfg := range configs {
		if matched, _ := filepath.Match(cfg.Pattern, filename); matched {
			return &cfg
		}
		base := filepath.Base(filename)
		if matched, _ := filepath.Match(cfg.Pattern, base); matched {
			return &cfg
		}
	}
	return nil
}
