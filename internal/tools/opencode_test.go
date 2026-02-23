package tools

import (
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

func TestOpencode_HasAuthVolumePath(t *testing.T) {
	tool, err := Get("opencode")
	if err != nil {
		t.Fatalf("Get(\"opencode\"): %v", err)
	}
	if tool.AuthVolumePath == "" {
		t.Error("opencode AuthVolumePath must not be empty")
	}
}
