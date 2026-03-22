package imagespec_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/construct-run/construct/internal/imagespec"
)

// --- helpers ---

func mustSpec(t *testing.T, fsys fs.FS) *imagespec.EmbeddedBuildSpec {
	t.Helper()
	spec, err := imagespec.NewEmbeddedBuildSpec(fsys)
	if err != nil {
		t.Fatalf("NewEmbeddedBuildSpec: %v", err)
	}
	return spec
}

// --- tests ---

func TestEmbeddedBuildSpec_Label_Prefix(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM debian:bookworm-slim\n")},
	}
	spec := mustSpec(t, fsys)
	label := spec.Label()
	if len(label) < 9 || label[:9] != "embedded:" {
		t.Errorf("Label() = %q, want prefix %q", label, "embedded:")
	}
}

func TestEmbeddedBuildSpec_Label_Stable(t *testing.T) {
	// Same content → same label on repeated calls.
	fsys := fstest.MapFS{
		"Dockerfile":    {Data: []byte("FROM debian:bookworm-slim\n")},
		"entrypoint.sh": {Data: []byte("#!/bin/bash\nexec \"$@\"\n")},
	}
	a := mustSpec(t, fsys)
	b := mustSpec(t, fsys)
	if a.Label() != b.Label() {
		t.Errorf("repeated calls: got %q and %q, want same", a.Label(), b.Label())
	}
}

func TestEmbeddedBuildSpec_Label_ContentChange(t *testing.T) {
	// Changing file contents changes the label.
	before := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM debian:bookworm-slim\n")},
	}
	after := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM debian:bookworm-slim\nRUN apt-get update\n")},
	}
	if mustSpec(t, before).Label() == mustSpec(t, after).Label() {
		t.Error("content change: labels are equal, want different")
	}
}

func TestEmbeddedBuildSpec_Label_AddFile(t *testing.T) {
	// Adding a file changes the label.
	before := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM debian:bookworm-slim\n")},
	}
	after := fstest.MapFS{
		"Dockerfile":    {Data: []byte("FROM debian:bookworm-slim\n")},
		"entrypoint.sh": {Data: []byte("#!/bin/bash\n")},
	}
	if mustSpec(t, before).Label() == mustSpec(t, after).Label() {
		t.Error("add file: labels are equal, want different")
	}
}

func TestEmbeddedBuildSpec_Label_PathChange(t *testing.T) {
	// Renaming a file (same content, different path) changes the label.
	before := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM debian:bookworm-slim\n")},
	}
	after := fstest.MapFS{
		"dockerfile": {Data: []byte("FROM debian:bookworm-slim\n")},
	}
	if mustSpec(t, before).Label() == mustSpec(t, after).Label() {
		t.Error("path change: labels are equal, want different")
	}
}

func TestEmbeddedBuildSpec_Implements_Spec(t *testing.T) {
	// Compile-time check that EmbeddedBuildSpec satisfies the Spec interface.
	var _ imagespec.Spec = (*imagespec.EmbeddedBuildSpec)(nil)
}
