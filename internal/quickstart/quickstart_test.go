package quickstart

import (
	"errors"
	"testing"
)

func TestStore_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	r := Record{
		Folder:     "/home/alice/src/myapp",
		Tool:       "opencode",
		Stack:      "node",
		DockerMode: "none",
		Ports:      []string{"3000:3000", "8080"},
	}

	if err := store.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load("/home/alice/src/myapp")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Folder != r.Folder {
		t.Errorf("Folder = %q, want %q", got.Folder, r.Folder)
	}
	if got.Tool != r.Tool {
		t.Errorf("Tool = %q, want %q", got.Tool, r.Tool)
	}
	if got.Stack != r.Stack {
		t.Errorf("Stack = %q, want %q", got.Stack, r.Stack)
	}
	if got.DockerMode != r.DockerMode {
		t.Errorf("DockerMode = %q, want %q", got.DockerMode, r.DockerMode)
	}
	if len(got.Ports) != len(r.Ports) {
		t.Errorf("Ports len %d, want %d", len(got.Ports), len(r.Ports))
	}
	if got.SavedAt.IsZero() {
		t.Error("SavedAt should be set")
	}
}

func TestStore_Load_NoRecord(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, err := store.Load("/nonexistent/path")
	if !errors.Is(err, ErrNoRecord) {
		t.Errorf("expected ErrNoRecord, got %v", err)
	}
}

func TestStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	r := Record{Folder: "/tmp/test", Tool: "opencode", Stack: "base", DockerMode: "none"}
	if err := store.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete("/tmp/test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Load("/tmp/test")
	if !errors.Is(err, ErrNoRecord) {
		t.Errorf("expected ErrNoRecord after delete, got %v", err)
	}
}

func TestStore_Delete_NonExistent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	// Should not error
	if err := store.Delete("/nonexistent"); err != nil {
		t.Errorf("Delete non-existent should not error, got %v", err)
	}
}

func TestStore_SlugBasedPath(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Two different folders should have different record paths
	path1 := store.recordPath("/home/alice/app1")
	path2 := store.recordPath("/home/alice/app2")
	if path1 == path2 {
		t.Error("different folders should have different quickstart paths")
	}
}
