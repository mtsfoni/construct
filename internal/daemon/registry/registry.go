// Package registry manages the in-memory and on-disk session state.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status represents the session lifecycle state.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
)

// PortMapping is a published port entry.
type PortMapping struct {
	HostPort      int `json:"host_port"`
	ContainerPort int `json:"container_port"`
}

// Session represents a persistent session record.
type Session struct {
	ID                string        `json:"id"`
	Repo              string        `json:"repo"`
	Tool              string        `json:"tool"`
	Stack             string        `json:"stack"`
	DockerMode        string        `json:"docker_mode"`
	Debug             bool          `json:"debug"`
	Ports             []PortMapping `json:"ports"`
	WebPort           int           `json:"web_port"`
	ContainerName     string        `json:"container_name"`
	HostUID           int           `json:"host_uid"`
	HostGID           int           `json:"host_gid"`
	OpenCodeConfigDir string        `json:"opencode_config_dir"`
	Status            Status        `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	StartedAt         *time.Time    `json:"started_at,omitempty"`
	StoppedAt         *time.Time    `json:"stopped_at,omitempty"`
}

// ShortID returns the first 8 characters of the session ID.
func (s *Session) ShortID() string {
	if len(s.ID) >= 8 {
		return s.ID[:8]
	}
	return s.ID
}

// persistedState is the JSON-serializable form of the registry.
type persistedState struct {
	Version  int                `json:"version"`
	Sessions map[string]Session `json:"sessions"`
}

const stateVersion = 1

// Registry manages sessions in-memory and on disk.
type Registry struct {
	mu        sync.RWMutex
	sessions  map[string]*Session // keyed by session UUID
	byRepo    map[string]*Session // secondary index by repo path
	statePath string
}

// New creates a new Registry that persists state to statePath.
func New(statePath string) *Registry {
	return &Registry{
		sessions:  make(map[string]*Session),
		byRepo:    make(map[string]*Session),
		statePath: statePath,
	}
}

// Load loads state from disk. If the file doesn't exist, returns an empty registry.
func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse state file: %w", err)
	}

	for _, s := range state.Sessions {
		sess := s // copy
		r.sessions[sess.ID] = &sess
		r.byRepo[sess.Repo] = &sess
	}
	return nil
}

// save writes registry state to disk atomically. Must be called with lock held.
func (r *Registry) save() error {
	state := persistedState{
		Version:  stateVersion,
		Sessions: make(map[string]Session, len(r.sessions)),
	}
	for id, s := range r.sessions {
		state.Sessions[id] = *s
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	dir := filepath.Dir(r.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".state-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close state: %w", err)
	}
	return os.Rename(tmpPath, r.statePath)
}

// Add adds a session to the registry and persists it.
func (r *Registry) Add(s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sessions[s.ID] = s
	r.byRepo[s.Repo] = s
	return r.save()
}

// Update updates an existing session and persists it.
func (r *Registry) Update(s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.sessions[s.ID]
	if !ok {
		return fmt.Errorf("session %s not found", s.ID)
	}
	// Update repo index if repo changed (shouldn't happen but be safe)
	if existing.Repo != s.Repo {
		delete(r.byRepo, existing.Repo)
		r.byRepo[s.Repo] = s
	}
	r.sessions[s.ID] = s
	return r.save()
}

// Remove removes a session from the registry and persists.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return nil
	}
	delete(r.sessions, id)
	delete(r.byRepo, s.Repo)
	return r.save()
}

// GetByID looks up a session by its UUID. Returns nil if not found.
func (r *Registry) GetByID(id string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.sessions[id]
	if s == nil {
		return nil
	}
	copy := *s
	return &copy
}

// GetByRepo looks up a session by its canonical repo path. Returns nil if not found.
func (r *Registry) GetByRepo(repo string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.byRepo[repo]
	if s == nil {
		return nil
	}
	copy := *s
	return &copy
}

// GetByPrefix looks up a session by an ID prefix.
// Returns nil if no match, or an error if ambiguous.
func (r *Registry) GetByPrefix(prefix string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matches []*Session
	for id, s := range r.sessions {
		if len(id) >= len(prefix) && id[:len(prefix)] == prefix {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		copy := *matches[0]
		return &copy, nil
	default:
		return nil, fmt.Errorf("ambiguous session ID prefix %q: matches %d sessions", prefix, len(matches))
	}
}

// List returns all sessions as a slice.
func (r *Registry) List() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		copy := *s
		result = append(result, &copy)
	}
	return result
}

// UpdateStatus updates the status (and optional time fields) of a session in place.
func (r *Registry) UpdateStatus(id string, status Status, startedAt, stoppedAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return fmt.Errorf("session %s not found", id)
	}
	s.Status = status
	if startedAt != nil {
		s.StartedAt = startedAt
	}
	if stoppedAt != nil {
		s.StoppedAt = stoppedAt
	}
	return r.save()
}
