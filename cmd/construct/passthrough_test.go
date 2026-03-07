// Integration tests for the construct CLI -- pass-through args feature.
//
// These tests reuse the binary compiled by TestMain in config_test.go.
package main_test

import (
	"strings"
	"testing"
)

// TestPassthrough_DoubleDashSeparatesToolArgs verifies that args after -- do
// not cause a flag-parse error and are accepted by the binary.
func TestPassthrough_DoubleDashSeparatesToolArgs(t *testing.T) {
	home := t.TempDir()
	// Use --help so the binary exits immediately after parsing flags, without
	// attempting to connect to Docker (which would block in CI).
	out, _ := run(t, home, "", "--help", "--", "continue-session", "dead-beef-1234")
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("-- caused a flag-parse error: %s", out)
	}
}

// TestPassthrough_FlagsBeforeDoubleDash verifies that construct flags before --
// are still parsed correctly when pass-through args follow.
func TestPassthrough_FlagsBeforeDoubleDash(t *testing.T) {
	home := t.TempDir()
	// Use --help so the binary exits immediately after flag parsing, without
	// attempting to connect to Docker (which would block in CI).
	out, _ := run(t, home, "", "--stack", "base", "--help", "--", "continue-session", "abc")
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

	// Use a repo with no last-used entry. qs will exit non-zero ("no previous
	// run recorded") before reaching runner.Run, so the test completes quickly
	// without attempting to connect to Docker. We only care that -- does not
	// produce a flag-parse error.
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
