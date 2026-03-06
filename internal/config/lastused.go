package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LastUsed holds the stack that was last used for a repository,
// plus any optional --mcp, --port, --docker, --serve-port, and --client flags.
type LastUsed struct {
	Stack      string   `json:"stack"`
	MCP        bool     `json:"mcp,omitempty"`
	Ports      []string `json:"ports,omitempty"`
	DockerMode string   `json:"docker,omitempty"`
	// ServePort is the port used for the opencode HTTP server (opencode serve).
	// Zero means the field was absent in an older entry; callers must default to 4096.
	ServePort int `json:"serve_port,omitempty"`
	// Client is the local client to use when connecting to the opencode server.
	// Empty means auto-detect (try opencode attach, fall back to browser).
	// Valid non-empty values: "tui", "web".
	Client string `json:"client,omitempty"`
}

// lastUsedFile returns the path to ~/.construct/last-used.json.
func lastUsedFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".construct", "last-used.json"), nil
}

// SaveLastUsed records the stack, mcp flag, ports, docker mode, serve port, and client used for repoPath.
func SaveLastUsed(repoPath, stack string, mcp bool, ports []string, dockerMode string, servePort int, client string) error {
	path, err := lastUsedFile()
	if err != nil {
		return err
	}

	m, err := readLastUsedMap(path)
	if err != nil {
		return err
	}

	m[repoPath] = LastUsed{Stack: stack, MCP: mcp, Ports: ports, DockerMode: dockerMode, ServePort: servePort, Client: client}
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
