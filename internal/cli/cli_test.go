package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/construct-run/construct/internal/cli"
)

// startFakeDaemon starts a minimal fake daemon that responds to requests using
// the provided handler function.
func startFakeDaemon(t *testing.T, handler func(req map[string]interface{}, conn net.Conn)) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "daemon.sock")
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
				scanner := bufio.NewScanner(c)
				if !scanner.Scan() {
					return
				}
				var req map[string]interface{}
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
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

func TestLs_Empty(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
			"payload": map[string]interface{}{
				"sessions": []interface{}{},
			},
		})
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Ls(context.Background(), false, &out)
	if err != nil {
		t.Fatalf("ls error: %v", err)
	}
	// Should print header row.
	output := out.String()
	if output == "" {
		t.Fatal("expected output, got empty string")
	}
	if !bytes.Contains(out.Bytes(), []byte("ID")) {
		t.Fatalf("expected header with ID column, got: %q", output)
	}
}

func TestLs_WithSession(t *testing.T) {
	now := time.Now().UTC()
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
			"payload": map[string]interface{}{
				"sessions": []interface{}{
					map[string]interface{}{
						"id":          "aabbccdd-0000-0000-0000-000000000000",
						"repo":        "/home/alice/src/myapp",
						"tool":        "opencode",
						"stack":       "base",
						"docker_mode": "none",
						"status":      "running",
						"web_port":    4096,
						"ports":       []interface{}{},
						"created_at":  now.Format(time.RFC3339),
					},
				},
			},
		})
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Ls(context.Background(), false, &out)
	if err != nil {
		t.Fatalf("ls error: %v", err)
	}
	output := out.String()
	if !bytes.Contains(out.Bytes(), []byte("aabbccdd")) {
		t.Fatalf("expected session ID in output, got: %q", output)
	}
	if !bytes.Contains(out.Bytes(), []byte("myapp")) {
		t.Fatalf("expected repo in output, got: %q", output)
	}
}

func TestLs_JSON(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
			"payload": map[string]interface{}{
				"sessions": []interface{}{},
			},
		})
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Ls(context.Background(), true, &out)
	if err != nil {
		t.Fatalf("ls --json error: %v", err)
	}
	// Output should be valid JSON.
	var result interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("ls --json output is not valid JSON: %v", err)
	}
}

func TestStop(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		cmd, _ := req["command"].(string)
		switch cmd {
		case "session.list":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"sessions": []interface{}{
						map[string]interface{}{
							"id":   "aabbccdd-0000-0000-0000-000000000000",
							"repo": "/home/alice/src/myapp",
						},
					},
				},
			})
		case "session.stop":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"session": map[string]interface{}{
						"id":   "aabbccdd-0000-0000-0000-000000000000",
						"repo": "/home/alice/src/myapp",
					},
				},
			})
		}
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Stop(context.Background(), "/home/alice/src/myapp", &out)
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("stopped")) {
		t.Fatalf("expected 'stopped' in output, got: %q", out.String())
	}
}

func TestCredSet(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
			"payload": map[string]interface{}{
				"stored": true,
				"scope":  "global",
			},
		})
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.CredSet(context.Background(), "ANTHROPIC_API_KEY", "sk-test", "", &out)
	if err != nil {
		t.Fatalf("cred set error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("ANTHROPIC_API_KEY")) {
		t.Fatalf("expected key in output, got: %q", out.String())
	}
}

func TestCredList(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		writeJSON(conn, map[string]interface{}{
			"id":   req["id"],
			"type": "end",
			"payload": map[string]interface{}{
				"credentials": []interface{}{
					map[string]interface{}{
						"key":          "OPENAI_API_KEY",
						"scope":        "global",
						"masked_value": "****",
					},
				},
			},
		})
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.CredList(context.Background(), "", &out)
	if err != nil {
		t.Fatalf("cred list error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("OPENAI_API_KEY")) {
		t.Fatalf("expected key in output, got: %q", out.String())
	}
}

func TestStop_ShortIDPrefix(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		cmd, _ := req["command"].(string)
		switch cmd {
		case "session.list":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"sessions": []interface{}{
						map[string]interface{}{
							"id":   "aabbccdd-0000-0000-0000-000000000000",
							"repo": "/home/alice/src/myapp",
						},
					},
				},
			})
		case "session.stop":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"session": map[string]interface{}{
						"id":   "aabbccdd-0000-0000-0000-000000000000",
						"repo": "/home/alice/src/myapp",
					},
				},
			})
		}
	})

	c := cli.New(sockPath)
	tests := []struct {
		name   string
		prefix string
	}{
		{"two chars", "aa"},
		{"four chars", "aabb"},
		{"eight chars", "aabbccdd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := c.Stop(context.Background(), tt.prefix, &out)
			if err != nil {
				t.Fatalf("stop(%q) error: %v", tt.prefix, err)
			}
			if !bytes.Contains(out.Bytes(), []byte("stopped")) {
				t.Fatalf("expected 'stopped' in output, got: %q", out.String())
			}
		})
	}
}

func TestStop_AmbiguousIDPrefix(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		cmd, _ := req["command"].(string)
		if cmd == "session.list" {
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"sessions": []interface{}{
						map[string]interface{}{"id": "aabb1111-0000-0000-0000-000000000000", "repo": "/a"},
						map[string]interface{}{"id": "aabb2222-0000-0000-0000-000000000000", "repo": "/b"},
					},
				},
			})
		}
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Stop(context.Background(), "aabb", &out)
	if err == nil {
		t.Fatal("expected error for ambiguous prefix, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("ambiguous")) {
		t.Fatalf("expected 'ambiguous' in error, got: %v", err)
	}
}

func TestStop_NoMatchIDPrefix(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		cmd, _ := req["command"].(string)
		if cmd == "session.list" {
			writeJSON(conn, map[string]interface{}{
				"id":      req["id"],
				"type":    "end",
				"payload": map[string]interface{}{"sessions": []interface{}{}},
			})
		}
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	err := c.Stop(context.Background(), "dead", &out)
	if err == nil {
		t.Fatal("expected error for no-match prefix, got nil")
	}
}

func TestDestroy_Force(t *testing.T) {
	sockPath := startFakeDaemon(t, func(req map[string]interface{}, conn net.Conn) {
		cmd, _ := req["command"].(string)
		switch cmd {
		case "session.list":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"sessions": []interface{}{},
				},
			})
		case "session.destroy":
			writeJSON(conn, map[string]interface{}{
				"id":   req["id"],
				"type": "end",
				"payload": map[string]interface{}{
					"destroyed": true,
				},
			})
		}
	})

	c := cli.New(sockPath)
	var out bytes.Buffer
	// Use folder path directly (resolveToIDAndRepo will use repo="")
	err := c.Destroy(context.Background(), "/some/path", true, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("destroy error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("destroyed")) {
		t.Fatalf("expected 'destroyed' in output, got: %q", out.String())
	}
}
