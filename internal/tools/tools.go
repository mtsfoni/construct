package tools

import "fmt"

// Tool defines how an AI coding tool is installed, configured, and invoked inside the agent container.
type Tool struct {
	// Name is the tool identifier used on the CLI (e.g. "copilot", "opencode").
	Name string
	// InstallCmds are shell commands run as root during image build to install the tool.
	InstallCmds []string
	// AuthEnvVars lists the environment variable names the tool needs for authentication.
	AuthEnvVars []string
	// RunCmd is the command (and arguments) used to start the tool inside the container.
	RunCmd []string
	// ExtraEnv holds additional environment variables to inject at run time (e.g. yolo flags).
	ExtraEnv map[string]string
}

var registry = map[string]*Tool{}

// register adds a Tool to the global registry; called from each tool's init().
func register(t *Tool) {
	registry[t.Name] = t
}

// Get returns the named Tool or an error if it is not registered.
func Get(name string) (*Tool, error) {
	t, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q; supported tools: copilot, opencode", name)
	}
	return t, nil
}

// All returns a slice of every registered tool name.
func All() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	return names
}
