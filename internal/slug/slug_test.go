package slug

import (
	"strings"
	"testing"
)

func TestFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "simple path",
			path: "/home/alice/src/myapp",
			want: "home_alice_src_myapp",
		},
		{
			name: "tmp path",
			path: "/tmp/test",
			want: "tmp_test",
		},
		{
			name: "deep path",
			path: "/home/alice/src/very/deep/nested/path",
			want: "home_alice_src_very_deep_nested_path",
		},
		{
			name: "root",
			path: "/",
			want: "",
		},
		{
			name: "single segment",
			path: "/mydir",
			want: "mydir",
		},
		{
			name: "truncation at 200 chars",
			path: "/" + strings.Repeat("a", 250),
			want: strings.Repeat("a", 200),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromPath(tt.path)
			if got != tt.want {
				t.Errorf("FromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
			if len(got) > maxLen {
				t.Errorf("FromPath(%q) length %d exceeds max %d", tt.path, len(got), maxLen)
			}
		})
	}
}

func TestFromPath_TruncationExact(t *testing.T) {
	// Path that produces exactly 200 chars - no truncation
	path := "/" + strings.Repeat("x", 200)
	got := FromPath(path)
	if len(got) != 200 {
		t.Errorf("expected length 200, got %d", len(got))
	}
}

func TestFromPath_TruncationOver(t *testing.T) {
	// Path that produces 201 chars - should truncate to 200
	path := "/" + strings.Repeat("x", 201)
	got := FromPath(path)
	if len(got) != 200 {
		t.Errorf("expected length 200, got %d", len(got))
	}
}
