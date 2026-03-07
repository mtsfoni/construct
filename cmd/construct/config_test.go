// Integration tests for the construct CLI config subcommand.
//
// TestMain compiles the binary once into a temp directory; each test runs it
// as a real subprocess with a controlled HOME so no real credential files are
// touched.
package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Compile the binary into a temp dir shared across all tests.
	dir, err := os.MkdirTemp("", "construct-cli-test-*")
	if err != nil {
		panic("create temp dir: " + err.Error())
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "construct")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".").CombinedOutput()
	if err != nil {
		panic("build binary: " + err.Error() + "\n" + string(out))
	}
	binaryPath = bin
	os.Exit(m.Run())
}

// run executes the construct binary with the given args.
// home sets the HOME env var so tests never touch the real ~/.construct/.env.
// cwd sets the working directory (defaults to a temp dir when empty).
func run(t *testing.T, home, cwd string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	// Use a 30-second timeout so tests that accidentally invoke runner.Run
	// (which blocks on Docker) fail fast rather than hanging until the suite
	// hits its 10-minute deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	// Set both HOME and USERPROFILE so os.UserHomeDir() finds the right dir
	// on both Linux/macOS (HOME) and Windows (USERPROFILE).
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	stdout = string(out)
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("run %v timed out after 30s (binary did not exit; likely blocked on Docker)", args)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return stdout, exitCode
}

func TestConfigSet_WritesToGlobalFile(t *testing.T) {
	home := t.TempDir()
	out, code := run(t, home, "", "config", "set", "ANTHROPIC_API_KEY", "sk-ant-test123")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}

	envFile := filepath.Join(home, ".construct", ".env")
	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(content), "ANTHROPIC_API_KEY=sk-ant-test123") {
		t.Errorf("file content = %q, want it to contain ANTHROPIC_API_KEY=sk-ant-test123", string(content))
	}
}

func TestConfigSet_GlobalFileHas0600Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX permission bits")
	}
	home := t.TempDir()
	if _, code := run(t, home, "", "config", "set", "KEY", "val"); code != 0 {
		t.Fatal("non-zero exit")
	}

	info, err := os.Stat(filepath.Join(home, ".construct", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestConfigSet_WritesToLocalFileWithFlag(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	out, code := run(t, home, repo, "config", "set", "--local", "MY_KEY", "my_val")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}

	// Should be in the repo's .construct/.env, not in the global file.
	localFile := filepath.Join(repo, ".construct", ".env")
	content, err := os.ReadFile(localFile)
	if err != nil {
		t.Fatalf("read local env file: %v", err)
	}
	if !strings.Contains(string(content), "MY_KEY=my_val") {
		t.Errorf("local file = %q, want MY_KEY=my_val", string(content))
	}

	// Global file should not have been touched.
	globalFile := filepath.Join(home, ".construct", ".env")
	if _, err := os.Stat(globalFile); !os.IsNotExist(err) {
		t.Error("global file was written but should not have been")
	}
}

func TestConfigSet_UpdatesExistingKey(t *testing.T) {
	home := t.TempDir()
	run(t, home, "", "config", "set", "TOKEN", "old") //nolint:errcheck
	run(t, home, "", "config", "set", "TOKEN", "new") //nolint:errcheck

	content, _ := os.ReadFile(filepath.Join(home, ".construct", ".env"))
	count := strings.Count(string(content), "TOKEN=")
	if count != 1 {
		t.Errorf("TOKEN appears %d times, want 1:\n%s", count, content)
	}
	if !strings.Contains(string(content), "TOKEN=new") {
		t.Errorf("expected TOKEN=new in file, got:\n%s", content)
	}
}

func TestConfigUnset_RemovesKey(t *testing.T) {
	home := t.TempDir()
	run(t, home, "", "config", "set", "KEEP", "yes")
	run(t, home, "", "config", "set", "REMOVE", "bye")
	out, code := run(t, home, "", "config", "unset", "REMOVE")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}

	content, _ := os.ReadFile(filepath.Join(home, ".construct", ".env"))
	if strings.Contains(string(content), "REMOVE") {
		t.Errorf("REMOVE still present after unset:\n%s", content)
	}
	if !strings.Contains(string(content), "KEEP=yes") {
		t.Errorf("KEEP was lost after unset:\n%s", content)
	}
}

func TestConfigUnset_NoopWhenFileAbsent(t *testing.T) {
	home := t.TempDir()
	_, code := run(t, home, "", "config", "unset", "ANY_KEY")
	if code != 0 {
		t.Error("expected exit 0 when unsetting from nonexistent file")
	}
}

func TestConfigList_ShowsKeys(t *testing.T) {
	home := t.TempDir()
	run(t, home, "", "config", "set", "ANTHROPIC_API_KEY", "sk-ant-secret")
	run(t, home, "", "config", "set", "OPENAI_API_KEY", "sk-openai-secret")

	out, code := run(t, home, "", "config", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}

	if !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Errorf("output missing ANTHROPIC_API_KEY:\n%s", out)
	}
	if !strings.Contains(out, "OPENAI_API_KEY") {
		t.Errorf("output missing OPENAI_API_KEY:\n%s", out)
	}
}

func TestConfigList_MasksValues(t *testing.T) {
	home := t.TempDir()
	run(t, home, "", "config", "set", "SECRET", "super_secret_value")

	out, code := run(t, home, "", "config", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "super_secret_value") {
		t.Errorf("list output exposed secret value:\n%s", out)
	}
}

func TestConfigList_EmptyMessage(t *testing.T) {
	home := t.TempDir()
	out, code := run(t, home, "", "config", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected a message for empty config, got blank output")
	}
}

func TestConfigList_LocalScope(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	run(t, home, "", "config", "set", "GLOBAL_KEY", "gval")
	run(t, home, repo, "config", "set", "--local", "LOCAL_KEY", "lval")

	out, code := run(t, home, repo, "config", "list", "--local")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "LOCAL_KEY") {
		t.Errorf("missing LOCAL_KEY in local list:\n%s", out)
	}
	if strings.Contains(out, "GLOBAL_KEY") {
		t.Errorf("local list leaked global key:\n%s", out)
	}
}

func TestConfig_ErrorOnMissingArgs(t *testing.T) {
	home := t.TempDir()

	tests := []struct {
		name string
		args []string
	}{
		{"set missing value", []string{"config", "set", "KEY"}},
		{"set missing key and value", []string{"config", "set"}},
		{"unset missing key", []string{"config", "unset"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, code := run(t, home, "", tc.args...)
			if code == 0 {
				t.Errorf("expected non-zero exit for %v", tc.args)
			}
		})
	}
}
