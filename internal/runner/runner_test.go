package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtsfoni/construct/internal/dind"
	"github.com/mtsfoni/construct/internal/tools"
)

// fakeDind returns a *dind.Instance with deterministic test values.
func fakeDind() *dind.Instance {
	return &dind.Instance{
		SessionID:     "test",
		ContainerName: "construct-dind-test",
		NetworkName:   "construct-net-test",
	}
}

// fakeConfig builds a minimal Config with the given tool AuthEnvVars.
func fakeConfig(t *testing.T, authKeys []string) *Config {
	t.Helper()
	return &Config{
		Tool: &tools.Tool{
			Name:        "testtool",
			AuthEnvVars: authKeys,
			RunCmd:      []string{"echo"},
		},
		Stack:    "node",
		RepoPath: t.TempDir(),
	}
}

func TestBuildRunArgs_UsesSecretNotEnv(t *testing.T) {
	secretsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretsDir, "MY_TOKEN"), []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := fakeConfig(t, []string{"MY_TOKEN"})
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", secretsDir)

	joined := strings.Join(args, " ")

	// The secrets dir must be bind-mounted at /run/secrets.
	if !strings.Contains(joined, "/run/secrets:ro") {
		t.Errorf("expected /run/secrets:ro bind mount in args, got: %s", joined)
	}

	// The credential value must NOT appear anywhere in the args.
	if strings.Contains(joined, "s3cr3t") {
		t.Errorf("credential value leaked in args: %s", joined)
	}
	// No -e flag should carry the key.
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "MY_TOKEN=") {
			t.Errorf("credential exposed via -e flag: %s", args[i+1])
		}
	}
}

func TestBuildRunArgs_MissingSecretSkipped(t *testing.T) {
	// secretsDir is empty (no files written for the key).
	secretsDir := t.TempDir()

	cfg := fakeConfig(t, []string{"MISSING_KEY"})
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", secretsDir)

	// The secrets dir is still mounted; MISSING_KEY simply won't be present inside.
	joined := strings.Join(args, " ")
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "MISSING_KEY=") {
			t.Errorf("missing key should not appear as -e flag: %s", joined)
		}
	}
}

func TestWriteSecretFiles_CreatesFiles(t *testing.T) {
	env := map[string]string{
		"TOKEN_A": "aaa",
		"TOKEN_B": "bbb",
	}
	dir, err := writeSecretFiles([]string{"TOKEN_A", "TOKEN_B"}, env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	for key, want := range env {
		content, err := os.ReadFile(filepath.Join(dir, key))
		if err != nil {
			t.Errorf("read %s: %v", key, err)
			continue
		}
		if string(content) != want {
			t.Errorf("%s = %q, want %q", key, content, want)
		}
		info, _ := os.Stat(filepath.Join(dir, key))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", key, info.Mode().Perm())
		}
	}
}

func TestWriteSecretFiles_SkipsMissingEnvKey(t *testing.T) {
	env := map[string]string{"PRESENT": "val"}
	dir, err := writeSecretFiles([]string{"PRESENT", "ABSENT"}, env)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	if _, err := os.Stat(filepath.Join(dir, "ABSENT")); !os.IsNotExist(err) {
		t.Error("expected no file for absent key")
	}
}

// TestEntrypointScript_ExportsSecrets is an integration test that verifies the
// entrypoint wrapper exports /run/secrets/* as environment variables.
// It is skipped when Docker is unavailable or bind mounts from the current
// filesystem are not visible inside containers (e.g. Docker-in-Docker environments).
func TestEntrypointScript_ExportsSecrets(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not accessible")
	}

	// Verify that bind mounts from this filesystem are visible inside containers.
	// In Docker-in-Docker environments the paths are resolved on the outer host,
	// making the files invisible from inside spawned containers.
	probeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(probeDir, "probe"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("docker", "run", "--rm",
		"-v", probeDir+":/probe:ro",
		"ubuntu:22.04", "cat", "/probe/probe",
	).Output()
	if strings.TrimSpace(string(out)) != "ok" {
		t.Skip("bind mounts from current filesystem not visible inside containers (DinD environment)")
	}

	entrypoint := "#!/bin/sh\n" +
		"if [ -d /run/secrets ]; then\n" +
		"  for f in /run/secrets/*; do\n" +
		"    [ -f \"$f\" ] || continue\n" +
		"    export \"$(basename \"$f\")=$(cat \"$f\")\"\n" +
		"  done\n" +
		"fi\n" +
		"exec \"$@\"\n"

	secretsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretsDir, "TEST_SECRET"), []byte("hello-from-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write the entrypoint to a temp file that will be injected via the image.
	// We build a minimal image to avoid file-vs-directory bind-mount ambiguity.
	buildDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildDir, "construct-entrypoint.sh"), []byte(entrypoint), 0o755); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM ubuntu:22.04\nCOPY construct-entrypoint.sh /entrypoint.sh\nRUN chmod +x /entrypoint.sh\n"
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	testImage := "construct-entrypoint-test"
	if out, err := exec.Command("docker", "build", "-t", testImage, buildDir).CombinedOutput(); err != nil {
		t.Fatalf("build test image: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", testImage).Run() }) //nolint:errcheck

	out, err := exec.Command("docker", "run", "--rm",
		"-v", secretsDir+":/run/secrets:ro",
		"--entrypoint", "/entrypoint.sh",
		testImage,
		"sh", "-c", "echo $TEST_SECRET",
	).Output()
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}

	got := strings.TrimSpace(string(out))
	if got != "hello-from-secret" {
		t.Errorf("entrypoint exported %q, want %q", got, "hello-from-secret")
	}
}
