package tools

import (
	"testing"
)

func TestInstallCommand(t *testing.T) {
	got := InstallCommand(ToolOpencode)
	want := "npm install -g opencode-ai"
	if got != want {
		t.Errorf("InstallCommand(%q) = %q, want %q", ToolOpencode, got, want)
	}
}

func TestInstallCommand_Unknown(t *testing.T) {
	got := InstallCommand("nonexistent")
	if got != "" {
		t.Errorf("InstallCommand(\"nonexistent\") = %q, want empty", got)
	}
}

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
	want := "/agent/bin/opencode"
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
