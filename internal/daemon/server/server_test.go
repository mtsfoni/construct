package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/construct-run/construct/internal/auth"
	"github.com/construct-run/construct/internal/daemon/logbuffer"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/daemon/server"
	"github.com/construct-run/construct/internal/daemon/session"
	"github.com/construct-run/construct/internal/quickstart"
)

// --- fakes ---

// fakeDockerClient is a minimal implementation of the docker.Client interface
// that returns success for all operations used in tests.
type fakeDockerClient struct{}

func (f *fakeDockerClient) ContainerCreate(_ context.Context, _ interface{}, _ interface{}, _ interface{}, _ interface{}, name string) (interface{}, error) {
	return nil, nil
}

// We don't need a full fake here; tests rely on the manager being pre-populated
// via the registry directly, bypassing Docker. The server tests focus on
// protocol correctness, not session lifecycle.

// --- helpers ---

func newTestServer(t *testing.T) (*server.Server, string, *session.Manager, *auth.Store) {
	t.Helper()
	dir := t.TempDir()

	reg := registry.New(filepath.Join(dir, "state.json"))
	authStore := auth.NewStore(dir)
	qsStore := quickstart.NewStore(dir)

	// We use a minimal manager with a nil docker client.
	// Tests that call session.start/stop/etc would need a real fake docker client,
	// but for protocol tests we only call list/logs/cred operations which don't
	// require docker.
	mgr := session.NewManager(nil, reg, authStore, qsStore, dir)

	sockPath := filepath.Join(dir, "test.sock")
	srv := server.New(sockPath, mgr, authStore, qsStore)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		srv.Close()
	})
	go srv.Serve(ctx)
	return srv, sockPath, mgr, authStore
}

func sendRequest(t *testing.T, sockPath string, req map[string]interface{}) []map[string]interface{} {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var responses []map[string]interface{}
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var resp map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		responses = append(responses, resp)
		rtype, _ := resp["type"].(string)
		if rtype == "end" || rtype == "error" {
			break
		}
	}
	return responses
}

// --- tests ---

func TestSessionList_Empty(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "test-001",
		"command": "session.list",
		"params":  map[string]interface{}{},
	})

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	resp := responses[0]
	if resp["type"] != "end" {
		t.Fatalf("expected end, got %v", resp["type"])
	}
	if resp["id"] != "test-001" {
		t.Fatalf("expected id=test-001, got %v", resp["id"])
	}
	payload, ok := resp["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected payload map, got %T", resp["payload"])
	}
	sessions, ok := payload["sessions"].([]interface{})
	if !ok {
		t.Fatalf("expected sessions array, got %T", payload["sessions"])
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestUnknownCommand(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "test-002",
		"command": "bogus.command",
		"params":  map[string]interface{}{},
	})

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0]["type"] != "error" {
		t.Fatalf("expected error, got %v", responses[0]["type"])
	}
}

func TestCredSetGetList(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	// Set a credential.
	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "cred-set-001",
		"command": "config.cred.set",
		"params": map[string]interface{}{
			"key":   "ANTHROPIC_API_KEY",
			"value": "sk-ant-test",
		},
	})
	if len(responses) != 1 || responses[0]["type"] != "end" {
		t.Fatalf("expected end response for cred.set, got %v", responses)
	}
	payload := responses[0]["payload"].(map[string]interface{})
	if payload["stored"] != true {
		t.Fatalf("expected stored=true, got %v", payload["stored"])
	}
	if payload["scope"] != "global" {
		t.Fatalf("expected scope=global, got %v", payload["scope"])
	}

	// List credentials.
	responses = sendRequest(t, sockPath, map[string]interface{}{
		"id":      "cred-list-001",
		"command": "config.cred.list",
		"params":  map[string]interface{}{},
	})
	if len(responses) != 1 || responses[0]["type"] != "end" {
		t.Fatalf("expected end response for cred.list, got %v", responses)
	}
	listPayload := responses[0]["payload"].(map[string]interface{})
	creds, ok := listPayload["credentials"].([]interface{})
	if !ok {
		t.Fatalf("expected credentials array, got %T", listPayload["credentials"])
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	c := creds[0].(map[string]interface{})
	if c["key"] != "ANTHROPIC_API_KEY" {
		t.Fatalf("expected key=ANTHROPIC_API_KEY, got %v", c["key"])
	}
	if c["scope"] != "global" {
		t.Fatalf("expected scope=global, got %v", c["scope"])
	}
}

func TestCredUnset(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	// Set then unset.
	sendRequest(t, sockPath, map[string]interface{}{
		"id": "s1", "command": "config.cred.set",
		"params": map[string]interface{}{"key": "OPENAI_API_KEY", "value": "sk-test"},
	})

	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "u1",
		"command": "config.cred.unset",
		"params":  map[string]interface{}{"key": "OPENAI_API_KEY"},
	})
	if len(responses) != 1 || responses[0]["type"] != "end" {
		t.Fatalf("expected end, got %v", responses)
	}
	payload := responses[0]["payload"].(map[string]interface{})
	if payload["removed"] != true {
		t.Fatalf("expected removed=true, got %v", payload["removed"])
	}
}

func TestSessionLogs_NoBuffer(t *testing.T) {
	_, sockPath, mgr, _ := newTestServer(t)

	// Inject a session directly into the registry via the manager's List (can't
	// easily add without docker; just test the "no buffer" path by using a
	// non-existent session ID via repo lookup returning nil).
	_ = mgr

	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "logs-001",
		"command": "session.logs",
		"params": map[string]interface{}{
			"repo": "/nonexistent/path",
		},
	})
	// Should return error because session not found.
	if len(responses) != 1 || responses[0]["type"] != "error" {
		t.Fatalf("expected error for unknown session, got %v", responses)
	}
}

func TestSessionLogs_WithBuffer(t *testing.T) {
	dir := t.TempDir()

	reg := registry.New(filepath.Join(dir, "state.json"))
	authStore := auth.NewStore(dir)
	qsStore := quickstart.NewStore(dir)
	mgr := session.NewManager(nil, reg, authStore, qsStore, dir)

	// Add a session to the registry (no log buffer injected -> empty buffer path).
	sess := &registry.Session{
		ID:            "aabbccdd-0000-0000-0000-000000000000",
		Repo:          "/test/repo",
		Tool:          "opencode",
		Stack:         "base",
		DockerMode:    "none",
		ContainerName: "construct-aabbccdd",
		Status:        registry.StatusRunning,
	}
	if err := reg.Add(sess); err != nil {
		t.Fatalf("add session: %v", err)
	}

	sockPath := filepath.Join(dir, "test.sock")
	srv := server.New(sockPath, mgr, authStore, qsStore)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); srv.Close() })
	go srv.Serve(ctx)

	// Session exists but no log buffer -> expect end immediately.
	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "logs-002",
		"command": "session.logs",
		"params": map[string]interface{}{
			"repo": "/test/repo",
		},
	})
	if len(responses) != 1 || responses[0]["type"] != "end" {
		t.Fatalf("expected immediate end for session with no buffer, got %v", responses)
	}
}

func TestSessionLogs_BufferedLines(t *testing.T) {
	dir := t.TempDir()

	reg := registry.New(filepath.Join(dir, "state.json"))
	authStore := auth.NewStore(dir)
	qsStore := quickstart.NewStore(dir)
	mgr := session.NewManager(nil, reg, authStore, qsStore, dir)

	sess := &registry.Session{
		ID:            "bbccddee-0000-0000-0000-000000000000",
		Repo:          "/test/repo2",
		Tool:          "opencode",
		Stack:         "base",
		DockerMode:    "none",
		ContainerName: "construct-bbccddee",
		Status:        registry.StatusRunning,
	}
	if err := reg.Add(sess); err != nil {
		t.Fatalf("add session: %v", err)
	}

	buf := logbuffer.New(100)
	buf.Append(logbuffer.Line{Text: "hello world", Stream: "stdout"})
	buf.Append(logbuffer.Line{Text: "second line", Stream: "stdout"})
	mgr.InjectLogBuffer(sess.ID, buf)

	sockPath := filepath.Join(dir, "test2.sock")
	srv := server.New(sockPath, mgr, authStore, qsStore)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); srv.Close() })
	go srv.Serve(ctx)

	responses := sendRequest(t, sockPath, map[string]interface{}{
		"id":      "logs-003",
		"command": "session.logs",
		"params": map[string]interface{}{
			"repo": "/test/repo2",
		},
	})

	// Expect 2 data responses + 1 end.
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses (2 data + end), got %d: %v", len(responses), responses)
	}
	if responses[0]["type"] != "data" {
		t.Fatalf("expected first response to be data, got %v", responses[0]["type"])
	}
	if responses[1]["type"] != "data" {
		t.Fatalf("expected second response to be data, got %v", responses[1]["type"])
	}
	if responses[2]["type"] != "end" {
		t.Fatalf("expected third response to be end, got %v", responses[2]["type"])
	}
	p0 := responses[0]["payload"].(map[string]interface{})
	if p0["line"] != "hello world" {
		t.Fatalf("expected 'hello world', got %v", p0["line"])
	}
}

func TestMalformedRequest(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("{not valid json}\n"))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("expected a response")
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["type"] != "error" {
		t.Fatalf("expected error response, got %v", resp["type"])
	}
}

// TestSocketPermissions verifies that the socket is created with 0666 permissions
// so that the host user (non-root) can connect to it from outside the daemon container.
func TestSocketPermissions(t *testing.T) {
	_, sockPath, _, _ := newTestServer(t)

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o666 {
		t.Fatalf("expected socket perm 0666, got %04o", perm)
	}
}
