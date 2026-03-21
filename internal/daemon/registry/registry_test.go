package registry

import (
	"testing"
	"time"
)

func newTestSession(id, repo string) *Session {
	return &Session{
		ID:            id,
		Repo:          repo,
		Tool:          "opencode",
		Stack:         "base",
		DockerMode:    "none",
		ContainerName: "construct-" + id[:8],
		Status:        StatusRunning,
		CreatedAt:     time.Now(),
	}
}

func TestRegistry_AddAndGetByID(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/home/alice/app")
	if err := r.Add(s); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got := r.GetByID(s.ID)
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if got.ID != s.ID {
		t.Errorf("ID = %q, want %q", got.ID, s.ID)
	}
}

func TestRegistry_GetByRepo(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/home/alice/app")
	r.Add(s)

	got := r.GetByRepo("/home/alice/app")
	if got == nil {
		t.Fatal("GetByRepo returned nil")
	}
	if got.Repo != "/home/alice/app" {
		t.Errorf("Repo = %q", got.Repo)
	}
}

func TestRegistry_GetByPrefix(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/app1")
	r.Add(s)

	got, err := r.GetByPrefix("a1b2c3d4")
	if err != nil {
		t.Fatalf("GetByPrefix: %v", err)
	}
	if got == nil {
		t.Fatal("GetByPrefix returned nil")
	}
	if got.ID != s.ID {
		t.Errorf("ID = %q, want %q", got.ID, s.ID)
	}
}

func TestRegistry_GetByPrefix_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	r.Add(newTestSession("aaaa1111-0000-0000-0000-000000000001", "/app1"))
	r.Add(newTestSession("aaaa2222-0000-0000-0000-000000000002", "/app2"))

	_, err := r.GetByPrefix("aaaa")
	if err == nil {
		t.Error("expected error for ambiguous prefix")
	}
}

func TestRegistry_Remove(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/app")
	r.Add(s)
	r.Remove(s.ID)

	if r.GetByID(s.ID) != nil {
		t.Error("session should be removed")
	}
	if r.GetByRepo("/app") != nil {
		t.Error("repo index should be cleared")
	}
}

func TestRegistry_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	// Write
	r1 := New(path)
	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/app")
	r1.Add(s)

	// Read back
	r2 := New(path)
	if err := r2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := r2.GetByID(s.ID)
	if got == nil {
		t.Fatal("session not found after reload")
	}
	if got.Repo != "/app" {
		t.Errorf("Repo = %q, want /app", got.Repo)
	}
}

func TestRegistry_UpdateStatus(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	s := newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/app")
	s.Status = StatusRunning
	r.Add(s)

	now := time.Now()
	if err := r.UpdateStatus(s.ID, StatusStopped, nil, &now); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got := r.GetByID(s.ID)
	if got.Status != StatusStopped {
		t.Errorf("Status = %q, want stopped", got.Status)
	}
	if got.StoppedAt == nil {
		t.Error("StoppedAt should be set")
	}
}

func TestRegistry_List(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/state.json")

	r.Add(newTestSession("a1b2c3d4-e5f6-7890-abcd-ef1234567890", "/app1"))
	r.Add(newTestSession("b2c3d4e5-e5f6-7890-abcd-ef1234567891", "/app2"))

	list := r.List()
	if len(list) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(list))
	}
}

func TestRegistry_LoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	r := New(dir + "/nonexistent.json")
	if err := r.Load(); err != nil {
		t.Errorf("Load from nonexistent file should not error: %v", err)
	}
	if len(r.List()) != 0 {
		t.Error("empty registry should have no sessions")
	}
}
