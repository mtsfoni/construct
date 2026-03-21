package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderFromKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"OPENCODE_GITHUB_TOKEN", "opencode"},
		{"GITHUB_TOKEN", "github"},
		{"MY_CUSTOM_KEY", "my"},
		{"APIKEY", "apikey"},        // no underscore
		{"apikey", "apikey"},        // already lowercase
		{"SOME_THING_ELSE", "some"}, // multiple underscores
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := ProviderFromKey(tt.key)
			if got != tt.want {
				t.Errorf("ProviderFromKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestStore_SetGetUnset(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Set a global credential
	if err := store.Set("ANTHROPIC_API_KEY", "sk-test-123", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// The file should exist
	path := filepath.Join(dir, "credentials", "global", "anthropic.env")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if string(data) != "ANTHROPIC_API_KEY=sk-test-123\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}

	// List should return it
	creds, err := store.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	if creds[0].Key != "ANTHROPIC_API_KEY" {
		t.Errorf("expected key ANTHROPIC_API_KEY, got %q", creds[0].Key)
	}
	if creds[0].Scope != "global" {
		t.Errorf("expected scope global, got %q", creds[0].Scope)
	}
	if creds[0].MaskedValue != "****" {
		t.Errorf("expected masked value ****, got %q", creds[0].MaskedValue)
	}

	// Update the value
	if err := store.Set("ANTHROPIC_API_KEY", "sk-new-value", ""); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated env file: %v", err)
	}
	if string(data) != "ANTHROPIC_API_KEY=sk-new-value\n" {
		t.Errorf("unexpected updated file content: %q", string(data))
	}

	// Unset
	if err := store.Unset("ANTHROPIC_API_KEY", ""); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected env file to be removed after unset")
	}
}

func TestStore_FolderScope(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	folderPath := "/home/alice/src/myapp"

	// Set global
	if err := store.Set("ANTHROPIC_API_KEY", "global-value", ""); err != nil {
		t.Fatalf("Set global: %v", err)
	}
	// Set folder-specific
	if err := store.Set("ANTHROPIC_API_KEY", "folder-value", folderPath); err != nil {
		t.Fatalf("Set folder: %v", err)
	}

	// List with folder should return both
	creds, err := store.List(folderPath)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(creds))
	}

	var hasGlobal, hasFolder bool
	for _, c := range creds {
		if c.Scope == "global" {
			hasGlobal = true
		}
		if c.Scope == "folder" {
			hasFolder = true
		}
	}
	if !hasGlobal {
		t.Error("expected global credential")
	}
	if !hasFolder {
		t.Error("expected folder credential")
	}
}

func TestStore_EnsureFolderDir(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	folderPath := "/home/alice/src/myapp"
	if err := store.EnsureFolderDir(folderPath); err != nil {
		t.Fatalf("EnsureFolderDir: %v", err)
	}

	credDir := store.FolderDir(folderPath)
	if _, err := os.Stat(credDir); err != nil {
		t.Errorf("expected folder credentials dir to exist: %v", err)
	}
}

func TestStore_MultipleKeysInFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Two keys from the same provider go to the same file
	if err := store.Set("OPENCODE_GITHUB_TOKEN", "ghtoken", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set("OPENCODE_ANOTHER_TOKEN", "other", ""); err != nil {
		t.Fatalf("Set second key: %v", err)
	}

	path := filepath.Join(dir, "credentials", "global", "opencode.env")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	content := string(data)
	if !contains(content, "OPENCODE_GITHUB_TOKEN=ghtoken") {
		t.Errorf("missing first key in file: %q", content)
	}
	if !contains(content, "OPENCODE_ANOTHER_TOKEN=other") {
		t.Errorf("missing second key in file: %q", content)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
