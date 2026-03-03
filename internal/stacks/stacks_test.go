package stacks

import (
	"strings"
	"testing"
)

func TestIsValid_KnownStacks(t *testing.T) {
	known := []string{"base", "dotnet", "dotnet-big", "dotnet-ui", "go", "ui"}
	for _, s := range known {
		if !IsValid(s) {
			t.Errorf("IsValid(%q) = false, want true", s)
		}
	}
}

func TestIsValid_RemovedStacks(t *testing.T) {
	removed := []string{"node", "python"}
	for _, s := range removed {
		if IsValid(s) {
			t.Errorf("IsValid(%q) = true, want false (stack was merged into base)", s)
		}
	}
}

func TestIsValid_UnknownStack(t *testing.T) {
	if IsValid("rust") {
		t.Error("IsValid(\"rust\") = true, want false")
	}
}

func TestAll_ContainsUI(t *testing.T) {
	all := All()
	for _, s := range all {
		if s == "ui" {
			return
		}
	}
	t.Errorf("All() = %v, want it to contain \"ui\"", all)
}

func TestImageName(t *testing.T) {
	cases := []struct {
		stack string
		want  string
	}{
		{"base", "construct-base"},
		{"go", "construct-go"},
		{"ui", "construct-ui"},
	}
	for _, c := range cases {
		if got := ImageName(c.stack); got != c.want {
			t.Errorf("ImageName(%q) = %q, want %q", c.stack, got, c.want)
		}
	}
}

func TestEnsureBuilt_UnknownStackError(t *testing.T) {
	err := EnsureBuilt("rust", false)
	if err == nil {
		t.Fatal("expected error for unknown stack, got nil")
	}
	if !strings.Contains(err.Error(), "rust") {
		t.Errorf("error should mention stack name, got: %v", err)
	}
	// All valid stack names should appear in the error message.
	for _, s := range validStacks {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error should list valid stack %q, got: %v", s, err)
		}
	}
}

func TestStackDeps_UIHasBase(t *testing.T) {
	deps, ok := stackDeps["ui"]
	if !ok {
		t.Fatal("stackDeps[\"ui\"] not set")
	}
	if len(deps) != 1 || deps[0] != "base" {
		t.Errorf("stackDeps[\"ui\"] = %v, want [\"base\"]", deps)
	}
}

func TestEmbeddedDockerfiles_BaseContent(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/base/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded base Dockerfile: %v", err)
	}
	content := string(data)

	checks := []struct {
		desc    string
		snippet string
	}{
		{"includes Node.js install", "nodesource.com/setup_20.x"},
		{"includes python3", "python3"},
		{"includes python3-pip", "python3-pip"},
		{"creates agent user", "useradd"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.snippet) {
			t.Errorf("base Dockerfile: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

func TestEmbeddedDockerfiles_UIExists(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/ui/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile for ui not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("embedded Dockerfile for ui is empty")
	}
}

func TestEmbeddedDockerfiles_UIContent(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/ui/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	content := string(data)

	checks := []struct {
		desc    string
		snippet string
	}{
		{"extends construct-base", "FROM construct-base"},
		{"sets fixed browser path env var", "PLAYWRIGHT_BROWSERS_PATH=/ms-playwright"},
		{"installs Chromium via playwright cli", "playwright install"},
		{"installs @playwright/mcp globally", "npm install -g @playwright/mcp"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.snippet) {
			t.Errorf("ui Dockerfile: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

func TestEmbeddedDockerfiles_DotnetBigExists(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-big/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile for dotnet-big not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("embedded Dockerfile for dotnet-big is empty")
	}
}

func TestEmbeddedDockerfiles_DotnetBigContent(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-big/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded dotnet-big Dockerfile: %v", err)
	}
	content := string(data)

	checks := []struct {
		desc    string
		snippet string
	}{
		{"extends construct-base", "FROM construct-base"},
		{"installs .NET 8 SDK", "--channel 8.0"},
		{"installs .NET 9 SDK", "--channel 9.0"},
		{"installs .NET 10 SDK", "--channel 10.0"},
		{"sets DOTNET_ROOT", "DOTNET_ROOT=/usr/share/dotnet"},
		{"opts out of telemetry", "DOTNET_CLI_TELEMETRY_OPTOUT=1"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.snippet) {
			t.Errorf("dotnet-big Dockerfile: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

func TestStackDeps_DotnetBigUIHasBaseAndDotnetBig(t *testing.T) {
	deps, ok := stackDeps["dotnet-big-ui"]
	if !ok {
		t.Fatal("stackDeps[\"dotnet-big-ui\"] not set")
	}
	if len(deps) != 2 || deps[0] != "base" || deps[1] != "dotnet-big" {
		t.Errorf("stackDeps[\"dotnet-big-ui\"] = %v, want [\"base\", \"dotnet-big\"]", deps)
	}
}

func TestEmbeddedDockerfiles_DotnetBigUIExists(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-big-ui/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile for dotnet-big-ui not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("embedded Dockerfile for dotnet-big-ui is empty")
	}
}

func TestEmbeddedDockerfiles_DotnetBigUIContent(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-big-ui/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded dotnet-big-ui Dockerfile: %v", err)
	}
	content := string(data)

	checks := []struct {
		desc    string
		snippet string
	}{
		{"extends construct-dotnet-big", "FROM construct-dotnet-big"},
		{"sets fixed browser path env var", "PLAYWRIGHT_BROWSERS_PATH=/ms-playwright"},
		{"installs Chromium via playwright cli", "playwright install"},
		{"installs @playwright/mcp globally", "npm install -g @playwright/mcp"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.snippet) {
			t.Errorf("dotnet-big-ui Dockerfile: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

func TestStackDeps_DotnetUIHasBaseAndDotnet(t *testing.T) {
	deps, ok := stackDeps["dotnet-ui"]
	if !ok {
		t.Fatal("stackDeps[\"dotnet-ui\"] not set")
	}
	if len(deps) != 2 || deps[0] != "base" || deps[1] != "dotnet" {
		t.Errorf("stackDeps[\"dotnet-ui\"] = %v, want [\"base\", \"dotnet\"]", deps)
	}
}

func TestEmbeddedDockerfiles_DotnetUIExists(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-ui/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile for dotnet-ui not found: %v", err)
	}
	if len(data) == 0 {
		t.Error("embedded Dockerfile for dotnet-ui is empty")
	}
}

func TestEmbeddedDockerfiles_DotnetUIContent(t *testing.T) {
	data, err := dockerfiles.ReadFile("dockerfiles/dotnet-ui/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded dotnet-ui Dockerfile: %v", err)
	}
	content := string(data)

	checks := []struct {
		desc    string
		snippet string
	}{
		{"extends construct-dotnet", "FROM construct-dotnet"},
		{"sets fixed browser path env var", "PLAYWRIGHT_BROWSERS_PATH=/ms-playwright"},
		{"installs Chromium via playwright cli", "playwright install"},
		{"installs @playwright/mcp globally", "npm install -g @playwright/mcp"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.snippet) {
			t.Errorf("dotnet-ui Dockerfile: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}
