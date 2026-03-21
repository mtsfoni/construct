// Package auth manages credential storage and injection for construct sessions.
//
// Credentials are stored as .env files under a state directory and bind-mounted
// into session containers. They are never passed as Docker env vars.
package auth

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/construct-run/construct/internal/slug"
)

// Store manages credential files on disk.
type Store struct {
	stateDir string // top-level state dir, e.g. ~/.config/construct/
}

// NewStore creates a new credential store rooted at stateDir.
func NewStore(stateDir string) *Store {
	return &Store{stateDir: stateDir}
}

// ProviderFromKey derives a provider name from a credential key.
// The provider is the first word before the first '_', lowercased.
// If the key has no underscores, the entire key lowercased is the provider.
func ProviderFromKey(key string) string {
	idx := strings.IndexByte(key, '_')
	if idx < 0 {
		return strings.ToLower(key)
	}
	return strings.ToLower(key[:idx])
}

// globalDir returns the path to the global credentials directory.
func (s *Store) globalDir() string {
	return filepath.Join(s.stateDir, "credentials", "global")
}

// folderDir returns the path to the per-folder credentials directory for the given folder path.
func (s *Store) folderDir(folderPath string) string {
	sl := slug.FromPath(folderPath)
	return filepath.Join(s.stateDir, "credentials", "folders", sl)
}

// envFilePath returns the path to the .env file for a given key and scope.
// If folderPath is empty, returns the global .env file path.
func (s *Store) envFilePath(key, folderPath string) string {
	provider := ProviderFromKey(key)
	filename := provider + ".env"
	if folderPath == "" {
		return filepath.Join(s.globalDir(), filename)
	}
	return filepath.Join(s.folderDir(folderPath), filename)
}

// Set stores a credential key=value in the specified scope.
// If folderPath is empty, the credential is stored globally.
func (s *Store) Set(key, value, folderPath string) error {
	path := s.envFilePath(key, folderPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}

	// Read existing entries, replace or append
	entries, err := readEnvFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read env file: %w", err)
	}

	found := false
	for i, e := range entries {
		if e.key == key {
			entries[i].value = value
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, envEntry{key: key, value: value})
	}

	return writeEnvFileAtomic(path, entries)
}

// Unset removes a credential key from the specified scope.
// If folderPath is empty, the global credential is removed.
func (s *Store) Unset(key, folderPath string) error {
	path := s.envFilePath(key, folderPath)

	entries, err := readEnvFile(path)
	if os.IsNotExist(err) {
		return nil // nothing to remove
	}
	if err != nil {
		return fmt.Errorf("read env file: %w", err)
	}

	newEntries := entries[:0]
	for _, e := range entries {
		if e.key != key {
			newEntries = append(newEntries, e)
		}
	}

	if len(newEntries) == 0 {
		return os.Remove(path) // clean up empty file
	}
	return writeEnvFileAtomic(path, newEntries)
}

// Credential represents a stored credential with its scope.
type Credential struct {
	Key         string
	Scope       string // "global" or "folder"
	MaskedValue string // always "****"
}

// List returns all credentials in the specified scope.
// If folderPath is non-empty, returns both global and folder-specific credentials.
func (s *Store) List(folderPath string) ([]Credential, error) {
	var creds []Credential

	// Always include global credentials
	globalEntries, err := readAllEnvFiles(s.globalDir())
	if err != nil {
		return nil, fmt.Errorf("read global credentials: %w", err)
	}
	for _, e := range globalEntries {
		creds = append(creds, Credential{
			Key:         e.key,
			Scope:       "global",
			MaskedValue: "****",
		})
	}

	// Include folder credentials if requested
	if folderPath != "" {
		folderEntries, err := readAllEnvFiles(s.folderDir(folderPath))
		if err != nil {
			return nil, fmt.Errorf("read folder credentials: %w", err)
		}
		for _, e := range folderEntries {
			creds = append(creds, Credential{
				Key:         e.key,
				Scope:       "folder",
				MaskedValue: "****",
			})
		}
	}

	return creds, nil
}

// EnsureFolderDir creates the per-folder credentials directory for a folder
// if it does not already exist. Called at session start to ensure the
// bind mount source is always valid.
func (s *Store) EnsureFolderDir(folderPath string) error {
	dir := s.folderDir(folderPath)
	return os.MkdirAll(dir, 0o700)
}

// GlobalDir returns the global credentials directory path.
func (s *Store) GlobalDir() string {
	return s.globalDir()
}

// FolderDir returns the per-folder credentials directory path for a folder.
func (s *Store) FolderDir(folderPath string) string {
	return s.folderDir(folderPath)
}

// envEntry is a key=value pair in an .env file.
type envEntry struct {
	key   string
	value string
}

// readEnvFile reads all key=value entries from a .env file.
func readEnvFile(path string) ([]envEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []envEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		entries = append(entries, envEntry{
			key:   line[:idx],
			value: line[idx+1:],
		})
	}
	return entries, scanner.Err()
}

// readAllEnvFiles reads all .env files in a directory.
func readAllEnvFiles(dir string) ([]envEntry, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var all []envEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}
		e, err := readEnvFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		all = append(all, e...)
	}
	return all, nil
}

// writeEnvFileAtomic writes entries to path atomically.
func writeEnvFileAtomic(path string, entries []envEntry) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	for _, e := range entries {
		if _, err := fmt.Fprintf(tmp, "%s=%s\n", e.key, e.value); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write entry: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
