// Package quickstart persists and retrieves last-used session settings per folder.
package quickstart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/construct-run/construct/internal/slug"
)

// ErrNoRecord is returned when no quickstart record exists for a folder.
var ErrNoRecord = errors.New("no quickstart record for this folder")

// Record holds the last-used settings for a folder.
type Record struct {
	Folder     string    `json:"folder"`
	Tool       string    `json:"tool"`
	Stack      string    `json:"stack"`
	DockerMode string    `json:"docker_mode"`
	Ports      []string  `json:"ports"`
	SavedAt    time.Time `json:"saved_at"`
}

// Store manages quickstart records on disk.
type Store struct {
	stateDir string
}

// NewStore returns a new quickstart store rooted at stateDir.
func NewStore(stateDir string) *Store {
	return &Store{stateDir: stateDir}
}

// recordPath returns the path to the quickstart record for a folder.
func (s *Store) recordPath(folderPath string) string {
	sl := slug.FromPath(folderPath)
	return filepath.Join(s.stateDir, "quickstart", sl+".json")
}

// Save writes a quickstart record for the given folder.
func (s *Store) Save(r Record) error {
	r.SavedAt = time.Now().UTC()
	path := s.recordPath(r.Folder)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create quickstart dir: %w", err)
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal quickstart record: %w", err)
	}

	// Atomic write
	tmp, err := os.CreateTemp(dir, ".qs-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	return os.Rename(tmpPath, path)
}

// Load loads the quickstart record for a folder.
// Returns ErrNoRecord if no record exists.
func (s *Store) Load(folderPath string) (Record, error) {
	path := s.recordPath(folderPath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Record{}, ErrNoRecord
	}
	if err != nil {
		return Record{}, fmt.Errorf("read quickstart record: %w", err)
	}

	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("parse quickstart record: %w", err)
	}
	return r, nil
}

// Delete removes the quickstart record for a folder.
// Does nothing if no record exists.
func (s *Store) Delete(folderPath string) error {
	path := s.recordPath(folderPath)
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}
