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

// TestPortFlag_MultipleAllowed verifies that the flag can be repeated without
// the binary exiting with a parse error.
func TestPortFlag_MultipleAllowed(t *testing.T) {
	home := t.TempDir()
	// --stack is not required to be valid at parse time; we just want to confirm
	// the flag parser accepts multiple --port values without a flag syntax error.
	out, code := run(t, home, "", "--port", "3000", "--port", "8080")
	// Will exit non-zero (no Docker), but must not be a flag parse error.
	_ = code
	if strings.Contains(out, "flag provided but not defined") {
		t.Errorf("multiple --port values caused a flag parse error: %s", out)
	}
}
