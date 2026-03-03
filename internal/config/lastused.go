package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LastUsed holds the tool and stack that were last used for a repository,
// plus any optional --mcp, --port, and --docker flags.
type LastUsed struct {
	Tool       string   `json:"tool"`
	Stack      string   `json:"stack"`
	MCP        bool     `json:"mcp,omitempty"`
	Ports      []string `json:"ports,omitempty"`
	DockerMode string   `json:"docker,omitempty"`
}

// lastUsedFile returns the path to ~/.construct/last-used.json.
func lastUsedFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".construct", "last-used.json"), nil
}

// SaveLastUsed records the tool, stack, mcp flag, ports, and docker mode used for repoPath.
func SaveLastUsed(repoPath, tool, stack string, mcp bool, ports []string, dockerMode string) error {
	path, err := lastUsedFile()
	if err != nil {
		return err
	}

	m, err := readLastUsedMap(path)
	if err != nil {
		return err
	}

	m[repoPath] = LastUsed{Tool: tool, Stack: stack, MCP: mcp, Ports: ports, DockerMode: dockerMode}
	return writeLastUsedMap(path, m)
}

// LoadLastUsed returns the last-used tool and stack for repoPath.
// Returns a zero LastUsed and no error if no entry exists.
func LoadLastUsed(repoPath string) (LastUsed, error) {
	path, err := lastUsedFile()
	if err != nil {
		return LastUsed{}, err
	}

	m, err := readLastUsedMap(path)
	if err != nil {
		return LastUsed{}, err
	}

	return m[repoPath], nil
}

func readLastUsedMap(path string) (map[string]LastUsed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]LastUsed{}, nil
		}
		return nil, err
	}

	m := map[string]LastUsed{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeLastUsedMap(path string, m map[string]LastUsed) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	return nil
}
