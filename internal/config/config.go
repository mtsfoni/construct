// Package config manages construct's .env credential files.
//
// Two scopes are supported:
//   - Global:    ~/.construct/.env  (all repos, all tools)
//   - Local:     <repoDir>/.construct/.env  (overrides global for one repo)
//
// The file format is KEY=VALUE, one per line, with # comments and blank lines allowed.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GlobalFile returns the path to the global env file (~/.construct/.env).
func GlobalFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".construct", ".env"), nil
}

// LocalFile returns the path to the per-repo env file inside dir.
func LocalFile(dir string) string {
	return filepath.Join(dir, ".construct", ".env")
}

// Set writes or updates key=value in the env file at path.
// Parent directories and the file itself are created with mode 0700/0600
// respectively if they do not already exist.
// Comments and unrelated lines are preserved.
func Set(path, key, value string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}

	newLine := key + "=" + value
	replaced := false
	for i, line := range lines {
		k, _, ok := parseLine(line)
		if ok && k == key {
			lines[i] = newLine
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}

	return writeLines(path, lines)
}

// Unset removes key from the env file at path.
// No-ops if the file does not exist or the key is not present.
func Unset(path, key string) error {
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	filtered := lines[:0]
	for _, line := range lines {
		k, _, ok := parseLine(line)
		if ok && k == key {
			continue
		}
		filtered = append(filtered, line)
	}

	return writeLines(path, filtered)
}

// List reads all key=value pairs from the env file at path.
// Returns an empty map (and no error) if the file does not exist.
// Comments and blank lines are ignored.
func List(path string) (map[string]string, error) {
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	m := make(map[string]string)
	for _, line := range lines {
		k, v, ok := parseLine(line)
		if ok {
			m[k] = v
		}
	}
	return m, nil
}

// parseLine parses a single line into key and value.
// Returns ok=false for blank lines and comments.
func parseLine(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	k, v, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	// Strip surrounding quotes (single or double).
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') ||
			(v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	return k, v, true
}

// readLines returns the lines of path (without trailing newlines).
// Returns an empty slice if the file does not exist.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// writeLines writes lines (joined by newlines) to path atomically.
// Creates parent dirs with 0700 and the file itself with 0600.
func writeLines(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	// Write to a temp file next to the target for an atomic rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("rename to target: %w", err)
	}
	return nil
}
