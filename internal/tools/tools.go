package tools

import (
	"fmt"
	"sort"
	"strings"
)

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
