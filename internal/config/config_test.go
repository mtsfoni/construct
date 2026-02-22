package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mtsfoni/construct/internal/config"
)

// envPath returns a path inside a fresh temp directory. The file does not
// exist yet unless the test creates it.
func envPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".env")
}

// readFile is a small helper that reads a file's contents as a string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(b)
}

// fileMode returns the permission bits of the file at path.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// ---- Set ---------------------------------------------------------------

func TestSet_CreatesFileWithCorrectContent(t *testing.T) {
	p := envPath(t)
	if err := config.Set(p, "MY_KEY", "my_value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := readFile(t, p)
	want := "MY_KEY=my_value\n"
	if got != want {
		t.Errorf("file content = %q, want %q", got, want)
	}
}

func TestSet_CreatesFileWith0600Permissions(t *testing.T) {
	p := envPath(t)
	if err := config.Set(p, "KEY", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if mode := fileMode(t, p); mode != 0o600 {
		t.Errorf("file mode = %o, want 600", mode)
	}
}

func TestSet_CreatesParentDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "dir", ".env")
	if err := config.Set(p, "KEY", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSet_UpdatesExistingKey(t *testing.T) {
	p := envPath(t)
	if err := config.Set(p, "TOKEN", "old"); err != nil {
		t.Fatalf("Set old: %v", err)
	}
	if err := config.Set(p, "TOKEN", "new"); err != nil {
		t.Fatalf("Set new: %v", err)
	}

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if m["TOKEN"] != "new" {
		t.Errorf("TOKEN = %q, want %q", m["TOKEN"], "new")
	}
	// Must not contain duplicate entries.
	content := readFile(t, p)
	count := 0
	for _, line := range splitLines(content) {
		if len(line) > 0 && line[:len("TOKEN")] == "TOKEN" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("key TOKEN appears %d times in file, want 1:\n%s", count, content)
	}
}

func TestSet_AddsNewKeyToExistingFile(t *testing.T) {
	p := envPath(t)
	must(t, config.Set(p, "FIRST", "1"))
	must(t, config.Set(p, "SECOND", "2"))

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if m["FIRST"] != "1" || m["SECOND"] != "2" {
		t.Errorf("got %v, want FIRST=1 SECOND=2", m)
	}
}

func TestSet_PreservesComments(t *testing.T) {
	p := envPath(t)
	must(t, os.WriteFile(p, []byte("# my comment\nEXISTING=yes\n"), 0o600))
	must(t, config.Set(p, "NEW", "val"))

	content := readFile(t, p)
	if !contains(content, "# my comment") {
		t.Errorf("comment was stripped from file:\n%s", content)
	}
	if !contains(content, "EXISTING=yes") {
		t.Errorf("existing key lost:\n%s", content)
	}
	if !contains(content, "NEW=val") {
		t.Errorf("new key not written:\n%s", content)
	}
}

// ---- Unset -------------------------------------------------------------

func TestUnset_RemovesKey(t *testing.T) {
	p := envPath(t)
	must(t, config.Set(p, "REMOVE_ME", "gone"))
	must(t, config.Set(p, "KEEP", "here"))
	must(t, config.Unset(p, "REMOVE_ME"))

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := m["REMOVE_ME"]; ok {
		t.Error("REMOVE_ME still present after Unset")
	}
	if m["KEEP"] != "here" {
		t.Errorf("KEEP = %q, want %q", m["KEEP"], "here")
	}
}

func TestUnset_NoopOnMissingKey(t *testing.T) {
	p := envPath(t)
	must(t, config.Set(p, "KEY", "val"))
	if err := config.Unset(p, "NONEXISTENT"); err != nil {
		t.Errorf("Unset of missing key returned error: %v", err)
	}
}

func TestUnset_NoopOnMissingFile(t *testing.T) {
	p := envPath(t) // file does not exist
	if err := config.Unset(p, "ANY"); err != nil {
		t.Errorf("Unset on missing file returned error: %v", err)
	}
}

// ---- List --------------------------------------------------------------

func TestList_ReturnsAllKeyValues(t *testing.T) {
	p := envPath(t)
	must(t, os.WriteFile(p, []byte("A=1\nB=two\nC=three\n"), 0o600))

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]string{"A": "1", "B": "two", "C": "three"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
}

func TestList_IgnoresCommentsAndBlankLines(t *testing.T) {
	p := envPath(t)
	must(t, os.WriteFile(p, []byte("# comment\n\nKEY=val\n"), 0o600))

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(m) != 1 || m["KEY"] != "val" {
		t.Errorf("got %v, want {KEY:val}", m)
	}
}

func TestList_EmptyOnMissingFile(t *testing.T) {
	p := envPath(t) // does not exist
	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List on missing file returned error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestList_StripsQuotes(t *testing.T) {
	p := envPath(t)
	must(t, os.WriteFile(p, []byte(`SINGLE='hello'`+"\n"+`DOUBLE="world"`+"\n"), 0o600))

	m, err := config.List(p)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if m["SINGLE"] != "hello" {
		t.Errorf("SINGLE = %q, want %q", m["SINGLE"], "hello")
	}
	if m["DOUBLE"] != "world" {
		t.Errorf("DOUBLE = %q, want %q", m["DOUBLE"], "world")
	}
}

// ---- GlobalFile / LocalFile --------------------------------------------

func TestGlobalFile_ContainsConstructDir(t *testing.T) {
	p, err := config.GlobalFile()
	if err != nil {
		t.Fatalf("GlobalFile: %v", err)
	}
	if !contains(p, ".construct") {
		t.Errorf("GlobalFile = %q, expected path to contain .construct", p)
	}
	if filepath.Base(p) != ".env" {
		t.Errorf("GlobalFile base = %q, want .env", filepath.Base(p))
	}
}

func TestLocalFile_ReturnsConstructEnvUnderDir(t *testing.T) {
	dir := t.TempDir()
	p := config.LocalFile(dir)
	want := filepath.Join(dir, ".construct", ".env")
	if p != want {
		t.Errorf("LocalFile = %q, want %q", p, want)
	}
}

// ---- helpers -----------------------------------------------------------

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
