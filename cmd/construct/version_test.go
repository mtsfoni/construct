// Integration tests for the construct CLI --version flag.
//
// These tests reuse the binary compiled by TestMain in config_test.go.
package main_test

import (
	"strings"
	"testing"
)

// TestVersionFlag_PrintsVersion verifies that --version prints a version line
// and exits zero.
func TestVersionFlag_PrintsVersion(t *testing.T) {
	home := t.TempDir()
	out, code := run(t, home, "", "--version")
	if code != 0 {
		t.Errorf("expected exit code 0; got %d\noutput: %s", code, out)
	}
	if !strings.HasPrefix(out, "construct ") {
		t.Errorf("expected output to start with \"construct \"; got: %s", out)
	}
}

// TestVersionFlag_ShortForm verifies that -version (single dash) also works.
func TestVersionFlag_ShortForm(t *testing.T) {
	home := t.TempDir()
	out, code := run(t, home, "", "-version")
	if code != 0 {
		t.Errorf("expected exit code 0; got %d\noutput: %s", code, out)
	}
	if !strings.HasPrefix(out, "construct ") {
		t.Errorf("expected output to start with \"construct \"; got: %s", out)
	}
}

// TestVersionFlag_DevWhenUnset verifies that a binary built without ldflags
// reports "construct dev".
func TestVersionFlag_DevWhenUnset(t *testing.T) {
	home := t.TempDir()
	out, code := run(t, home, "", "--version")
	if code != 0 {
		t.Fatalf("expected exit code 0; got %d", code)
	}
	// The TestMain binary is built without -X main.version, so it should say "dev".
	if !strings.Contains(out, "dev") {
		t.Errorf("expected \"dev\" in version output when built without ldflags; got: %s", out)
	}
}

// TestVersionFlag_AppearsInUsage verifies --version is documented in the
// usage output.
func TestVersionFlag_AppearsInUsage(t *testing.T) {
	home := t.TempDir()
	out, _ := run(t, home, "", "--help")
	if !strings.Contains(out, "version") {
		t.Errorf("expected --version to appear in usage output; got:\n%s", out)
	}
}
