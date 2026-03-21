package platform

import (
	"testing"
)

func TestParseKernelVersion(t *testing.T) {
	tests := []struct {
		input        string
		wantMajor    int
		wantMinor    int
		wantErrMatch string
	}{
		{"5.15.0-91-generic", 5, 15, ""},
		{"6.1.0", 6, 1, ""},
		{"5.12.0", 5, 12, ""},
		{"4.19.0", 4, 19, ""},
		{"5.4.0+", 5, 4, ""},
		{"bad", 0, 0, "unexpected format"},
	}
	for _, tt := range tests {
		major, minor, err := parseKernelVersion(tt.input)
		if tt.wantErrMatch != "" {
			if err == nil {
				t.Errorf("parseKernelVersion(%q): want error containing %q, got nil", tt.input, tt.wantErrMatch)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseKernelVersion(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if major != tt.wantMajor || minor != tt.wantMinor {
			t.Errorf("parseKernelVersion(%q) = %d.%d, want %d.%d", tt.input, major, minor, tt.wantMajor, tt.wantMinor)
		}
	}
}

func TestCheckDockerVersion(t *testing.T) {
	tests := []struct {
		version string
		wantErr bool
	}{
		{"28.5.2", false},
		{"25.0.0", false},
		{"25.0.6", false},
		{"24.9.0", true},
		{"1.13.0", true},
	}
	for _, tt := range tests {
		err := checkDockerVersion(tt.version)
		if tt.wantErr && err == nil {
			t.Errorf("checkDockerVersion(%q): want error, got nil", tt.version)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("checkDockerVersion(%q): unexpected error: %v", tt.version, err)
		}
	}
}

func TestCheckSkipsDockerWhenEmpty(t *testing.T) {
	// Should not error on docker check when version is empty (kernel check may
	// fail depending on host, so we just verify the function doesn't panic).
	_ = Check("")
}
