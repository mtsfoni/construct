package network

import (
	"testing"
)

func TestSessionNetworkName(t *testing.T) {
	got := SessionNetworkName("a1b2c3d4")
	want := "construct-net-a1b2c3d4"
	if got != want {
		t.Errorf("SessionNetworkName = %q, want %q", got, want)
	}
}

func TestDindContainerName(t *testing.T) {
	got := DindContainerName("a1b2c3d4")
	want := "construct-dind-a1b2c3d4"
	if got != want {
		t.Errorf("DindContainerName = %q, want %q", got, want)
	}
}

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		spec     string
		wantHost int
		wantCont int
		wantErr  bool
	}{
		{"3000:3000", 3000, 3000, false},
		{"8080:9000", 8080, 9000, false},
		{"8080", 0, 8080, false},
		{"invalid", 0, 0, true},
		{"", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			h, c, err := ParsePortSpec(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParsePortSpec(%q) expected error, got nil", tt.spec)
				}
				return
			}
			if err != nil {
				t.Errorf("ParsePortSpec(%q) unexpected error: %v", tt.spec, err)
				return
			}
			if h != tt.wantHost {
				t.Errorf("ParsePortSpec(%q) host = %d, want %d", tt.spec, h, tt.wantHost)
			}
			if c != tt.wantCont {
				t.Errorf("ParsePortSpec(%q) container = %d, want %d", tt.spec, c, tt.wantCont)
			}
		})
	}
}

func TestFindFreePort(t *testing.T) {
	// Should find a free port in the high-numbered range
	port, err := FindFreePort(50000)
	if err != nil {
		t.Fatalf("FindFreePort: %v", err)
	}
	if port < 50000 {
		t.Errorf("port %d should be >= 50000", port)
	}
}
