// Integration tests for the construct CLI -- pass-through args feature.
//
// These tests reuse the binary compiled by TestMain in config_test.go.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPassthrough_DoubleDashSeparatesToolArgs verifies that args after -- do
// not cause a flag-parse error and are accepted by the binary.
func TestPassthrough_DoubleDashSeparatesToolArgs(t *testing.T) {
	home := t.TempDir()
	// The binary will fail (no Docker), but must not exit with a flag-parse error.
	out, _ := run(t, home, "", "--", "continue-session", "dead-beef-1234")
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("-- caused a flag-parse error: %s", out)
	}
}

// TestPassthrough_FlagsBeforeDoubleDash verifies that construct flags before --
// are still parsed correctly when pass-through args follow.
func TestPassthrough_FlagsBeforeDoubleDash(t *testing.T) {
	home := t.TempDir()
	out, _ := run(t, home, "", "--stack", "base", "--", "continue-session", "abc")
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("flags before -- caused a parse error: %s", out)
	}
	if strings.Contains(out, "unknown stack") {
		t.Errorf("valid stack was rejected: %s", out)
	}
}

// TestPassthrough_UsageDocumentsDoubleDash verifies the usage text mentions --.
func TestPassthrough_UsageDocumentsDoubleDash(t *testing.T) {
	home := t.TempDir()
	out, _ := run(t, home, "", "--help")
	if !strings.Contains(out, "--") {
		t.Errorf("usage output does not mention --:\n%s", out)
	}
}

// TestPassthrough_QsDoubleDash verifies that qs accepts -- without a flag error.
func TestPassthrough_QsDoubleDash(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()

	// Write a last-used entry so qs doesn't fail on "no previous run".
	lastUsedDir := filepath.Join(home, ".construct")
	if err := os.MkdirAll(lastUsedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := map[string]interface{}{
		repo: map[string]interface{}{"stack": "base", "docker": "none"},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lastUsedDir, "last-used.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := run(t, home, repo, "qs", repo, "--", "continue-session", "dead-beef-1234")
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("qs -- caused a flag-parse error: %s", out)
	}
}

// TestPassthrough_QsUsageDocumentsDoubleDash verifies the qs usage text mentions --.
func TestPassthrough_QsUsageDocumentsDoubleDash(t *testing.T) {
	home := t.TempDir()
	// qs with no last-used entry exits non-zero with usage; that's fine.
	out, _ := run(t, home, "", "qs", "--help")
	if !strings.Contains(out, "--") {
		t.Errorf("qs usage output does not mention --:\n%s", out)
	}
}
