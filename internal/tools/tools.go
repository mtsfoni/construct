// Package tools describes the supported agent tools: invoke commands, web UI
// ports, and related constants. opencode is baked into the stack image and
// does not require a runtime install step.
package tools

// Tool names.
const (
	ToolOpencode = "opencode"
)

// DefaultTool is the tool used when none is specified.
const DefaultTool = ToolOpencode

// WebPort is the port opencode binds its web server to inside the container.
const WebPort = 4096

// InvokeCommand returns the command used to start the tool in yolo mode.
// port is the container-side port the tool should bind to.
func InvokeCommand(tool string, port int) []string {
	switch tool {
	case ToolOpencode:
		return []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", itoa(port)}
	default:
		return nil
	}
}

// BinaryPath returns the expected path of the tool binary inside the image.
func BinaryPath(tool string) string {
	switch tool {
	case ToolOpencode:
		return "/usr/local/bin/opencode"
	default:
		return ""
	}
}

// HasWebUI returns true if the tool exposes a web interface.
func HasWebUI(tool string) bool {
	switch tool {
	case ToolOpencode:
		return true
	default:
		return false
	}
}

// LogPath returns the path inside the container where the tool writes its logs,
// or an empty string if the tool does not write to a log file.
// Used by daemon reconciliation to re-attach log streaming after a restart.
func LogPath(tool string) string {
	switch tool {
	case ToolOpencode:
		return "/agent/home/.local/share/opencode/opencode.log"
	default:
		return ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
