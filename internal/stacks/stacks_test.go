package stacks

import (
	"testing"
)

func TestImageName(t *testing.T) {
	tests := []struct {
		stack string
		want  string
	}{
		{"base", "construct-stack-base:latest"},
		{"node", "construct-stack-node:latest"},
		{"go", "construct-stack-go:latest"},
		{"python", "construct-stack-python:latest"},
		{"dotnet", "construct-stack-dotnet:latest"},
		{"dotnet-big", "construct-stack-dotnet-big:latest"},
		{"ruby", "construct-stack-ruby:latest"},
		{"base-ui", "construct-stack-base-ui:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.stack, func(t *testing.T) {
			got := ImageName(tt.stack)
			if got != tt.want {
				t.Errorf("ImageName(%q) = %q, want %q", tt.stack, got, tt.want)
			}
		})
	}
}

func TestDaemonImageName(t *testing.T) {
	want := "construct-daemon:latest"
	got := DaemonImageName()
	if got != want {
		t.Errorf("DaemonImageName() = %q, want %q", got, want)
	}
}

func TestValidStacks(t *testing.T) {
	expected := []string{"base", "node", "go", "python", "dotnet", "dotnet-big", "ruby", "base-ui"}
	for _, s := range expected {
		if !ValidStacks[s] {
			t.Errorf("ValidStacks[%q] should be true", s)
		}
	}
}

func TestExtractBuildContext_UnknownStack(t *testing.T) {
	_, err := ExtractBuildContext("nonexistent")
	if err == nil {
		t.Error("expected error for unknown stack, got nil")
	}
}
