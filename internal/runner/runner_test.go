package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mtsfoni/construct/internal/dind"
	"github.com/mtsfoni/construct/internal/tools"
)

// TestMain builds the shared entrypoint test image once before all tests run
// and removes it after the suite finishes. This avoids each test building and
// immediately cleaning up the same image, which would leave later tests in the
// same run unable to use it.
func TestMain(m *testing.M) {
	code := m.Run()
	// Best-effort cleanup of the shared entrypoint image; ignore errors.
	exec.Command("docker", "rmi", "-f", entrypointTestImageName).Run() //nolint:errcheck
	os.Exit(code)
}

// entrypointTestImageName is the tag used for the shared entrypoint test image.
const entrypointTestImageName = "construct-entrypoint-test-ports"

// entrypointImageOnce ensures the image is built at most once per test binary run.
var (
	entrypointImageOnce  sync.Once
	entrypointImageBuilt bool
)

// buildEntrypointTestImage builds (at most once) a minimal Docker image
// containing the generated entrypoint script and returns true on success.
// Callers should t.Skip when it returns false.
func buildEntrypointTestImage(t *testing.T) bool {
	t.Helper()
	if !dockerAvailable() {
		return false
	}
	entrypointImageOnce.Do(func() {
		buildDir, err := os.MkdirTemp("", "construct-ep-build-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(buildDir)

		ep := generatedEntrypoint()
		if err := os.WriteFile(filepath.Join(buildDir, "construct-entrypoint.sh"), []byte(ep), 0o755); err != nil {
			return
		}
		df := "FROM ubuntu:22.04\nCOPY construct-entrypoint.sh /entrypoint.sh\nRUN chmod +x /entrypoint.sh\n"
		if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(df), 0o644); err != nil {
			return
		}
		if out, err := exec.Command("docker", "build", "-t", entrypointTestImageName, buildDir).CombinedOutput(); err != nil {
			t.Logf("build entrypoint test image: %v\n%s", err, out)
			return
		}
		entrypointImageBuilt = true
	})
	return entrypointImageBuilt
}

// runEntrypoint runs the entrypoint test image with the provided extra docker
// run flags and shell command, returning trimmed stdout. It is the container
// equivalent of "debug mode": no agent is started, just sh.
func runEntrypoint(t *testing.T, extraFlags []string, shellCmd string) string {
	t.Helper()
	args := []string{"run", "--rm", "--entrypoint", "/entrypoint.sh"}
	args = append(args, extraFlags...)
	args = append(args, entrypointTestImageName, "sh", "-c", shellCmd)
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker run failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

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
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", secretsDir)

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
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", secretsDir)

	// The secrets dir is still mounted; MISSING_KEY simply won't be present inside.
	joined := strings.Join(args, " ")
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "MISSING_KEY=") {
			t.Errorf("missing key should not appear as -e flag: %s", joined)
		}
	}
}

// TestBuildRunArgs_UserFlag verifies that --user is included on platforms where
// os.Getuid() returns a valid ID (Linux/macOS) and omitted on Windows (where it
// returns -1) to avoid passing an invalid --user -1:-1 to Docker.
func TestBuildRunArgs_UserFlag(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "")

	hasUser := false
	for _, arg := range args {
		if arg == "--user" {
			hasUser = true
			break
		}
	}

	if os.Getuid() < 0 {
		// Windows: --user must not be present to avoid --user -1:-1.
		if hasUser {
			t.Error("--user flag must not appear when os.Getuid() < 0 (Windows)")
		}
	} else {
		// Linux/macOS: --user must be present for correct workspace ownership.
		if !hasUser {
			t.Error("--user flag must appear when os.Getuid() >= 0")
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
		// Windows does not enforce POSIX permission bits; skip the check there.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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

// TestEnsureHomeVolume_SetsLabel is an integration test that verifies the home
// volume is created with the io.construct.managed=true label so that
// `docker volume prune` (which skips labelled volumes by default) does not
// remove it and wipe persisted tool auth state (e.g. gh auth login tokens).
func TestEnsureHomeVolume_SetsLabel(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not accessible")
	}

	volumeName := "construct-test-label-" + t.Name()
	// Ensure the volume does not already exist from a previous run.
	exec.Command("docker", "volume", "rm", volumeName).Run()                       //nolint:errcheck
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", volumeName).Run() }) //nolint:errcheck

	if err := ensureHomeVolume(volumeName, "", nil, ""); err != nil {
		t.Fatalf("ensureHomeVolume: %v", err)
	}

	out, err := exec.Command("docker", "volume", "inspect",
		"--format", "{{index .Labels \"io.construct.managed\"}}",
		volumeName,
	).Output()
	if err != nil {
		t.Fatalf("inspect volume: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "true" {
		t.Errorf("label io.construct.managed = %q, want %q", got, "true")
	}
}

// TestEnsureHomeVolume_PreCreatesAuthParentDirs is an integration test that
// verifies that when authMountPath is set, ensureHomeVolume pre-creates the
// parent directories inside the home volume and chowns them to the current
// user. This prevents Docker's automatic mount-point creation for nested auth
// volumes from leaving intermediate directories (e.g. /home/agent/.local)
// root-owned, which would block the agent from creating siblings like
// /home/agent/.local/state (EACCES).
func TestEnsureHomeVolume_PreCreatesAuthParentDirs(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	volumeName := "construct-test-authdirs-" + t.Name()
	exec.Command("docker", "volume", "rm", volumeName).Run()                       //nolint:errcheck
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", volumeName).Run() }) //nolint:errcheck

	authMountPath := "/home/agent/.local/share/opencode"
	if err := ensureHomeVolume(volumeName, "", nil, authMountPath); err != nil {
		t.Fatalf("ensureHomeVolume: %v", err)
	}

	uid := os.Getuid()
	gid := os.Getgid()

	// Verify that /home/agent/.local/share exists and is owned by the current user.
	// We use stat -c "%u %g" for portable numeric uid:gid output.
	out, err := exec.Command("docker", "run", "--rm",
		"-v", volumeName+":/home/agent",
		"ubuntu:22.04",
		"sh", "-c", "stat -c '%u %g' /home/agent/.local && stat -c '%u %g' /home/agent/.local/share",
	).Output()
	if err != nil {
		t.Fatalf("stat in volume: %v", err)
	}

	want := fmt.Sprintf("%d %d", uid, gid)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != want {
			t.Errorf("unexpected ownership %q, want %q (uid:gid of current user)", line, want)
		}
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

	entrypoint := generatedEntrypoint()

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

// entrypointTestImage builds (once per test binary run) a minimal Docker image
// that contains the generated entrypoint script and returns its name.
// The image is cleaned up via t.Cleanup when docker is available.
//
// TestEntrypointScript_PortEnvVars_CONSTRUCT verifies that when CONSTRUCT=1 is
// passed to the container the variable is visible to the process that the
// entrypoint hands off to.
func TestEntrypointScript_PortEnvVars_CONSTRUCT(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		[]string{"-e", "CONSTRUCT=1"},
		"echo $CONSTRUCT",
	)
	if got != "1" {
		t.Errorf("CONSTRUCT = %q inside container, want %q", got, "1")
	}
}

// TestEntrypointScript_PortEnvVars_CONSTRUCT_PORTS verifies that CONSTRUCT_PORTS
// is forwarded intact through the entrypoint to the child process.
func TestEntrypointScript_PortEnvVars_CONSTRUCT_PORTS(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		[]string{"-e", "CONSTRUCT=1", "-e", "CONSTRUCT_PORTS=3000"},
		"echo $CONSTRUCT_PORTS",
	)
	if got != "3000" {
		t.Errorf("CONSTRUCT_PORTS = %q inside container, want %q", got, "3000")
	}
}

// TestEntrypointScript_PortEnvVars_MultiplePorts verifies that a comma-separated
// CONSTRUCT_PORTS value (set by the runner when multiple --port flags are used)
// is forwarded intact.
func TestEntrypointScript_PortEnvVars_MultiplePorts(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		[]string{"-e", "CONSTRUCT=1", "-e", "CONSTRUCT_PORTS=3000,8080"},
		"echo $CONSTRUCT_PORTS",
	)
	if got != "3000,8080" {
		t.Errorf("CONSTRUCT_PORTS = %q inside container, want %q", got, "3000,8080")
	}
}

// TestEntrypointScript_PortEnvVars_AbsentWhenNotSet verifies that CONSTRUCT and
// CONSTRUCT_PORTS are empty strings (not set) when the runner does not inject
// them — i.e. when --port is not used.
func TestEntrypointScript_PortEnvVars_AbsentWhenNotSet(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}

	construct := runEntrypoint(t, nil, "echo ${CONSTRUCT:-unset}")
	if construct != "unset" {
		t.Errorf("CONSTRUCT = %q inside container, want it unset (no --port given)", construct)
	}

	ports := runEntrypoint(t, nil, "echo ${CONSTRUCT_PORTS:-unset}")
	if ports != "unset" {
		t.Errorf("CONSTRUCT_PORTS = %q inside container, want it unset (no --port given)", ports)
	}
}

// dockerAvailable returns true if the Docker CLI and daemon are both usable.
func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// TestRemoveHomeVolume_RemovesVolume creates a real Docker volume and verifies
// that removeHomeVolume deletes it.
func TestRemoveHomeVolume_RemovesVolume(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	volumeName := "construct-test-remove-" + t.Name()
	// Clean up any leftover from a previous run.
	exec.Command("docker", "volume", "rm", volumeName).Run()                       //nolint:errcheck
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", volumeName).Run() }) //nolint:errcheck

	// Create the volume.
	if out, err := exec.Command("docker", "volume", "create", volumeName).CombinedOutput(); err != nil {
		t.Fatalf("create volume: %v\n%s", err, out)
	}

	if err := removeHomeVolume(volumeName); err != nil {
		t.Fatalf("removeHomeVolume: %v", err)
	}

	// Inspect should now fail because the volume is gone.
	out, err := exec.Command("docker", "volume", "inspect", volumeName).CombinedOutput()
	if err == nil {
		t.Errorf("expected volume to be gone, but inspect succeeded: %s", out)
	}
}

// TestRemoveHomeVolume_NoopWhenAbsent verifies that removeHomeVolume does not
// return an error when the volume does not exist.
func TestRemoveHomeVolume_NoopWhenAbsent(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	volumeName := "construct-test-absent-" + t.Name()
	// Make absolutely sure it does not exist.
	exec.Command("docker", "volume", "rm", volumeName).Run() //nolint:errcheck

	if err := removeHomeVolume(volumeName); err != nil {
		t.Errorf("removeHomeVolume on absent volume returned error: %v", err)
	}
}

// TestAuthVolumeName_IsGlobal verifies that authVolumeName is not keyed by repo
// path — the same tool always produces the same volume name.
func TestAuthVolumeName_IsGlobal(t *testing.T) {
	name1 := authVolumeName("opencode")
	name2 := authVolumeName("opencode")
	if name1 != name2 {
		t.Errorf("authVolumeName not deterministic: %q vs %q", name1, name2)
	}
	if !strings.HasPrefix(name1, "construct-auth-") {
		t.Errorf("authVolumeName %q does not start with construct-auth-", name1)
	}
}

// TestAuthVolumeName_DiffersFromHomeVolume verifies that the auth volume name
// cannot collide with any home volume name.
func TestAuthVolumeName_DiffersFromHomeVolume(t *testing.T) {
	authName := authVolumeName("opencode")
	homeName := homeVolumeName("/some/repo", "opencode")
	if authName == homeName {
		t.Errorf("authVolumeName and homeVolumeName collide: %q", authName)
	}
}

// TestBuildRunArgs_MountsAuthVolume verifies that when a tool defines
// AuthVolumePath the global auth volume is mounted at that path.
func TestBuildRunArgs_MountsAuthVolume(t *testing.T) {
	cfg := &Config{
		Tool: &tools.Tool{
			Name:           "testtool",
			RunCmd:         []string{"echo"},
			AuthVolumePath: "/home/agent/.local/share/testtool",
		},
		Stack:    "node",
		RepoPath: t.TempDir(),
	}
	authVol := authVolumeName("testtool")
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", authVol, "")
	joined := strings.Join(args, " ")

	want := authVol + ":/home/agent/.local/share/testtool"
	if !strings.Contains(joined, want) {
		t.Errorf("expected auth volume mount %q in args, got: %s", want, joined)
	}
}

// TestBuildRunArgs_NoAuthVolumeWhenPathEmpty verifies that no extra volume is
// mounted when AuthVolumePath is empty (i.e. the tool does not need one).
func TestBuildRunArgs_NoAuthVolumeWhenPathEmpty(t *testing.T) {
	cfg := fakeConfig(t, nil) // AuthVolumePath is ""
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "construct-auth-") {
		t.Errorf("unexpected auth volume in args: %s", joined)
	}
}

// TestEnsureAuthVolume_SetsLabel verifies the auth volume is created with the
// io.construct.managed=true label so docker volume prune ignores it.
func TestEnsureAuthVolume_SetsLabel(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	volumeName := "construct-test-auth-label-" + t.Name()
	exec.Command("docker", "volume", "rm", volumeName).Run()                       //nolint:errcheck
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", volumeName).Run() }) //nolint:errcheck

	if err := ensureAuthVolume(volumeName); err != nil {
		t.Fatalf("ensureAuthVolume: %v", err)
	}

	out, err := exec.Command("docker", "volume", "inspect",
		"--format", "{{index .Labels \"io.construct.managed\"}}",
		volumeName,
	).Output()
	if err != nil {
		t.Fatalf("inspect volume: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "true" {
		t.Errorf("label io.construct.managed = %q, want %q", got, "true")
	}
}

// TestEnsureAuthVolume_IdempotentWhenExists verifies that calling ensureAuthVolume
// on a volume that already exists returns no error.
func TestEnsureAuthVolume_IdempotentWhenExists(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	volumeName := "construct-test-auth-idempotent-" + t.Name()
	exec.Command("docker", "volume", "rm", volumeName).Run()                       //nolint:errcheck
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", volumeName).Run() }) //nolint:errcheck

	if err := ensureAuthVolume(volumeName); err != nil {
		t.Fatalf("first ensureAuthVolume: %v", err)
	}
	if err := ensureAuthVolume(volumeName); err != nil {
		t.Fatalf("second ensureAuthVolume (idempotent): %v", err)
	}
}

// TestBuildRunArgs_MCPEnvVar_WhenMCPTrue asserts that CONSTRUCT_MCP=1 is
// present in the docker run args when cfg.MCP is true.
func TestBuildRunArgs_MCPEnvVar_WhenMCPTrue(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		Stack:    "ui",
		RepoPath: t.TempDir(),
		MCP:      true,
	}
	args := buildRunArgs(cfg, fakeDind(), "construct-ui-opencode", "testsess", "home-vol", "", "")
	if !containsPair(args, "-e", "CONSTRUCT_MCP=1") {
		t.Errorf("expected -e CONSTRUCT_MCP=1 in args when MCP=true; got: %v", args)
	}
}

// TestBuildRunArgs_MCPEnvVar_AbsentWhenMCPFalse asserts that CONSTRUCT_MCP is
// not injected when cfg.MCP is false.
func TestBuildRunArgs_MCPEnvVar_AbsentWhenMCPFalse(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		Stack:    "ui",
		RepoPath: t.TempDir(),
		MCP:      false,
	}
	args := buildRunArgs(cfg, fakeDind(), "construct-ui-opencode", "testsess", "home-vol", "", "")
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "CONSTRUCT_MCP") {
			t.Errorf("unexpected CONSTRUCT_MCP env var in args when MCP=false; got: %v", args)
		}
	}
}

// TestBuildRunArgs_MCPEnvVar_AbsentOnGoStack confirms that stack choice does
// not override the MCP flag — no CONSTRUCT_MCP when MCP=false regardless of stack.
func TestBuildRunArgs_MCPEnvVar_AbsentOnGoStack(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		Stack:    "go",
		RepoPath: t.TempDir(),
		MCP:      false,
	}
	args := buildRunArgs(cfg, fakeDind(), "construct-go-opencode", "testsess", "home-vol", "", "")
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "CONSTRUCT_MCP") {
			t.Errorf("unexpected CONSTRUCT_MCP env var when MCP=false on go stack; got: %v", args)
		}
	}
}

// TestGeneratedEntrypoint_ContainsMCPBlock verifies the entrypoint includes
// the CONSTRUCT_MCP conditional block that writes opencode.json.
func TestGeneratedEntrypoint_ContainsMCPBlock(t *testing.T) {
	script := generatedEntrypoint()
	checks := []struct {
		desc    string
		snippet string
	}{
		{"checks CONSTRUCT_MCP env var", "CONSTRUCT_MCP"},
		{"creates opencode config dir", "mkdir -p"},
		{"writes opencode.json", "opencode.json"},
		{"includes playwright mcp command", "@playwright/mcp"},
		{"still exports secrets", "/run/secrets"},
		{"ends with exec", `exec "$@"`},
	}
	for _, c := range checks {
		if !strings.Contains(script, c.snippet) {
			t.Errorf("entrypoint: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

// TestGeneratedEntrypoint_MCPBlockBeforeExec verifies the MCP config block
// appears before the final exec line.
func TestGeneratedEntrypoint_MCPBlockBeforeExec(t *testing.T) {
	script := generatedEntrypoint()
	mcpIdx := strings.Index(script, "CONSTRUCT_MCP")
	execIdx := strings.LastIndex(script, `exec "$@"`)
	if mcpIdx == -1 {
		t.Fatal("CONSTRUCT_MCP block not found in entrypoint")
	}
	if execIdx == -1 {
		t.Fatal(`exec "$@" not found in entrypoint`)
	}
	if mcpIdx > execIdx {
		t.Error("MCP block appears after exec line; it must come before")
	}
}

// TestGeneratedEntrypoint_DeletesMCPConfigWhenDisabled verifies that the
// entrypoint removes opencode.json when CONSTRUCT_MCP is not set, so that a
// persistent home volume does not retain a stale MCP config from a previous
// run that used --mcp.
func TestGeneratedEntrypoint_DeletesMCPConfigWhenDisabled(t *testing.T) {
	script := generatedEntrypoint()
	// The else branch must be present with an rm -f of opencode.json.
	if !strings.Contains(script, "rm -f") {
		t.Error("entrypoint: expected 'rm -f' in else branch to remove stale MCP config")
	}
	if !strings.Contains(script, "rm -f \"${HOME}/.config/opencode/opencode.json\"") {
		t.Error("entrypoint: expected rm -f to target opencode.json specifically")
	}
	// The else must follow the fi-less MCP write block, not be a standalone statement.
	elseIdx := strings.Index(script, "\nelse\n")
	if elseIdx == -1 {
		t.Error("entrypoint: expected 'else' branch in MCP conditional")
	}
}

// TestGeneratedEntrypoint_ContainsConstructAgentsBlock verifies the entrypoint
// always writes ~/.config/opencode/AGENTS.md (CONSTRUCT=1 is always set) and
// appends port-binding instructions only when CONSTRUCT_PORTS is non-empty.
func TestGeneratedEntrypoint_ContainsConstructAgentsBlock(t *testing.T) {
	script := generatedEntrypoint()
	checks := []struct {
		desc    string
		snippet string
	}{
		{"creates opencode config dir for AGENTS.md", ".config/opencode"},
		{"writes AGENTS.md", "AGENTS.md"},
		{"instructs agent to bind to 0.0.0.0", "0.0.0.0"},
		{"mentions CONSTRUCT_PORTS", "CONSTRUCT_PORTS"},
		{"conditionally appends port rules when CONSTRUCT_PORTS set", "if [ -n \"${CONSTRUCT_PORTS}\" ]"},
	}
	for _, c := range checks {
		if !strings.Contains(script, c.snippet) {
			t.Errorf("entrypoint: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
	// rm -f on AGENTS.md should no longer exist — file is always written.
	if strings.Contains(script, "rm -f") && strings.Contains(script, "AGENTS.md") {
		// Only fail if the rm -f and AGENTS.md appear in the same logical block.
		// Check by looking for the exact old pattern.
		if strings.Contains(script, "rm -f \"${HOME}/.config/opencode/AGENTS.md\"") {
			t.Error("entrypoint should not delete AGENTS.md; it must always be written")
		}
	}
}

// TestGeneratedEntrypoint_ConstructBlockBeforeExec verifies the CONSTRUCT
// AGENTS.md block appears before the final exec line.
func TestGeneratedEntrypoint_ConstructBlockBeforeExec(t *testing.T) {
	script := generatedEntrypoint()
	// Find the AGENTS.md write line as the anchor for the CONSTRUCT block.
	agentsMdIdx := strings.Index(script, "AGENTS.md")
	execIdx := strings.LastIndex(script, `exec "$@"`)
	if agentsMdIdx == -1 {
		t.Fatal("AGENTS.md block not found in entrypoint")
	}
	if execIdx == -1 {
		t.Fatal(`exec "$@" not found in entrypoint`)
	}
	if agentsMdIdx > execIdx {
		t.Error("AGENTS.md block appears after exec line; it must come before")
	}
}

// TestEntrypointScript_WritesAgentsMD_WithPortSection verifies that when
// CONSTRUCT_PORTS is set the entrypoint includes port-binding rules in AGENTS.md.
func TestEntrypointScript_WritesAgentsMD_WithPortSection(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		[]string{"-e", "CONSTRUCT=1", "-e", "CONSTRUCT_PORTS=3000"},
		"cat ${HOME}/.config/opencode/AGENTS.md",
	)
	if got == "" {
		t.Error("expected ~/.config/opencode/AGENTS.md to be non-empty when CONSTRUCT_PORTS is set")
	}
	// The file must mention 0.0.0.0 so the agent knows to bind to all interfaces.
	if !strings.Contains(got, "0.0.0.0") {
		t.Errorf("AGENTS.md does not mention 0.0.0.0; got:\n%s", got)
	}
	// The file must mention CONSTRUCT_PORTS so the agent knows the port var name.
	if !strings.Contains(got, "CONSTRUCT_PORTS") {
		t.Errorf("AGENTS.md does not mention CONSTRUCT_PORTS; got:\n%s", got)
	}
}

// TestEntrypointScript_AgentsMD_AlwaysPresent verifies the entrypoint always
// writes ~/.config/opencode/AGENTS.md, even when no ports are configured.
func TestEntrypointScript_AgentsMD_AlwaysPresent(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		nil,
		"test -f ${HOME}/.config/opencode/AGENTS.md && echo present || echo absent",
	)
	if got != "present" {
		t.Errorf("expected ~/.config/opencode/AGENTS.md to always be present; got %q", got)
	}
}

// TestEntrypointScript_AgentsMD_NoPortSection verifies that without
// CONSTRUCT_PORTS the AGENTS.md does not contain port-binding rules.
func TestEntrypointScript_AgentsMD_NoPortSection(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		nil,
		"cat ${HOME}/.config/opencode/AGENTS.md",
	)
	if strings.Contains(got, "0.0.0.0") {
		t.Errorf("AGENTS.md should not contain port-binding rules when no ports set; got:\n%s", got)
	}
}

// TestEntrypointScript_AgentsMD_IncludesPortValue verifies that the AGENTS.md
// written by the entrypoint contains the actual port value from CONSTRUCT_PORTS.
func TestEntrypointScript_AgentsMD_IncludesPortValue(t *testing.T) {
	if !buildEntrypointTestImage(t) {
		t.Skip("docker not available or image build failed")
	}
	got := runEntrypoint(t,
		[]string{"-e", "CONSTRUCT=1", "-e", "CONSTRUCT_PORTS=9000"},
		"cat ${HOME}/.config/opencode/AGENTS.md",
	)
	if !strings.Contains(got, "9000") {
		t.Errorf("AGENTS.md does not contain the port value 9000; got:\n%s", got)
	}
}

// containsPair reports whether slice contains the two consecutive values a, b.
func containsPair(slice []string, a, b string) bool {
	for i := 0; i+1 < len(slice); i++ {
		if slice[i] == a && slice[i+1] == b {
			return true
		}
	}
	return false
}

// TestBuildRunArgs_Ports_SingleBarePort verifies that a bare port number is
// expanded to "N:N" so host and container use the same port, and that
// CONSTRUCT=1 / CONSTRUCT_PORTS are injected.
func TestBuildRunArgs_Ports_SingleBarePort(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		RepoPath: t.TempDir(),
		Ports:    []string{"3000"},
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	// Bare "3000" must be expanded to "3000:3000" so the host port matches.
	if !containsPair(args, "-p", "3000:3000") {
		t.Errorf("expected -p 3000:3000 (bare port expanded) in args; got: %v", args)
	}
	if containsPair(args, "-p", "3000") {
		// "3000" without a colon is the unexpanded form — it must not appear.
		found := false
		for i, a := range args {
			if a == "-p" && i+1 < len(args) && args[i+1] == "3000" {
				found = true
				break
			}
		}
		if found {
			t.Errorf("unexpected unexpanded -p 3000 in args (should be 3000:3000); got: %v", args)
		}
	}
	if !containsPair(args, "-e", "CONSTRUCT=1") {
		t.Errorf("expected -e CONSTRUCT=1 in args; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_PORTS=3000") {
		t.Errorf("expected -e CONSTRUCT_PORTS=3000 in args; got: %v", args)
	}
}

// TestBuildRunArgs_Ports_ColonMapping verifies that "host:container" format is
// passed through verbatim to -p and that CONSTRUCT_PORTS carries only the
// container-side port.
func TestBuildRunArgs_Ports_ColonMapping(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		RepoPath: t.TempDir(),
		Ports:    []string{"9000:3000"},
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	if !containsPair(args, "-p", "9000:3000") {
		t.Errorf("expected -p 9000:3000 in args; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_PORTS=3000") {
		t.Errorf("expected CONSTRUCT_PORTS to hold container-side port 3000; got: %v", args)
	}
}

// TestBuildRunArgs_Ports_Multiple verifies that multiple --port values each
// produce a -p flag and that CONSTRUCT_PORTS lists all container-side ports.
// Bare port numbers are expanded to "N:N".
func TestBuildRunArgs_Ports_Multiple(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		RepoPath: t.TempDir(),
		Ports:    []string{"3000", "8080:8080"},
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	// "3000" (bare) must be expanded to "3000:3000".
	if !containsPair(args, "-p", "3000:3000") {
		t.Errorf("expected -p 3000:3000 (bare port expanded) in args; got: %v", args)
	}
	if !containsPair(args, "-p", "8080:8080") {
		t.Errorf("expected -p 8080:8080 in args; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_PORTS=3000,8080") {
		t.Errorf("expected -e CONSTRUCT_PORTS=3000,8080 in args; got: %v", args)
	}
}

// TestBuildRunArgs_Ports_ThreePartMapping verifies that a full
// "ip:host:container" format yields the container-side port in CONSTRUCT_PORTS.
func TestBuildRunArgs_Ports_ThreePartMapping(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		RepoPath: t.TempDir(),
		Ports:    []string{"127.0.0.1:3000:3000"},
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	if !containsPair(args, "-p", "127.0.0.1:3000:3000") {
		t.Errorf("expected -p 127.0.0.1:3000:3000 in args; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_PORTS=3000") {
		t.Errorf("expected CONSTRUCT_PORTS=3000 for three-part mapping; got: %v", args)
	}
}

// TestBuildRunArgs_Ports_AbsentWhenEmpty verifies that CONSTRUCT_PORTS is NOT
// injected when no ports are requested. CONSTRUCT=1 is always present.
func TestBuildRunArgs_Ports_AbsentWhenEmpty(t *testing.T) {
	cfg := &Config{
		Tool:     fakeConfig(t, nil).Tool,
		RepoPath: t.TempDir(),
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	// CONSTRUCT=1 is always injected so the agent knows it is inside construct.
	if !containsPair(args, "-e", "CONSTRUCT=1") {
		t.Errorf("CONSTRUCT=1 should always be present; got: %v", args)
	}
	if strings.Contains(joined, "CONSTRUCT_PORTS") {
		t.Errorf("CONSTRUCT_PORTS should be absent when no ports set; got: %v", args)
	}
}
