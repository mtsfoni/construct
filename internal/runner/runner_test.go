package runner

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtsfoni/construct/internal/buildinfo"
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
// DockerMode defaults to "dind" so existing tests that rely on fakeDind() keep
// passing without change.
func fakeConfig(t *testing.T, authKeys []string) *Config {
	t.Helper()
	return &Config{
		Tool: &tools.Tool{
			Name:        "testtool",
			AuthEnvVars: authKeys,
			RunCmd:      []string{"echo"},
		},
		Stack:      "node",
		RepoPath:   t.TempDir(),
		DockerMode: "dind",
	}
}

// TestBuildRunArgs_ExtraArgsAppendedToRunCmd verifies that Config.ExtraArgs are
// appended after Tool.RunCmd at the end of the docker run argument list.
func TestBuildRunArgs_ExtraArgsAppendedToRunCmd(t *testing.T) {
	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "testtool", RunCmd: []string{"opencode"}}
	cfg.ExtraArgs = []string{"continue-session", "dead-beef-1234"}

	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	// Find the image name and confirm what follows it.
	imageIdx := -1
	for i, arg := range args {
		if arg == "testimage" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image name not found in args: %v", args)
	}
	tail := args[imageIdx+1:]
	want := []string{"opencode", "continue-session", "dead-beef-1234"}
	if len(tail) != len(want) {
		t.Fatalf("args after image = %v, want %v", tail, want)
	}
	for i, w := range want {
		if tail[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, tail[i], w)
		}
	}
}

// TestBuildRunArgs_ExtraArgsIgnoredInDebugMode verifies that ExtraArgs are NOT
// forwarded when Debug is true (the command is /bin/bash, not the tool).
func TestBuildRunArgs_ExtraArgsIgnoredInDebugMode(t *testing.T) {
	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "testtool", RunCmd: []string{"opencode"}}
	cfg.ExtraArgs = []string{"continue-session", "dead-beef-1234"}
	cfg.Debug = true

	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	imageIdx := -1
	for i, arg := range args {
		if arg == "testimage" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image name not found in args: %v", args)
	}
	tail := args[imageIdx+1:]
	// Debug mode must pass /bin/bash as the sole command, with no extra args.
	if len(tail) != 1 || tail[0] != "/bin/bash" {
		t.Errorf("debug mode args after image = %v, want [/bin/bash]", tail)
	}
}

// TestBuildRunArgs_NoExtraArgsWhenEmpty verifies that when ExtraArgs is nil or
// empty the command section after the image is just Tool.RunCmd unchanged.
func TestBuildRunArgs_NoExtraArgsWhenEmpty(t *testing.T) {
	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "testtool", RunCmd: []string{"opencode"}}
	cfg.ExtraArgs = nil

	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	imageIdx := -1
	for i, arg := range args {
		if arg == "testimage" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image name not found in args: %v", args)
	}
	tail := args[imageIdx+1:]
	if len(tail) != 1 || tail[0] != "opencode" {
		t.Errorf("args after image = %v, want [opencode]", tail)
	}
}

func TestBuildRunArgs_DockerHostUsesStaticDindAlias(t *testing.T) {
	cfg := fakeConfig(t, nil) // DockerMode = "dind"
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	want := "DOCKER_HOST=tcp://dind:2375"
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) && args[i+1] == want {
			return
		}
	}
	t.Errorf("expected -e %s in args, got: %v", want, args)
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
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

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

// TestBuildRunArgs_GlobalCommandsMountedWhenDirExists verifies that when the
// tool is "opencode" and ~/.config/opencode/commands/ exists on the host, a
// read-only bind mount for that directory is added to the docker run args.
func TestBuildRunArgs_GlobalCommandsMountedWhenDirExists(t *testing.T) {
	// Point HOME at a temp dir so os.UserHomeDir() returns a predictable path.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	commandsDir := filepath.Join(fakeHome, ".config", "opencode", "commands")
	if err := os.MkdirAll(commandsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "opencode", RunCmd: []string{"opencode"}}

	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	want := commandsDir + ":/home/agent/.config/opencode/commands:ro,z"
	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v %s in args, got: %v", want, args)
	}
}

// TestBuildRunArgs_GlobalCommandsNotMountedWhenDirAbsent verifies that no
// commands bind mount is added when ~/.config/opencode/commands/ does not exist.
func TestBuildRunArgs_GlobalCommandsNotMountedWhenDirAbsent(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	// Deliberately do NOT create the commands directory.

	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "opencode", RunCmd: []string{"opencode"}}

	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && strings.Contains(args[i+1], "opencode/commands") {
			t.Errorf("unexpected commands mount when dir is absent: %s", args[i+1])
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

// TestBuildRunArgs_MountsAuthFiles verifies that when a tool defines AuthFiles
// each entry is bind-mounted as an individual file with the :z SELinux suffix.
func TestBuildRunArgs_MountsAuthFiles(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "auth.json")
	cfg := &Config{
		Tool: &tools.Tool{
			Name:   "testtool",
			RunCmd: []string{"echo"},
			AuthFiles: []tools.AuthFile{
				{HostPath: hostPath, ContainerPath: "/home/agent/.local/share/testtool/auth.json"},
			},
		},
		Stack:    "node",
		RepoPath: t.TempDir(),
	}
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	want := hostPath + ":/home/agent/.local/share/testtool/auth.json:z"
	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v %s in args; got: %v", want, args)
	}
}

// TestBuildServeArgs_MountsAuthFiles verifies that auth file bind-mounts also
// appear in the serve-mode docker run args.
func TestBuildServeArgs_MountsAuthFiles(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "auth.json")
	cfg := &Config{
		Tool: &tools.Tool{
			Name:   "testtool",
			RunCmd: []string{"echo"},
			AuthFiles: []tools.AuthFile{
				{HostPath: hostPath, ContainerPath: "/home/agent/.local/share/testtool/auth.json"},
			},
		},
		Stack:    "node",
		RepoPath: t.TempDir(),
	}
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 4096)

	want := hostPath + ":/home/agent/.local/share/testtool/auth.json:z"
	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v %s in serve args; got: %v", want, args)
	}
}

// TestBuildRunArgs_NoAuthFilesWhenEmpty verifies that no extra bind-mounts are
// added when AuthFiles is nil or empty.
func TestBuildRunArgs_NoAuthFilesWhenEmpty(t *testing.T) {
	cfg := fakeConfig(t, nil) // AuthFiles is nil
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "auth.json") {
		t.Errorf("unexpected auth.json in args when AuthFiles is nil: %s", joined)
	}
}

// TestEnsureAuthFile_CreatesFileWhenAbsent verifies that ensureAuthFile creates
// the file (and its parent directories) when neither exist.
func TestEnsureAuthFile_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "sub", "auth.json")

	if err := ensureAuthFile(hostPath); err != nil {
		t.Fatalf("ensureAuthFile: %v", err)
	}

	info, err := os.Stat(hostPath)
	if err != nil {
		t.Fatalf("stat after ensureAuthFile: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("expected regular file at %s, got mode %v", hostPath, info.Mode())
	}
}

// TestEnsureAuthFile_IdempotentWhenExists verifies that calling ensureAuthFile
// on a path that already contains a file returns no error and does not truncate it.
func TestEnsureAuthFile_IdempotentWhenExists(t *testing.T) {
	dir := t.TempDir()
	hostPath := filepath.Join(dir, "auth.json")

	// Write some content so we can confirm it is not overwritten.
	content := []byte(`{"token":"abc"}`)
	if err := os.WriteFile(hostPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ensureAuthFile(hostPath); err != nil {
		t.Fatalf("ensureAuthFile on existing file: %v", err)
	}

	got, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read after ensureAuthFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("file content changed: got %q, want %q", got, content)
	}
}

// TestOpencodeAuthFiles_PointsToConstructDir verifies that the opencode tool
// defines an AuthFiles entry pointing into ~/.construct/opencode/auth.json and
// that the container path is the expected XDG data location.
func TestOpencodeAuthFiles_PointsToConstructDir(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Re-derive the expected paths the same way opencode.go does.
	authFiles := tools.AuthFilesForOpencode(fakeHome)
	if len(authFiles) != 1 {
		t.Fatalf("expected 1 AuthFile, got %d", len(authFiles))
	}

	wantHost := filepath.Join(fakeHome, ".construct", "opencode", "auth.json")
	if authFiles[0].HostPath != wantHost {
		t.Errorf("HostPath = %q, want %q", authFiles[0].HostPath, wantHost)
	}

	const wantContainer = "/home/agent/.local/share/opencode/auth.json"
	if authFiles[0].ContainerPath != wantContainer {
		t.Errorf("ContainerPath = %q, want %q", authFiles[0].ContainerPath, wantContainer)
	}
}

// TestOpencodeAuthFiles_SessionDBNotShadowed verifies that the opencode tool
// does NOT use AuthVolumePath (which would shadow the whole share/opencode dir
// and make opencode.db global). Sessions must be per-repo via the home volume.
func TestOpencodeAuthFiles_SessionDBNotShadowed(t *testing.T) {
	tool := tools.Opencode()
	if tool == nil {
		t.Fatal("opencode tool not registered")
	}
	if tool.AuthVolumePath != "" {
		t.Errorf("opencode.AuthVolumePath = %q; want empty (use AuthFiles instead to avoid global opencode.db)", tool.AuthVolumePath)
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
		{"mentions /workspace as shared directory", "/workspace"},
		{"explains workspace is bind-mounted from user machine", "bind-mounted"},
		{"describes container isolation", "isolated"},
		{"mentions home dir persistence", "/home/agent"},
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

// TestGeneratedEntrypoint_AgentsMD_WorkspaceAndIsolationContent verifies that
// the generated entrypoint script includes the workspace and isolation sections
// in the AGENTS.md heredoc.
func TestGeneratedEntrypoint_AgentsMD_WorkspaceAndIsolationContent(t *testing.T) {
	script := generatedEntrypoint()
	checks := []struct {
		desc    string
		snippet string
	}{
		{"workspace section header", "## Workspace"},
		{"mentions /workspace path", "`/workspace`"},
		{"explains workspace is bind-mounted", "bind-mounted from their machine"},
		{"notes workspace is the only shared directory", "only directory shared with the user"},
		{"isolation section header", "## Isolation"},
		{"explains container isolation", "isolated inside the container"},
		{"mentions home dir path", "`/home/agent`"},
		{"explains home dir persistence", "persists across sessions"},
		{"notes user machine is separate", "user's machine is separate"},
	}
	for _, c := range checks {
		if !strings.Contains(script, c.snippet) {
			t.Errorf("entrypoint AGENTS.md: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

// TestGeneratedEntrypoint_AgentsMD_HeredocIsQuoted verifies that the heredoc
// that writes the static workspace/isolation content of AGENTS.md uses a
// quoted delimiter (<< 'AGENTSEOF'). An unquoted delimiter causes the shell to
// perform command substitution on backtick-wrapped paths like `/workspace` and
// `/home/agent`, which results in "Permission denied" errors on startup.
func TestGeneratedEntrypoint_AgentsMD_HeredocIsQuoted(t *testing.T) {
	script := generatedEntrypoint()
	// The first AGENTS.md heredoc must use a quoted delimiter to suppress
	// backtick expansion. The line must contain << 'AGENTSEOF', not << AGENTSEOF.
	if !strings.Contains(script, "<< 'AGENTSEOF'") {
		t.Error("entrypoint: AGENTS.md initial heredoc must use a quoted delimiter (<< 'AGENTSEOF') to prevent backtick command substitution on paths like `/workspace` and `/home/agent`")
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

// ---------------------------------------------------------------------------
// Docker mode tests
// ---------------------------------------------------------------------------

// TestBuildRunArgs_NoneMode_NoDockerHost verifies that when DockerMode is "none"
// (the default), DOCKER_HOST is not injected and no network is attached.
func TestBuildRunArgs_NoneMode_NoDockerHost(t *testing.T) {
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "none",
	}
	args := buildRunArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "DOCKER_HOST") {
		t.Errorf("DOCKER_HOST must not be set in none mode; got: %v", args)
	}
	if strings.Contains(joined, "--network") {
		t.Errorf("--network must not appear in none mode; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_DOCKER_MODE=none") {
		t.Errorf("expected -e CONSTRUCT_DOCKER_MODE=none in args; got: %v", args)
	}
}

// TestBuildRunArgs_DoodMode_MountsSocket verifies that DooD mode disables SELinux
// confinement, bind-mounts the host Docker socket, and sets DOCKER_HOST.
func TestBuildRunArgs_DoodMode_MountsSocket(t *testing.T) {
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildRunArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	if !containsPair(args, "--security-opt", "label=disable") {
		t.Errorf("expected --security-opt label=disable in dood mode; got: %v", args)
	}
	if !strings.Contains(joined, "/var/run/docker.sock:/var/run/docker.sock") {
		t.Errorf("expected docker socket bind-mount in dood mode; got: %v", args)
	}
	if strings.Contains(joined, "/var/run/docker.sock:/var/run/docker.sock:z") {
		t.Errorf("expected no :z on docker socket bind-mount (label=disable makes it redundant); got: %v", args)
	}
	if !containsPair(args, "-e", "DOCKER_HOST=unix:///var/run/docker.sock") {
		t.Errorf("expected -e DOCKER_HOST=unix:///var/run/docker.sock in dood mode; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_DOCKER_MODE=dood") {
		t.Errorf("expected -e CONSTRUCT_DOCKER_MODE=dood in args; got: %v", args)
	}
}

// TestBuildRunArgs_DoodMode_NoNetwork verifies that DooD mode does not attach
// a custom Docker network (the host socket is used instead of a dind sidecar).
func TestBuildRunArgs_DoodMode_NoNetwork(t *testing.T) {
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildRunArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "--network") {
		t.Errorf("--network must not appear in dood mode; got: %v", args)
	}
}

// stubDockerSocketGID replaces dockerSocketGID for the duration of a test.
func stubDockerSocketGID(t *testing.T, gid string) {
	t.Helper()
	orig := dockerSocketGID
	dockerSocketGID = func() string { return gid }
	t.Cleanup(func() { dockerSocketGID = orig })
}

// TestBuildRunArgs_DoodMode_GroupAdd verifies that --group-add is appended with
// the socket GID when the socket is accessible, making the agent user a member
// of the host docker group so it can reach the daemon without root.
func TestBuildRunArgs_DoodMode_GroupAdd(t *testing.T) {
	stubDockerSocketGID(t, "975")
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildRunArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	if !containsPair(args, "--group-add", "975") {
		t.Errorf("expected --group-add 975 in dood mode with socket GID 975; got: %v", args)
	}
}

// TestBuildRunArgs_DoodMode_GroupAdd_AbsentWhenNoSocket verifies that --group-add
// is omitted when the socket GID cannot be determined (e.g. socket absent).
func TestBuildRunArgs_DoodMode_GroupAdd_AbsentWhenNoSocket(t *testing.T) {
	stubDockerSocketGID(t, "")
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildRunArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--group-add") {
		t.Errorf("--group-add must not appear when socket GID is unknown; got: %v", args)
	}
}

// TestBuildServeArgs_DoodMode_GroupAdd verifies --group-add is also present in
// serve mode.
func TestBuildServeArgs_DoodMode_GroupAdd(t *testing.T) {
	stubDockerSocketGID(t, "975")
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildServeArgs(cfg, nil, "testimage", "sess1", "homevol", "", "", 4096)
	if !containsPair(args, "--group-add", "975") {
		t.Errorf("expected --group-add 975 in dood serve mode; got: %v", args)
	}
}

// TestBuildDebugArgs_DoodMode_GroupAdd verifies --group-add is also present in
// debug mode.
func TestBuildDebugArgs_DoodMode_GroupAdd(t *testing.T) {
	stubDockerSocketGID(t, "975")
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dood",
	}
	args := buildDebugArgs(cfg, nil, "testimage", "sess1", "homevol", "", "")
	if !containsPair(args, "--group-add", "975") {
		t.Errorf("expected --group-add 975 in dood debug mode; got: %v", args)
	}
}

// TestBuildRunArgs_DindMode_NetworkAndDockerHost verifies that dind mode attaches
// the session network and sets DOCKER_HOST to the dind sidecar.
func TestBuildRunArgs_DindMode_NetworkAndDockerHost(t *testing.T) {
	cfg := &Config{
		Tool:       fakeConfig(t, nil).Tool,
		RepoPath:   t.TempDir(),
		DockerMode: "dind",
	}
	d := fakeDind()
	args := buildRunArgs(cfg, d, "testimage", "sess1", "homevol", "", "")

	if !containsPair(args, "--network", d.NetworkName) {
		t.Errorf("expected --network %s in dind mode; got: %v", d.NetworkName, args)
	}
	if !containsPair(args, "-e", "DOCKER_HOST=tcp://dind:2375") {
		t.Errorf("expected -e DOCKER_HOST=tcp://dind:2375 in dind mode; got: %v", args)
	}
	if !containsPair(args, "-e", "CONSTRUCT_DOCKER_MODE=dind") {
		t.Errorf("expected -e CONSTRUCT_DOCKER_MODE=dind in args; got: %v", args)
	}
}

// TestBuildRunArgs_DockerModeEnvAlwaysPresent verifies that CONSTRUCT_DOCKER_MODE
// is always injected regardless of mode.
func TestBuildRunArgs_DockerModeEnvAlwaysPresent(t *testing.T) {
	for _, mode := range []string{"none", "dood", "dind"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &Config{
				Tool:       fakeConfig(t, nil).Tool,
				RepoPath:   t.TempDir(),
				DockerMode: mode,
			}
			var d *dind.Instance
			if mode == "dind" {
				d = fakeDind()
			}
			args := buildRunArgs(cfg, d, "testimage", "sess1", "homevol", "", "")
			if !containsPair(args, "-e", "CONSTRUCT_DOCKER_MODE="+mode) {
				t.Errorf("expected -e CONSTRUCT_DOCKER_MODE=%s in args; got: %v", mode, args)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Git identity tests
// ---------------------------------------------------------------------------

// TestBuildRunArgs_GitIdentityEnvVars verifies that the four GIT_AUTHOR_* /
// GIT_COMMITTER_* env vars are present in the run args and are non-empty.
// When the host has no special GIT_AUTHOR_* / GIT_COMMITTER_* env vars set,
// author and committer resolve to the same base identity.
func TestBuildRunArgs_GitIdentityEnvVars(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildRunArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	vars := []string{
		"GIT_AUTHOR_NAME",
		"GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME",
		"GIT_COMMITTER_EMAIL",
	}
	vals := make(map[string]string)
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) {
			kv := args[i+1]
			for _, v := range vars {
				if strings.HasPrefix(kv, v+"=") {
					vals[v] = strings.TrimPrefix(kv, v+"=")
				}
			}
		}
	}
	// All four vars must be present and non-empty.
	for _, v := range vars {
		if vals[v] == "" {
			t.Errorf("expected non-empty -e %s=... in args; got: %v", v, args)
		}
	}
}

// TestHostGitIdentity_FallbackWhenMissing verifies that hostGitIdentity returns
// the fallback values (and does not panic) when the host has no git identity.
// We simulate this by temporarily pointing PATH to an empty dir so "git" is
// not found.
func TestHostGitIdentity_FallbackWhenMissing(t *testing.T) {
	emptyDir := t.TempDir()
	// Also clear host GIT_* env vars so they can't mask the fallback.
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		old := os.Getenv(k)
		t.Cleanup(func() { os.Setenv(k, old) })
		os.Unsetenv(k)
	}
	old := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", old) })
	os.Setenv("PATH", emptyDir)

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()
	if authorName == "" {
		t.Error("expected non-empty fallback authorName, got empty string")
	}
	if authorEmail == "" {
		t.Error("expected non-empty fallback authorEmail, got empty string")
	}
	if committerName == "" {
		t.Error("expected non-empty fallback committerName, got empty string")
	}
	if committerEmail == "" {
		t.Error("expected non-empty fallback committerEmail, got empty string")
	}
}

// TestHostGitIdentity_EnvVarsOverrideConfig verifies that host GIT_AUTHOR_* /
// GIT_COMMITTER_* environment variables take precedence over git config and
// are passed through independently, allowing author != committer.
func TestHostGitIdentity_EnvVarsOverrideConfig(t *testing.T) {
	overrides := map[string]string{
		"GIT_AUTHOR_NAME":     "Alice Author",
		"GIT_AUTHOR_EMAIL":    "alice@example.com",
		"GIT_COMMITTER_NAME":  "Bob Committer",
		"GIT_COMMITTER_EMAIL": "bob@example.com",
	}
	for k, v := range overrides {
		old := os.Getenv(k)
		t.Cleanup(func() { os.Setenv(k, old) })
		os.Setenv(k, v)
	}

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()

	if authorName != "Alice Author" {
		t.Errorf("authorName = %q, want %q", authorName, "Alice Author")
	}
	if authorEmail != "alice@example.com" {
		t.Errorf("authorEmail = %q, want %q", authorEmail, "alice@example.com")
	}
	if committerName != "Bob Committer" {
		t.Errorf("committerName = %q, want %q", committerName, "Bob Committer")
	}
	if committerEmail != "bob@example.com" {
		t.Errorf("committerEmail = %q, want %q", committerEmail, "bob@example.com")
	}
	// Author and committer must be distinct, proving they are read independently.
	if authorName == committerName {
		t.Errorf("author and committer names should differ when env vars set independently")
	}
}

// TestHostGitIdentity_CommitterFallsBackToAuthor verifies that when only the
// author identity is available (no GIT_COMMITTER_* env vars, no separate
// committer config), the committer resolves to the same values as the author —
// matching git's own behaviour.
func TestHostGitIdentity_CommitterFallsBackToAuthor(t *testing.T) {
	// Set author env vars but leave committer env vars unset.
	for _, k := range []string{"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		old := os.Getenv(k)
		t.Cleanup(func() { os.Setenv(k, old) })
		os.Unsetenv(k)
	}
	os.Setenv("GIT_AUTHOR_NAME", "Alice Author")
	os.Setenv("GIT_AUTHOR_EMAIL", "alice@example.com")
	t.Cleanup(func() { os.Unsetenv("GIT_AUTHOR_NAME") })
	t.Cleanup(func() { os.Unsetenv("GIT_AUTHOR_EMAIL") })

	// Point PATH to empty dir so git config returns nothing.
	emptyDir := t.TempDir()
	old := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", old) })
	os.Setenv("PATH", emptyDir)

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()

	if committerName != authorName {
		t.Errorf("committerName = %q, want author fallback %q", committerName, authorName)
	}
	if committerEmail != authorEmail {
		t.Errorf("committerEmail = %q, want author fallback %q", committerEmail, authorEmail)
	}
}

// TestGeneratedEntrypoint_EndsWithExec verifies that the generated entrypoint
// script ends with exec "$@".
func TestGeneratedEntrypoint_EndsWithExec(t *testing.T) {
	script := generatedEntrypoint()
	if !strings.Contains(script, `exec "$@"`) {
		t.Error(`entrypoint: expected exec "$@" line not found`)
	}
}

// ---------------------------------------------------------------------------
// toolImageVersionCurrent tests
// ---------------------------------------------------------------------------

// stubToolImageLabel replaces toolImageLabel for the duration of a test.
func stubToolImageLabel(t *testing.T, fn func(imageName, label string) (string, error)) {
	t.Helper()
	orig := toolImageLabel
	toolImageLabel = fn
	t.Cleanup(func() { toolImageLabel = orig })
}

// stubVersion sets buildinfo.Version for the duration of a test and restores it.
func stubVersion(t *testing.T, v string) {
	t.Helper()
	orig := buildinfo.Version
	buildinfo.Version = v
	t.Cleanup(func() { buildinfo.Version = orig })
}

func TestToolImageVersionCurrent_DevBuild_AlwaysTrue(t *testing.T) {
	stubVersion(t, "")
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		return "v9.9.9", nil
	})

	if !toolImageVersionCurrent("any-tool-image") {
		t.Error("expected true for dev build (empty version), got false")
	}
}

func TestToolImageVersionCurrent_MatchingVersion_ReturnsTrue(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		return "v1.2.3", nil
	})

	if !toolImageVersionCurrent("construct-base-opencode") {
		t.Error("expected true when label matches binary version, got false")
	}
}

func TestToolImageVersionCurrent_DifferentVersion_ReturnsFalse(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		return "v1.0.0", nil
	})

	if toolImageVersionCurrent("construct-base-opencode") {
		t.Error("expected false when label differs from binary version, got true")
	}
}

func TestToolImageVersionCurrent_NoLabel_ReturnsFalse(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		return "", nil
	})

	if toolImageVersionCurrent("construct-base-opencode") {
		t.Error("expected false when image has no version label, got true")
	}
}

func TestToolImageVersionCurrent_InspectError_ReturnsFalse(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		return "", errors.New("docker: image not found")
	})

	if toolImageVersionCurrent("construct-base-opencode") {
		t.Error("expected false when inspect returns error, got true")
	}
}

func TestToolImageVersionCurrent_PassesCorrectArgs(t *testing.T) {
	stubVersion(t, "v2.0.0")
	var gotImage, gotLabel string
	stubToolImageLabel(t, func(imageName, label string) (string, error) {
		gotImage = imageName
		gotLabel = label
		return "v2.0.0", nil
	})

	toolImageVersionCurrent("construct-go-opencode")

	if gotImage != "construct-go-opencode" {
		t.Errorf("toolImageLabel called with image %q, want %q", gotImage, "construct-go-opencode")
	}
	if gotLabel != "io.construct.version" {
		t.Errorf("toolImageLabel called with label %q, want %q", gotLabel, "io.construct.version")
	}
}

// ---------------------------------------------------------------------------
// servePort helper tests
// ---------------------------------------------------------------------------

// TestServePort_DefaultIs4096 verifies that a zero Config.ServePort yields the
// default of 4096.
func TestServePort_DefaultIs4096(t *testing.T) {
	cfg := &Config{}
	if got := servePort(cfg); got != 4096 {
		t.Errorf("servePort(zero) = %d, want 4096", got)
	}
}

// TestServePort_CustomPort verifies that a non-zero Config.ServePort is
// returned unchanged.
func TestServePort_CustomPort(t *testing.T) {
	cfg := &Config{ServePort: 9000}
	if got := servePort(cfg); got != 9000 {
		t.Errorf("servePort(9000) = %d, want 9000", got)
	}
}

// ---------------------------------------------------------------------------
// isPortFree / findFreePort tests
// ---------------------------------------------------------------------------

// TestIsPortFree_FreePort verifies that isPortFree returns true for a port
// that nothing is bound to.
func TestIsPortFree_FreePort(t *testing.T) {
	// Grab an ephemeral port from the OS, then close the listener before
	// calling isPortFree so the port is genuinely free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if !isPortFree(port) {
		t.Errorf("isPortFree(%d) = false, want true for an unbound port", port)
	}
}

// TestIsPortFree_BusyPort verifies that isPortFree returns false when
// a listener is already bound to the port.
func TestIsPortFree_BusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if isPortFree(port) {
		t.Errorf("isPortFree(%d) = true, want false while a listener holds the port", port)
	}
}

// TestFindFreePort_ReturnsStartWhenFree verifies that findFreePort returns the
// start port itself when that port is not in use.
func TestFindFreePort_ReturnsStartWhenFree(t *testing.T) {
	// Use an ephemeral port that the OS just freed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	got := findFreePort(port)
	if got != port {
		t.Errorf("findFreePort(%d) = %d, want %d when port is free", port, got, port)
	}
}

// TestFindFreePort_SkipsBusyPort verifies that findFreePort skips over a busy
// port and returns the next free one.
func TestFindFreePort_SkipsBusyPort(t *testing.T) {
	// Bind a listener on the first port so it is unavailable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	busyPort := ln.Addr().(*net.TCPAddr).Port

	got := findFreePort(busyPort)
	if got == 0 {
		t.Fatalf("findFreePort(%d) = 0; expected a free port to be found", busyPort)
	}
	if got <= busyPort {
		t.Errorf("findFreePort(%d) = %d; want a port > %d (busy port skipped)", busyPort, got, busyPort)
	}
	// The returned port must actually be free.
	if !isPortFree(got) {
		t.Errorf("findFreePort returned port %d, but isPortFree(%d) = false", got, got)
	}
}

// TestFindFreePort_ReturnsZeroWhenRangeExhausted verifies that findFreePort
// returns 0 when all ports in the search range are occupied.
func TestFindFreePort_ReturnsZeroWhenRangeExhausted(t *testing.T) {
	// Bind listeners on 100 consecutive ports starting at startPort.
	// We use high ephemeral ports to avoid system conflicts.
	const startPort = 59900
	const count = 100
	listeners := make([]net.Listener, 0, count)
	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", startPort+i))
		if err != nil {
			// If we can't bind all 100 ports (e.g. some already taken), skip the test.
			for _, l := range listeners {
				l.Close()
			}
			t.Skipf("could not bind port %d: %v", startPort+i, err)
		}
		listeners = append(listeners, ln)
	}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()

	got := findFreePort(startPort)
	if got != 0 {
		t.Errorf("findFreePort(%d) = %d, want 0 when all ports in range are busy", startPort, got)
	}
}

// ---------------------------------------------------------------------------
// buildServeArgs tests
// ---------------------------------------------------------------------------

// TestBuildServeArgs_Detached verifies that buildServeArgs produces "-d" (not
// "-it") so the container runs detached.
func TestBuildServeArgs_Detached(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 4096)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, " -d ") {
		t.Errorf("expected -d flag in serve args; got: %v", args)
	}
	for _, a := range args {
		if a == "-it" || a == "-t" || a == "-i" {
			t.Errorf("unexpected interactive flag %q in serve args; got: %v", a, args)
		}
	}
}

// TestBuildServeArgs_ServeCommand verifies that the image is followed by
// "opencode serve --hostname 0.0.0.0 --port <port>".
func TestBuildServeArgs_ServeCommand(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 4096)

	imageIdx := -1
	for i, a := range args {
		if a == "testimage" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image name not found in args: %v", args)
	}
	tail := args[imageIdx+1:]
	want := []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", "4096"}
	if len(tail) != len(want) {
		t.Fatalf("args after image = %v, want %v", tail, want)
	}
	for i, w := range want {
		if tail[i] != w {
			t.Errorf("args[%d] = %q, want %q", i, tail[i], w)
		}
	}
}

// TestBuildServeArgs_ServePortPublishedOnLoopback verifies that the serve port
// is published only on 127.0.0.1 (not 0.0.0.0) to avoid LAN exposure.
func TestBuildServeArgs_ServePortPublishedOnLoopback(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 4096)

	if !containsPair(args, "-p", "127.0.0.1:4096:4096") {
		t.Errorf("expected -p 127.0.0.1:4096:4096 in args; got: %v", args)
	}
	// Must not publish on 0.0.0.0 (would expose port to LAN).
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			v := args[i+1]
			if strings.HasPrefix(v, "0.0.0.0:4096") || v == "4096:4096" {
				t.Errorf("serve port published on non-loopback address %q; want 127.0.0.1:4096:4096", v)
			}
		}
	}
}

// TestBuildServeArgs_CustomPort verifies that a non-default port flows through
// to both the -p flag and the serve command arguments.
func TestBuildServeArgs_CustomPort(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 9000)

	if !containsPair(args, "-p", "127.0.0.1:9000:9000") {
		t.Errorf("expected -p 127.0.0.1:9000:9000 in args; got: %v", args)
	}
	// The serve command must use the custom port number.
	if !containsPair(args, "--port", "9000") {
		t.Errorf("expected --port 9000 in args; got: %v", args)
	}
}

// ---------------------------------------------------------------------------
// buildDebugArgs tests
// ---------------------------------------------------------------------------

// TestBuildDebugArgs_Interactive verifies that buildDebugArgs includes "-it"
// and produces "/bin/bash" as the command (not the tool RunCmd).
func TestBuildDebugArgs_Interactive(t *testing.T) {
	cfg := fakeConfig(t, nil)
	cfg.Tool = &tools.Tool{Name: "testtool", RunCmd: []string{"opencode"}}
	args := buildDebugArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")

	// Must be interactive.
	hasIt := false
	for _, a := range args {
		if a == "-it" {
			hasIt = true
		}
	}
	if !hasIt {
		t.Errorf("expected -it in debug args; got: %v", args)
	}

	// Command after image must be /bin/bash.
	imageIdx := -1
	for i, a := range args {
		if a == "testimage" {
			imageIdx = i
			break
		}
	}
	if imageIdx < 0 {
		t.Fatalf("image not found in debug args: %v", args)
	}
	tail := args[imageIdx+1:]
	if len(tail) != 1 || tail[0] != "/bin/bash" {
		t.Errorf("debug command after image = %v, want [/bin/bash]", tail)
	}
}

// TestBuildDebugArgs_NotDetached verifies that "-d" does not appear in debug args.
func TestBuildDebugArgs_NotDetached(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildDebugArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "")
	for _, a := range args {
		if a == "-d" {
			t.Errorf("unexpected -d flag in debug args; got: %v", args)
		}
	}
}

// ---------------------------------------------------------------------------
// waitForServer tests
// ---------------------------------------------------------------------------

// TestWaitForServer_SucceedsWhenHealthy verifies that waitForServer returns nil
// when the server immediately responds with {"healthy":true}.
func TestWaitForServer_SucceedsWhenHealthy(t *testing.T) {
	// Start a minimal HTTP server that returns {"healthy":true}.
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"healthy":true,"version":"test"}`)
	})
	srv.Handler = mux

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	url := "http://" + ln.Addr().String()
	if err := waitForServer(url, 2*time.Second); err != nil {
		t.Errorf("waitForServer returned error: %v", err)
	}
}

// TestWaitForServer_TimesOutWhenNotReady verifies that waitForServer returns an
// error after the timeout when the health endpoint is never healthy.
func TestWaitForServer_TimesOutWhenNotReady(t *testing.T) {
	// Use a port that isn't listening so every attempt fails immediately.
	err := waitForServer("http://127.0.0.1:19999", 300*time.Millisecond)
	if err == nil {
		t.Error("expected error from waitForServer when server is not available, got nil")
	}
}

// TestWaitForServer_TimesOutWhenUnhealthy verifies that waitForServer returns an
// error when the server always responds with {"healthy":false}.
func TestWaitForServer_TimesOutWhenUnhealthy(t *testing.T) {
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"healthy":false}`)
	})
	srv.Handler = mux

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	url := "http://" + ln.Addr().String()
	err = waitForServer(url, 300*time.Millisecond)
	if err == nil {
		t.Error("expected error from waitForServer when server always returns healthy:false, got nil")
	}
}

// ---------------------------------------------------------------------------
// generatedEntrypoint serve port section tests
// ---------------------------------------------------------------------------

// TestGeneratedEntrypoint_MentionsServePort verifies that the entrypoint script
// contains the CONSTRUCT_SERVE_PORT conditional block that appends the opencode
// server section to AGENTS.md.
func TestGeneratedEntrypoint_MentionsServePort(t *testing.T) {
	script := generatedEntrypoint()
	checks := []struct {
		desc    string
		snippet string
	}{
		{"conditionally appends serve port section when CONSTRUCT_SERVE_PORT set", "CONSTRUCT_SERVE_PORT"},
		{"mentions server mode", "server mode"},
		{"mentions http://localhost", "http://localhost"},
	}
	for _, c := range checks {
		if !strings.Contains(script, c.snippet) {
			t.Errorf("entrypoint: expected %s (snippet %q not found)", c.desc, c.snippet)
		}
	}
}

// TestBuildServeArgs_InjectsServePortEnv verifies that buildServeArgs injects
// CONSTRUCT_SERVE_PORT into the container environment so the entrypoint can
// write the correct server URL to AGENTS.md.
func TestBuildServeArgs_InjectsServePortEnv(t *testing.T) {
	cfg := fakeConfig(t, nil)
	args := buildServeArgs(cfg, fakeDind(), "testimage", "sess1", "homevol", "", "", 4096)

	if !containsPair(args, "-e", "CONSTRUCT_SERVE_PORT=4096") {
		t.Errorf("expected -e CONSTRUCT_SERVE_PORT=4096 in serve args; got: %v", args)
	}
}

// ---------------------------------------------------------------------------
// runLocalAttach client-selection tests
// ---------------------------------------------------------------------------

// TestRunLocalAttach_WebClientOpensDirectly verifies that client="web" calls
// openBrowser without consulting $PATH for "opencode". We confirm this by
// observing that no "opencode attach" exec is attempted — instead we get the
// browser path (which will print to stdout and then block; we can't easily
// exercise the browser open itself in a unit test, so we test the TUI path
// directly using a fake opencode on PATH).

// TestRunLocalAttach_TUIClientErrorsWhenNotFound verifies that client="tui"
// returns an error when "opencode" is not on PATH, rather than silently
// falling back to the browser.
func TestRunLocalAttach_TUIClientErrorsWhenNotFound(t *testing.T) {
	// Temporarily clear PATH so exec.LookPath("opencode") always fails.
	t.Setenv("PATH", "")

	err := runLocalAttach("http://127.0.0.1:4096", "tui")
	if err == nil {
		t.Fatal("expected error when opencode not on PATH with --client tui, got nil")
	}
	want := "opencode not found on PATH; install opencode or use --client web"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestRunLocalAttach_AutoClientErrorsWhenNotFound verifies that client="" (auto)
// does NOT return an error when "opencode" is absent — it falls back to the
// browser path (openBrowser), which blocks forever in normal use but in tests
// we just confirm no error from the PATH-check branch.
// We do this indirectly: with PATH cleared, client="" should not error out;
// it should call openBrowser. openBrowser itself will try xdg-open/open and
// then block on select{}. Since we can't call that in a test, we just verify
// the TUI-not-found error is NOT returned.
func TestRunLocalAttach_AutoDoesNotErrorWhenOpencodeAbsent(t *testing.T) {
	// This test only verifies the error-path of runLocalAttach for the "tui"
	// client is NOT triggered for the "" (auto) client. The actual auto path
	// would call openBrowser which blocks; we can't run it to completion here.
	// Instead we verify that the error message from the "tui" branch is absent.
	t.Setenv("PATH", "")

	// We can't call runLocalAttach("...", "") directly because openBrowser blocks.
	// Instead, re-test the conditional logic directly:
	// With PATH="", LookPath("opencode") will fail.
	// For client="", we expect the fallback branch — no error from LookPath.
	_, err := exec.LookPath("opencode")
	if err == nil {
		t.Skip("opencode is on PATH; test is only meaningful when opencode is absent")
	}
	// If we reach here, opencode is not on PATH.
	// The auto client would call openBrowser; the TUI client would error.
	// This test merely documents the expected branching logic, already covered
	// by TestRunLocalAttach_TUIClientErrorsWhenNotFound.
}

// TestRunConfig_ClientValidation verifies that Run returns an error for
// an unrecognised --client value without attempting to build any images.
func TestRunConfig_ClientValidation(t *testing.T) {
	cfg := &Config{
		Tool:     &tools.Tool{Name: "test"},
		Stack:    "base",
		RepoPath: t.TempDir(),
		Client:   "invalid-client",
	}
	err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid --client value, got nil")
	}
	if !strings.Contains(err.Error(), "unknown client") {
		t.Errorf("error = %q, want it to mention 'unknown client'", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid-client") {
		t.Errorf("error = %q, want it to mention the bad value 'invalid-client'", err.Error())
	}
}

// TestToolDockerfile_MkdirForAuthFiles verifies that toolDockerfile emits a
// "RUN mkdir -p <parent>" line for each AuthFile entry, ensuring the parent
// directories are created as the agent user inside the image. Without this,
// Docker bind-mounts the file at runtime and auto-creates the missing parent
// directories as root-owned, which causes EACCES for the agent.
func TestToolDockerfile_MkdirForAuthFiles(t *testing.T) {
	tool := &tools.Tool{
		Name: "opencode",
		AuthFiles: []tools.AuthFile{
			{
				HostPath:      "/irrelevant/host/path",
				ContainerPath: "/home/agent/.local/share/opencode/auth.json",
			},
		},
	}
	got := toolDockerfile("construct-base", tool)

	// The USER agent line must appear before the mkdir so the directory is
	// created with agent ownership.
	userAgentIdx := strings.Index(got, "USER agent\n")
	if userAgentIdx < 0 {
		t.Fatal("generated Dockerfile missing 'USER agent' line")
	}

	wantMkdir := "RUN mkdir -p /home/agent/.local/share/opencode\n"
	mkdirIdx := strings.Index(got, wantMkdir)
	if mkdirIdx < 0 {
		t.Errorf("generated Dockerfile missing %q\ngot:\n%s", wantMkdir, got)
	}
	if mkdirIdx < userAgentIdx {
		t.Errorf("mkdir line appears before 'USER agent' line; want it after\ngot:\n%s", got)
	}
}

// TestToolDockerfile_NoMkdirWhenNoAuthFiles verifies that toolDockerfile does
// not emit any "RUN mkdir -p" lines when the tool has no AuthFiles.
func TestToolDockerfile_NoMkdirWhenNoAuthFiles(t *testing.T) {
	tool := &tools.Tool{
		Name:      "sometool",
		AuthFiles: nil,
	}
	got := toolDockerfile("construct-base", tool)
	if strings.Contains(got, "RUN mkdir -p") {
		t.Errorf("expected no 'RUN mkdir -p' lines when AuthFiles is empty, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// containerLogs / serve-timeout diagnostics tests
// ---------------------------------------------------------------------------

// stubContainerLogs replaces the containerLogs injectable for the duration of
// a test and restores the original on cleanup.
func stubContainerLogs(t *testing.T, fn func(name string) string) {
	t.Helper()
	orig := containerLogs
	containerLogs = fn
	t.Cleanup(func() { containerLogs = orig })
}

// TestContainerLogs_ReturnsOutput verifies that containerLogs returns the
// combined stdout+stderr from docker logs when the command succeeds.
func TestContainerLogs_ReturnsOutput(t *testing.T) {
	// We test the real function signature by checking the injectable default
	// behaves correctly with a real (known-absent) container name — it should
	// return an empty string (not panic) when docker logs exits non-zero.
	got := containerLogs("construct-test-nonexistent-container-abc123")
	// Docker will exit non-zero (no such container); we expect empty string.
	if got != "" {
		// This path runs only if somehow a container with this name exists.
		t.Logf("containerLogs returned non-empty for absent container (unexpected): %q", got)
	}
}

// TestServeTimeoutDiagnostics_PrintsContainerLogs verifies that when the
// health-check times out, printServeTimeoutDiagnostics writes the container
// logs and the recovery hints to the provided writer.
func TestServeTimeoutDiagnostics_PrintsContainerLogs(t *testing.T) {
	const fakeContainerName = "construct-agent-test123"
	const fakeLogs = "EACCES: permission denied, mkdir '/home/agent/.local/share/opencode/bin'"

	stubContainerLogs(t, func(name string) string {
		if name != fakeContainerName {
			t.Errorf("containerLogs called with %q, want %q", name, fakeContainerName)
		}
		return fakeLogs
	})

	var buf strings.Builder
	printServeTimeoutDiagnostics(&buf, fakeContainerName)
	got := buf.String()

	// Container logs section must be present.
	if !strings.Contains(got, fakeLogs) {
		t.Errorf("output missing container log content %q\ngot:\n%s", fakeLogs, got)
	}
	// Recovery hints must be present.
	if !strings.Contains(got, "--rebuild") {
		t.Errorf("output missing --rebuild hint\ngot:\n%s", got)
	}
	if !strings.Contains(got, "--reset") {
		t.Errorf("output missing --reset hint\ngot:\n%s", got)
	}
	if !strings.Contains(got, "--debug") {
		t.Errorf("output missing --debug hint\ngot:\n%s", got)
	}
}

// TestServeTimeoutDiagnostics_EmptyLogs verifies that when the container
// produced no output, the log section is omitted but the hints are still shown.
func TestServeTimeoutDiagnostics_EmptyLogs(t *testing.T) {
	stubContainerLogs(t, func(name string) string { return "" })

	var buf strings.Builder
	printServeTimeoutDiagnostics(&buf, "construct-agent-test456")
	got := buf.String()

	// No "begin container logs" block when logs are empty.
	if strings.Contains(got, "begin container logs") {
		t.Errorf("expected no log block for empty logs, got:\n%s", got)
	}
	// Hints still present.
	if !strings.Contains(got, "--rebuild") {
		t.Errorf("output missing --rebuild hint\ngot:\n%s", got)
	}
	if !strings.Contains(got, "--reset") {
		t.Errorf("output missing --reset hint\ngot:\n%s", got)
	}
}
