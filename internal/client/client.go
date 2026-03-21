// Package client implements the CLI-side daemon protocol client.
// It sends newline-delimited JSON requests over a Unix socket and streams
// response frames back to the caller.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
)

// Request is the envelope sent to the daemon.
type Request struct {
	ID      string      `json:"id"`
	Command string      `json:"command"`
	Params  interface{} `json:"params"`
}

// Response is a single response frame from the daemon.
type Response struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"` // "data", "error", "end"
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Client connects to the daemon Unix socket and sends requests.
type Client struct {
	socketPath  string
	dialTimeout time.Duration
}

// New creates a new Client targeting the given Unix socket path.
func New(socketPath string) *Client {
	return &Client{
		socketPath:  socketPath,
		dialTimeout: 5 * time.Second,
	}
}

// Do sends a command with params and collects all responses until "end" or "error".
// Progress callbacks are called for "data" frames with a "progress" type payload.
// Returns the final "end" payload on success, or an error.
func (c *Client) Do(ctx context.Context, command string, params interface{}) ([]Response, error) {
	return c.Stream(ctx, command, params, nil)
}

// Stream sends a command and calls onData for each "data" frame.
// Returns the final "end" payload in a 1-element slice, or an error.
func (c *Client) Stream(ctx context.Context, command string, params interface{}, onData func(Response)) ([]Response, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Cancel the conn when context is done (triggers client disconnect).
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	reqID := uuid.New().String()
	req := Request{
		ID:      reqID,
		Command: command,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	var result []Response
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}

		switch resp.Type {
		case "error":
			var payload struct {
				Message string `json:"message"`
			}
			json.Unmarshal(resp.Payload, &payload) //nolint:errcheck
			return nil, fmt.Errorf("%s", payload.Message)
		case "data":
			if onData != nil {
				onData(resp)
			}
		case "end":
			result = append(result, resp)
			return result, nil
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read response: %w", err)
	}
	return result, nil
}

// StreamRaw opens a persistent connection for streaming (e.g. logs --follow).
// It calls onFrame for every response frame until ctx is cancelled, "end" is
// received, or an error occurs.
func (c *Client) StreamRaw(ctx context.Context, command string, params interface{}, onFrame func(Response) error) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	reqID := uuid.New().String()
	req := Request{
		ID:      reqID,
		Command: command,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}

		if resp.Type == "error" {
			var payload struct {
				Message string `json:"message"`
			}
			json.Unmarshal(resp.Payload, &payload) //nolint:errcheck
			return fmt.Errorf("%s", payload.Message)
		}

		if err := onFrame(resp); err != nil {
			return err
		}

		if resp.Type == "end" {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil // context cancelled = normal disconnect
		}
		return fmt.Errorf("read response: %w", err)
	}
	return nil
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.dialTimeout}
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", c.socketPath, err)
	}
	return conn, nil
}
