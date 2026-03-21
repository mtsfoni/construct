package tools

import (
	"testing"
)

func TestInvokeCommand(t *testing.T) {
	got := InvokeCommand(ToolOpencode, 4096)
	want := []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", "4096"}
	if len(got) != len(want) {
		t.Fatalf("InvokeCommand length %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("InvokeCommand[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBinaryPath(t *testing.T) {
	got := BinaryPath(ToolOpencode)
	want := "/usr/local/bin/opencode"
	if got != want {
		t.Errorf("BinaryPath(%q) = %q, want %q", ToolOpencode, got, want)
	}
}

func TestHasWebUI(t *testing.T) {
	if !HasWebUI(ToolOpencode) {
		t.Error("HasWebUI(opencode) should be true")
	}
	if HasWebUI("nonexistent") {
		t.Error("HasWebUI(nonexistent) should be false")
	}
}

func TestWebPort(t *testing.T) {
	if WebPort != 4096 {
		t.Errorf("WebPort = %d, want 4096", WebPort)
	}
}
