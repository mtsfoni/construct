package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mtsfoni/construct/internal/config"
)

// withHome runs f with HOME set to a fresh temp directory, then restores it.
func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	_ = orig
	return dir
}

// ---- SaveLastUsed / LoadLastUsed ----------------------------------------

func TestSaveAndLoadLastUsed_RoundTrip(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	if err := config.SaveLastUsed(repo, "copilot", "node"); err != nil {
		t.Fatalf("SaveLastUsed: %v", err)
	}

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Tool != "copilot" || got.Stack != "node" {
		t.Errorf("got {%s %s}, want {copilot node}", got.Tool, got.Stack)
	}
}

func TestLoadLastUsed_ReturnsZeroWhenNoEntry(t *testing.T) {
	withHome(t)

	got, err := config.LoadLastUsed("/nonexistent/repo")
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Tool != "" || got.Stack != "" {
		t.Errorf("expected zero LastUsed, got %+v", got)
	}
}

func TestLoadLastUsed_ReturnsZeroWhenFileAbsent(t *testing.T) {
	withHome(t) // fresh home, no last-used.json

	got, err := config.LoadLastUsed(t.TempDir())
	if err != nil {
		t.Fatalf("LoadLastUsed on missing file: %v", err)
	}
	if got.Tool != "" {
		t.Errorf("expected empty Tool, got %q", got.Tool)
	}
}

func TestSaveLastUsed_UpdatesExistingEntry(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "copilot", "node"))
	must(t, config.SaveLastUsed(repo, "opencode", "python"))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Tool != "opencode" || got.Stack != "python" {
		t.Errorf("got {%s %s}, want {opencode python}", got.Tool, got.Stack)
	}
}

func TestSaveLastUsed_IndependentEntriesPerRepo(t *testing.T) {
	withHome(t)

	repo1 := t.TempDir()
	repo2 := t.TempDir()
	must(t, config.SaveLastUsed(repo1, "copilot", "node"))
	must(t, config.SaveLastUsed(repo2, "opencode", "go"))

	g1, err := config.LoadLastUsed(repo1)
	if err != nil {
		t.Fatalf("LoadLastUsed repo1: %v", err)
	}
	g2, err := config.LoadLastUsed(repo2)
	if err != nil {
		t.Fatalf("LoadLastUsed repo2: %v", err)
	}

	if g1.Tool != "copilot" || g1.Stack != "node" {
		t.Errorf("repo1: got {%s %s}, want {copilot node}", g1.Tool, g1.Stack)
	}
	if g2.Tool != "opencode" || g2.Stack != "go" {
		t.Errorf("repo2: got {%s %s}, want {opencode go}", g2.Tool, g2.Stack)
	}
}

func TestSaveLastUsed_FilePermissions(t *testing.T) {
	home := withHome(t)

	must(t, config.SaveLastUsed(t.TempDir(), "copilot", "node"))

	path := filepath.Join(home, ".construct", "last-used.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat last-used.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveLastUsed_CreatesConstructDir(t *testing.T) {
	home := withHome(t)

	must(t, config.SaveLastUsed(t.TempDir(), "copilot", "node"))

	dir := filepath.Join(home, ".construct")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat .construct dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf(".construct is not a directory")
	}
}
