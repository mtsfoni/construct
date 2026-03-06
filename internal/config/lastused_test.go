package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mtsfoni/construct/internal/config"
)

// withHome sets HOME and USERPROFILE to a fresh temp directory for the
// duration of the test. HOME is used by os.UserHomeDir on Linux/macOS;
// USERPROFILE is used on Windows.
func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// ---- SaveLastUsed / LoadLastUsed ----------------------------------------

func TestSaveAndLoadLastUsed_RoundTrip(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	if err := config.SaveLastUsed(repo, "base", false, nil, "", 0, ""); err != nil {
		t.Fatalf("SaveLastUsed: %v", err)
	}

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Stack != "base" {
		t.Errorf("got stack %q, want %q", got.Stack, "base")
	}
}

func TestLoadLastUsed_ReturnsZeroWhenNoEntry(t *testing.T) {
	withHome(t)

	got, err := config.LoadLastUsed("/nonexistent/repo")
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Stack != "" {
		t.Errorf("expected zero LastUsed, got %+v", got)
	}
}

func TestLoadLastUsed_ReturnsZeroWhenFileAbsent(t *testing.T) {
	withHome(t) // fresh home, no last-used.json

	got, err := config.LoadLastUsed(t.TempDir())
	if err != nil {
		t.Fatalf("LoadLastUsed on missing file: %v", err)
	}
	if got.Stack != "" {
		t.Errorf("expected empty Stack, got %q", got.Stack)
	}
}

func TestSaveLastUsed_UpdatesExistingEntry(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "base", false, nil, "", 0, ""))
	must(t, config.SaveLastUsed(repo, "go", false, nil, "", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Stack != "go" {
		t.Errorf("got stack %q, want %q", got.Stack, "go")
	}
}

func TestSaveLastUsed_IndependentEntriesPerRepo(t *testing.T) {
	withHome(t)

	repo1 := t.TempDir()
	repo2 := t.TempDir()
	must(t, config.SaveLastUsed(repo1, "base", false, nil, "", 0, ""))
	must(t, config.SaveLastUsed(repo2, "go", false, nil, "", 0, ""))

	g1, err := config.LoadLastUsed(repo1)
	if err != nil {
		t.Fatalf("LoadLastUsed repo1: %v", err)
	}
	g2, err := config.LoadLastUsed(repo2)
	if err != nil {
		t.Fatalf("LoadLastUsed repo2: %v", err)
	}

	if g1.Stack != "base" {
		t.Errorf("repo1: got stack %q, want %q", g1.Stack, "base")
	}
	if g2.Stack != "go" {
		t.Errorf("repo2: got stack %q, want %q", g2.Stack, "go")
	}
}

func TestSaveLastUsed_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX permission bits")
	}
	home := withHome(t)

	must(t, config.SaveLastUsed(t.TempDir(), "base", false, nil, "", 0, ""))

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

	must(t, config.SaveLastUsed(t.TempDir(), "base", false, nil, "", 0, ""))

	dir := filepath.Join(home, ".construct")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat .construct dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf(".construct is not a directory")
	}
}

func TestSaveAndLoadLastUsed_MCPAndPorts(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "ui", true, []string{"3000", "8080:8080"}, "dind", 4096, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if !got.MCP {
		t.Errorf("MCP = false, want true")
	}
	if len(got.Ports) != 2 || got.Ports[0] != "3000" || got.Ports[1] != "8080:8080" {
		t.Errorf("Ports = %v, want [3000 8080:8080]", got.Ports)
	}
	if got.DockerMode != "dind" {
		t.Errorf("DockerMode = %q, want %q", got.DockerMode, "dind")
	}
}

func TestSaveAndLoadLastUsed_MCPFalseOmitted(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.MCP {
		t.Errorf("MCP = true, want false")
	}
	if len(got.Ports) != 0 {
		t.Errorf("Ports = %v, want empty", got.Ports)
	}
}

func TestSaveAndLoadLastUsed_DockerMode(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "dind", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.DockerMode != "dind" {
		t.Errorf("DockerMode = %q, want %q", got.DockerMode, "dind")
	}
}

func TestSaveAndLoadLastUsed_DockerModeOmittedWhenEmpty(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	// Empty string means no explicit mode was saved; caller defaults to "none".
	if got.DockerMode != "" {
		t.Errorf("DockerMode = %q, want empty string when not set", got.DockerMode)
	}
}

func TestSaveAndLoadLastUsed_ServePort(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "none", 4096, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.ServePort != 4096 {
		t.Errorf("ServePort = %d, want 4096", got.ServePort)
	}
}

// TestSaveAndLoadLastUsed_ServePortOmittedWhenZero verifies that serve_port is
// omitted from JSON when zero (old entries without the field), and that the
// caller correctly interprets zero as "use the default of 4096".
func TestSaveAndLoadLastUsed_ServePortOmittedWhenZero(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "none", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	// Zero is the sentinel "absent" value; callers default it to 4096.
	if got.ServePort != 0 {
		t.Errorf("ServePort = %d, want 0 (absent/legacy entry)", got.ServePort)
	}
}

func TestSaveAndLoadLastUsed_Client(t *testing.T) {
	withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "none", 4096, "web"))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	if got.Client != "web" {
		t.Errorf("Client = %q, want %q", got.Client, "web")
	}
}

// TestSaveAndLoadLastUsed_ClientOmittedWhenEmpty verifies that client is omitted
// from JSON when empty (old entries without the field), meaning auto-detect.
func TestSaveAndLoadLastUsed_ClientOmittedWhenEmpty(t *testing.T) {
	home := withHome(t)

	repo := t.TempDir()
	must(t, config.SaveLastUsed(repo, "go", false, nil, "none", 0, ""))

	got, err := config.LoadLastUsed(repo)
	if err != nil {
		t.Fatalf("LoadLastUsed: %v", err)
	}
	// Empty string is the sentinel "auto-detect" value.
	if got.Client != "" {
		t.Errorf("Client = %q, want empty string (auto-detect)", got.Client)
	}

	// Confirm "client" key is absent in the JSON file.
	path := filepath.Join(home, ".construct", "last-used.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read last-used.json: %v", err)
	}
	if contains(string(data), `"client"`) {
		t.Errorf("expected \"client\" key to be absent from JSON when empty, but found it in:\n%s", data)
	}
}
