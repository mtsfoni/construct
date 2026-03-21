package client_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/construct-run/construct/internal/client"
)

// echoServer is a minimal fake daemon that responds to requests.
func startEchoServer(t *testing.T, handler func(req map[string]interface{}, conn net.Conn)) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	os.Chmod(sockPath, 0o600) //nolint:errcheck

	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req map[string]interface{}
				dec := json.NewDecoder(c)
				if err := dec.Decode(&req); err != nil {
					return
				}
				handler(req, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return sockPath
}

func writeJSON(conn net.Conn, v interface{}) {
	data, _ := json.Marshal(v)
	data = append(data, '\n')
	conn.Write(data) //nolint:errcheck
}

func TestClient_SimpleEnd(t *testing.T) {
	sockPath := startEchoServer(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":      req["id"],
			"type":    "end",
			"payload": map[string]interface{}{"sessions": []interface{}{}},
		})
	})

	c := client.New(sockPath)
	ctx := context.Background()
	responses, err := c.Do(ctx, "session.list", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	if responses[0].Type != "end" {
		t.Fatalf("expected end, got %s", responses[0].Type)
	}
}

func TestClient_ErrorResponse(t *testing.T) {
	sockPath := startEchoServer(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":      req["id"],
			"type":    "error",
			"payload": map[string]interface{}{"message": "something went wrong"},
		})
	})

	c := client.New(sockPath)
	_, err := c.Do(context.Background(), "session.start", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "something went wrong" {
		t.Fatalf("expected 'something went wrong', got %q", err.Error())
	}
}

func TestClient_DataThenEnd(t *testing.T) {
	sockPath := startEchoServer(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":      req["id"],
			"type":    "data",
			"payload": map[string]interface{}{"type": "progress", "message": "building..."},
		})
		writeJSON(conn, map[string]interface{}{
			"id":      req["id"],
			"type":    "data",
			"payload": map[string]interface{}{"type": "progress", "message": "done"},
		})
		writeJSON(conn, map[string]interface{}{
			"id":      req["id"],
			"type":    "end",
			"payload": map[string]interface{}{"session": map[string]interface{}{"id": "abc"}},
		})
	})

	c := client.New(sockPath)
	var dataCount int
	responses, err := c.Stream(context.Background(), "session.start", map[string]interface{}{}, func(resp client.Response) {
		dataCount++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dataCount != 2 {
		t.Fatalf("expected 2 data callbacks, got %d", dataCount)
	}
	if len(responses) != 1 || responses[0].Type != "end" {
		t.Fatalf("expected end response, got %v", responses)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	sockPath := startEchoServer(t, func(req map[string]interface{}, conn net.Conn) {
		// Never respond — just hold the connection open.
		time.Sleep(10 * time.Second)
	})

	c := client.New(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.Do(ctx, "session.logs", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
}

func TestClient_StreamRaw(t *testing.T) {
	sockPath := startEchoServer(t, func(req map[string]interface{}, conn net.Conn) {
		for i := 0; i < 3; i++ {
			writeJSON(conn, map[string]interface{}{
				"id":      req["id"],
				"type":    "data",
				"payload": map[string]interface{}{"line": "log line", "stream": "stdout"},
			})
		}
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
		})
	})

	c := client.New(sockPath)
	var lines []client.Response
	err := c.StreamRaw(context.Background(), "session.logs", map[string]interface{}{}, func(resp client.Response) error {
		lines = append(lines, resp)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 data + 1 end
	if len(lines) != 4 {
		t.Fatalf("expected 4 frames, got %d", len(lines))
	}
}
