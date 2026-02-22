package stacks

import (
	"strings"
	"testing"
)

func TestIsValid_KnownStacks(t *testing.T) {
	known := []string{"base", "node", "dotnet", "python", "go", "ui"}
	for _, s := range known {
		if !IsValid(s) {
			t.Errorf("IsValid(%q) = false, want true", s)
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
		{"node", "construct-node"},
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

func TestStackDeps_UIHasNodeAndBase(t *testing.T) {
	deps, ok := stackDeps["ui"]
	if !ok {
		t.Fatal("stackDeps[\"ui\"] not set")
	}
	want := map[string]bool{"base": true, "node": true}
	for _, d := range deps {
		if !want[d] {
			t.Errorf("unexpected dep %q in stackDeps[\"ui\"]", d)
		}
		delete(want, d)
	}
	for missing := range want {
		t.Errorf("expected dep %q in stackDeps[\"ui\"] but it was absent", missing)
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
		{"extends construct-node", "FROM construct-node"},
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
