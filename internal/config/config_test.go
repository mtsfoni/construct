package config

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateAgentsMD_ContainsEssentials(t *testing.T) {
	p := AgentsParams{
		SessionID:  "abc12345",
		Repo:       "/home/alice/src/myapp",
		Tool:       "opencode",
		Stack:      "node",
		DockerMode: "none",
		Ports:      []PortMapping{{HostPort: 3000, ContainerPort: 3000}},
	}

	got := GenerateAgentsMD(p)

	checks := []string{
		"construct",
		"/home/alice/src/myapp",
		"opencode",
		"node",
		"none",
		"Container port `3000` → Host port `3000`",
		"0.0.0.0",
		"/agent/bin",
	}

	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("GenerateAgentsMD missing %q", check)
		}
	}
}

func TestGenerateAgentsMD_DindMode(t *testing.T) {
	p := AgentsParams{
		Repo:       "/tmp/proj",
		DockerMode: "dind",
	}
	got := GenerateAgentsMD(p)
	if !strings.Contains(got, "DOCKER_HOST") {
		t.Error("dind mode should mention DOCKER_HOST")
	}
}

func TestGenerateAgentsMD_DoodMode(t *testing.T) {
	p := AgentsParams{
		Repo:       "/tmp/proj",
		DockerMode: "dood",
	}
	got := GenerateAgentsMD(p)
	if !strings.Contains(got, "host Docker daemon") {
		t.Error("dood mode should mention host Docker daemon")
	}
}

func TestGenerateAgentsMD_NoPorts(t *testing.T) {
	p := AgentsParams{
		Repo:       "/tmp/proj",
		DockerMode: "none",
		Ports:      nil,
	}
	got := GenerateAgentsMD(p)
	if strings.Contains(got, "Port forwarding") {
		t.Error("should not include port forwarding section when no ports")
	}
}

func TestWriteAgentsMD(t *testing.T) {
	dir := t.TempDir()
	p := AgentsParams{
		Repo:       "/home/alice/myapp",
		DockerMode: "none",
	}
	if err := WriteAgentsMD(dir, p); err != nil {
		t.Fatalf("WriteAgentsMD: %v", err)
	}
	data, err := os.ReadFile(dir + "/construct-agents.md")
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(data) == 0 {
		t.Error("construct-agents.md should not be empty")
	}
}

func TestOpenCodeConfigDir_Default(t *testing.T) {
	// Unset XDG_CONFIG_HOME to test default behaviour
	orig := os.Getenv("XDG_CONFIG_HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	defer func() {
		if orig != "" {
			os.Setenv("XDG_CONFIG_HOME", orig)
		}
	}()

	got := OpenCodeConfigDir()
	if !strings.HasSuffix(got, ".config/opencode") {
		t.Errorf("OpenCodeConfigDir() = %q, expected to end with .config/opencode", got)
	}
}

func TestOpenCodeConfigDir_XDG(t *testing.T) {
	orig := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", "/custom/config")
	defer func() {
		if orig == "" {
			os.Unsetenv("XDG_CONFIG_HOME")
		} else {
			os.Setenv("XDG_CONFIG_HOME", orig)
		}
	}()

	got := OpenCodeConfigDir()
	want := "/custom/config/opencode"
	if got != want {
		t.Errorf("OpenCodeConfigDir() = %q, want %q", got, want)
	}
}
