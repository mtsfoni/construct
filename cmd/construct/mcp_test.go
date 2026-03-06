// Integration tests for the construct CLI --mcp flag.
//
// These tests reuse the binary compiled by TestMain in config_test.go.
package main_test

import (
	"strings"
	"testing"
)

// TestMCPFlag_AppearsInUsage verifies --mcp is documented in the usage output.
func TestMCPFlag_AppearsInUsage(t *testing.T) {
	home := t.TempDir()
	out, _ := run(t, home, "", "--help")
	if !strings.Contains(out, "mcp") {
		t.Errorf("expected --mcp to appear in usage output; got:\n%s", out)
	}
}
