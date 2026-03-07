package tools

import (
	"fmt"
	"sort"
	"strings"
)

// AuthFile describes a single host file that should be bind-mounted into the
// container at a fixed path, typically used to persist a tool's auth tokens
// (e.g. opencode's auth.json) across repos and --reset without mounting a
// whole directory from a named volume.
type AuthFile struct {
	// HostPath is the absolute path on the host (e.g. "~/.construct/opencode/auth.json").
	// Tilde expansion is NOT performed here; callers must expand it before use.
	HostPath string
	// ContainerPath is the absolute path inside the container where the file is mounted.
	ContainerPath string
}

// Tool defines how an AI coding tool is installed, configured, and invoked inside the agent container.
type Tool struct {
	// Name is the tool identifier used in image names and volume names (e.g. "opencode").
	Name string
	// InstallCmds are shell commands run as root during image build to install the tool.
	InstallCmds []string
	// AuthEnvVars lists the environment variable names the tool needs for authentication.
	AuthEnvVars []string
	// RunCmd is the command (and arguments) used to start the tool inside the container.
	RunCmd []string
	// ExtraEnv holds additional environment variables to inject at run time (e.g. yolo flags).
	ExtraEnv map[string]string
	// HomeFiles maps paths relative to /home/agent to file contents that should be
	// written into the home volume on first initialisation (e.g. tool config files).
	HomeFiles map[string]string
	// AuthVolumePath is the absolute path inside the container where the tool stores
	// its OAuth tokens and persistent auth state (e.g. "/home/agent/.local/share/opencode").
	// When non-empty, a global named Docker volume is mounted at this path so that
	// auth state is shared across all repos and survives --reset.
	AuthVolumePath string
	// AuthFiles is a list of individual host files to bind-mount into the container
	// for persisting auth state globally without mounting an entire directory via a
	// named volume. This allows the session database (opencode.db) to remain in the
	// per-repo home volume while only the auth token file is shared globally.
	// The runner calls ensureAuthFile for each entry to create the host file if absent.
	AuthFiles []AuthFile
}

var registry = map[string]*Tool{}

// register adds a Tool to the global registry; called from each tool's init().
func register(t *Tool) {
	registry[t.Name] = t
}

// Opencode returns the opencode tool definition.
func Opencode() *Tool {
	return registry["opencode"]
}

// Get returns the named Tool or an error if it is not registered.
func Get(name string) (*Tool, error) {
	t, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q; supported tools: %s", name, knownTools())
	}
	return t, nil
}

// knownTools returns a sorted, comma-separated list of registered tool names.
func knownTools() string {
	names := All()
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// All returns a slice of every registered tool name.
func All() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	return names
}
