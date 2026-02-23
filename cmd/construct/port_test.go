// Integration tests for the construct CLI --port flag.
//
// These tests reuse the binary compiled by TestMain in config_test.go.
package main_test

import (
	"strings"
	"testing"
)

// TestPortFlag_AppearsInUsage verifies --port is documented in the usage output.
func TestPortFlag_AppearsInUsage(t *testing.T) {
	home := t.TempDir()
	out, _ := run(t, home, "", "--help")
	if !strings.Contains(out, "port") {
		t.Errorf("expected --port to appear in usage output; got:\n%s", out)
	}
}

// TestPortFlag_MissingTool_StillErrors verifies that --port does not suppress
// the --tool required error.
func TestPortFlag_MissingTool_StillErrors(t *testing.T) {
	home := t.TempDir()
	_, code := run(t, home, "", "--port", "3000")
	if code == 0 {
		t.Error("expected non-zero exit when --tool is missing, even with --port")
	}
}

// TestPortFlag_MultipleAllowed verifies that the flag can be repeated without
// the binary exiting with a parse error.
func TestPortFlag_MultipleAllowed(t *testing.T) {
	home := t.TempDir()
	// --tool is still required; we just want to confirm the flag parser accepts
	// multiple --port values (error should be about missing --tool, not flag syntax).
	out, code := run(t, home, "", "--port", "3000", "--port", "8080")
	if code == 0 {
		t.Error("expected non-zero exit (missing --tool), got success")
	}
	// The error must be about --tool, not about an unrecognised flag.
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("multiple --port values caused a flag parse error: %s", out)
	}
}
