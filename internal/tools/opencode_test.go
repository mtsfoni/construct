package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpencode_NoMCPInHomeFiles(t *testing.T) {
	tool, err := Get("opencode")
	if err != nil {
		t.Fatalf("Get(\"opencode\"): %v", err)
	}
	for path, content := range tool.HomeFiles {
		if strings.Contains(path, "opencode.json") || strings.Contains(content, "playwright") {
			t.Errorf("opencode HomeFiles must not contain MCP config; found path=%q content=%q", path, content)
		}
	}
}

// TestOpencode_AuthFilesNotVolumePath verifies that opencode uses AuthFiles
// (file bind-mount) rather than AuthVolumePath (directory volume mount).
// AuthVolumePath would shadow the whole ~/.local/share/opencode/ directory and
// make opencode.db global across all repos; AuthFiles only exposes auth.json.
func TestOpencode_AuthFilesNotVolumePath(t *testing.T) {
	tool, err := Get("opencode")
	if err != nil {
		t.Fatalf("Get(\"opencode\"): %v", err)
	}
	if tool.AuthVolumePath != "" {
		t.Errorf("opencode.AuthVolumePath = %q; must be empty — use AuthFiles instead to keep opencode.db per-repo", tool.AuthVolumePath)
	}
	if len(tool.AuthFiles) == 0 {
		t.Error("opencode.AuthFiles must not be empty; auth.json must be persisted globally via a file bind-mount")
	}
}

// TestOpencode_AuthFileContainerPath verifies that the auth.json file is
// mounted at the correct XDG data path inside the container.
func TestOpencode_AuthFileContainerPath(t *testing.T) {
	tool, err := Get("opencode")
	if err != nil {
		t.Fatalf("Get(\"opencode\"): %v", err)
	}
	const want = "/home/agent/.local/share/opencode/auth.json"
	for _, af := range tool.AuthFiles {
		if af.ContainerPath == want {
			return
		}
	}
	t.Errorf("expected AuthFile with ContainerPath %q; got %v", want, tool.AuthFiles)
}

// TestOpencode_AuthFileHostPathUnderConstruct verifies that the auth.json host
// path is under ~/.construct/ so it is co-located with other construct state.
func TestOpencode_AuthFileHostPathUnderConstruct(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("UserHomeDir unavailable")
	}
	tool, err := Get("opencode")
	if err != nil {
		t.Fatalf("Get(\"opencode\"): %v", err)
	}
	constructDir := filepath.Join(home, ".construct")
	for _, af := range tool.AuthFiles {
		if af.ContainerPath == "/home/agent/.local/share/opencode/auth.json" {
			if !strings.HasPrefix(af.HostPath, constructDir) {
				t.Errorf("auth.json HostPath = %q; want a path under %s", af.HostPath, constructDir)
			}
			return
		}
	}
	t.Error("auth.json AuthFile not found")
}
