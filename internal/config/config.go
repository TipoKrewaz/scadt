package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/TipoKrewaz/scadt/internal/models"
)

type File struct {
	Listen         string                 `json:"listen"`
	DataDir        string                 `json:"data_dir"`
	RetentionDays  int                    `json:"retention_days"`
	Servers        []models.Server        `json:"servers"`
	SavedRequests  []models.SavedRequest  `json:"saved_requests"`
	AlertRules     []models.AlertRule     `json:"alert_rules"`
	AlertChannels  []models.AlertChannel  `json:"alert_channels"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data File
}

func Load(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.reload(); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		s.data = defaultConfig()
		if err := s.Save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}
	if f.Listen == "" {
		f.Listen = "127.0.0.1:0"
	}
	if f.DataDir == "" {
		f.DataDir = filepath.Join(filepath.Dir(s.path), "data")
	}
	if f.RetentionDays == 0 {
		f.RetentionDays = 7
	}
	s.data = f
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	b, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Snapshot() File {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopy(s.data)
}

func (s *Store) Update(fn func(*File)) error {
	s.mu.Lock()
	fn(&s.data)
	s.mu.Unlock()
	return s.Save()
}

func (s *Store) Path() string { return s.path }

func deepCopy(f File) File {
	b, _ := json.Marshal(f)
	var out File
	_ = json.Unmarshal(b, &out)
	return out
}

func defaultConfig() File {
	return File{
		Listen:        "127.0.0.1:0",
		DataDir:       "data",
		RetentionDays: 7,
		Servers:       []models.Server{},
		SavedRequests: []models.SavedRequest{
			{ID: "req_health", Name: "Health check", Group: "Health", Method: "GET", Path: "/healthz"},
			{ID: "req_metrics", Name: "Prometheus metrics", Group: "Metrics", Method: "GET", Path: "/metrics"},
		},
		AlertRules:    []models.AlertRule{},
		AlertChannels: []models.AlertChannel{},
	}
}
