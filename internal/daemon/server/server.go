// Package server implements the daemon's Unix socket server.
// It listens for newline-delimited JSON requests from CLI clients and
// dispatches them to the session manager, auth store, and related packages.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/construct-run/construct/internal/auth"
	"github.com/construct-run/construct/internal/daemon/session"
)

// Server listens on a Unix socket and handles requests.
type Server struct {
	socketPath string
	mgr        *session.Manager
	authStore  *auth.Store
	listener   net.Listener
}

// New creates a new Server.
func New(socketPath string, mgr *session.Manager, authStore *auth.Store) *Server {
	return &Server{
		socketPath: socketPath,
		mgr:        mgr,
		authStore:  authStore,
	}
}

// Start begins listening on the Unix socket. It returns an error if the
// socket cannot be bound. Call Serve() in a goroutine after Start.
func (s *Server) Start() error {
	// Remove stale socket file if present.
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o666); err != nil {
		ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = ln
	return nil
}

// Serve accepts connections and dispatches requests. It blocks until the
// listener is closed.
func (s *Server) Serve(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("accept error: %v", err)
				return
			}
		}
		go s.handleConn(ctx, conn)
	}
}

// Close shuts down the listener.
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
}

// --- protocol types ---

// Request is the envelope for a single CLI request.
type Request struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Params  json.RawMessage `json:"params"`
}

// Response is the envelope for a single response frame.
type Response struct {
	ID      string      `json:"id"`
	Type    string      `json:"type"` // "data", "error", "end"
	Payload interface{} `json:"payload,omitempty"`
}

// errorPayload is the standard error response body.
type errorPayload struct {
	Message string `json:"message"`
}

// --- connection handling ---

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	line := scanner.Bytes()

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeError(conn, "", fmt.Sprintf("malformed request: %v", err))
		return
	}

	// Create a context that is cancelled when the client disconnects.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Detect client disconnect by attempting reads in the background.
	go func() {
		buf := make([]byte, 1)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				cancel()
				return
			}
		}
	}()

	w := &connWriter{conn: conn, id: req.ID}
	s.dispatch(ctx, w, req)
}

// connWriter wraps a net.Conn and frames responses as newline-delimited JSON.
type connWriter struct {
	conn net.Conn
	id   string
}

func (w *connWriter) sendData(payload interface{}) error {
	return writeResponse(w.conn, Response{ID: w.id, Type: "data", Payload: payload})
}

func (w *connWriter) sendEnd(payload interface{}) error {
	return writeResponse(w.conn, Response{ID: w.id, Type: "end", Payload: payload})
}

func (w *connWriter) sendError(msg string) error {
	return writeResponse(w.conn, Response{ID: w.id, Type: "error", Payload: errorPayload{Message: msg}})
}

func writeError(conn net.Conn, id, msg string) {
	writeResponse(conn, Response{ID: id, Type: "error", Payload: errorPayload{Message: msg}}) //nolint:errcheck
}

func writeResponse(conn net.Conn, r Response) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// --- dispatch ---

func (s *Server) dispatch(ctx context.Context, w *connWriter, req Request) {
	switch req.Command {
	case "session.start":
		s.handleSessionStart(ctx, w, req.Params)
	case "session.stop":
		s.handleSessionStop(ctx, w, req.Params)
	case "session.destroy":
		s.handleSessionDestroy(ctx, w, req.Params)
	case "session.list":
		s.handleSessionList(ctx, w, req.Params)
	case "session.logs":
		s.handleSessionLogs(ctx, w, req.Params)
	case "config.cred.set":
		s.handleCredSet(ctx, w, req.Params)
	case "config.cred.unset":
		s.handleCredUnset(ctx, w, req.Params)
	case "config.cred.list":
		s.handleCredList(ctx, w, req.Params)
	default:
		w.sendError(fmt.Sprintf("unknown command: %s", req.Command)) //nolint:errcheck
	}
}

// --- session.start ---

type sessionStartParams struct {
	Repo              string   `json:"repo"`
	Tool              string   `json:"tool"`
	Stack             string   `json:"stack"`
	DockerMode        string   `json:"docker_mode"`
	Ports             []string `json:"ports"`
	Debug             bool     `json:"debug"`
	HostUID           int      `json:"host_uid"`
	HostGID           int      `json:"host_gid"`
	OpenCodeConfigDir string   `json:"opencode_config_dir"`
	OpenCodeDataDir   string   `json:"opencode_data_dir"`
}

type sessionStartResult struct {
	Session interface{} `json:"session"`
	WebURL  string      `json:"web_url,omitempty"`
	TUIHint string      `json:"tui_hint,omitempty"`
	Warning string      `json:"warning,omitempty"`
}

type progressPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (s *Server) handleSessionStart(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p sessionStartParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	progress := func(msg string) {
		w.sendData(progressPayload{Type: "progress", Message: msg}) //nolint:errcheck
	}

	result, err := s.mgr.Start(ctx, session.StartParams{
		Repo:              p.Repo,
		Tool:              p.Tool,
		Stack:             p.Stack,
		DockerMode:        p.DockerMode,
		Ports:             p.Ports,
		Debug:             p.Debug,
		HostUID:           p.HostUID,
		HostGID:           p.HostGID,
		OpenCodeConfigDir: p.OpenCodeConfigDir,
		OpenCodeDataDir:   p.OpenCodeDataDir,
	}, progress)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	w.sendEnd(sessionStartResult{ //nolint:errcheck
		Session: result.Session,
		WebURL:  result.WebURL,
		TUIHint: result.TUIHint,
		Warning: result.Warning,
	})
}

// --- session.stop ---

type sessionRefParams struct {
	SessionID string `json:"session_id"`
	Repo      string `json:"repo"`
}

type sessionStopResult struct {
	Session interface{} `json:"session"`
}

func (s *Server) handleSessionStop(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p sessionRefParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	id, err := s.resolveSessionID(p.SessionID, p.Repo)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	sess, err := s.mgr.Stop(ctx, id)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	w.sendEnd(sessionStopResult{Session: sess}) //nolint:errcheck
}

// --- session.destroy ---

type sessionDestroyResult struct {
	Destroyed bool `json:"destroyed"`
}

func (s *Server) handleSessionDestroy(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p sessionRefParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	id, err := s.resolveSessionID(p.SessionID, p.Repo)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	if err := s.mgr.Destroy(ctx, id); err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	w.sendEnd(sessionDestroyResult{Destroyed: true}) //nolint:errcheck
}

// --- session.list ---

type sessionListResult struct {
	Sessions interface{} `json:"sessions"`
}

func (s *Server) handleSessionList(ctx context.Context, w *connWriter, _ json.RawMessage) {
	sessions := s.mgr.List()
	w.sendEnd(sessionListResult{Sessions: sessions}) //nolint:errcheck
}

// --- session.logs ---

type sessionLogsParams struct {
	SessionID string `json:"session_id"`
	Repo      string `json:"repo"`
	Follow    bool   `json:"follow"`
	Tail      int    `json:"tail"`
}

type logLinePayload struct {
	Timestamp string `json:"timestamp"`
	Line      string `json:"line"`
	Stream    string `json:"stream"`
}

func (s *Server) handleSessionLogs(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p sessionLogsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	id, err := s.resolveSessionID(p.SessionID, p.Repo)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	buf := s.mgr.LogBuffer(id)
	if buf == nil {
		// Session exists but has no buffer yet (never ran, or daemon restart).
		w.sendEnd(nil) //nolint:errcheck
		return
	}

	// Stream buffered lines.
	lines := buf.Lines(p.Tail)
	for _, l := range lines {
		if err := w.sendData(logLinePayload{
			Timestamp: l.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
			Line:      l.Text,
			Stream:    l.Stream,
		}); err != nil {
			return // client disconnected
		}
	}

	if !p.Follow {
		w.sendEnd(nil) //nolint:errcheck
		return
	}

	// Follow mode: stream new lines until context is done or session stops.
	ch, cancel := buf.Follow()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				w.sendEnd(nil) //nolint:errcheck
				return
			}
			if err := w.sendData(logLinePayload{
				Timestamp: line.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
				Line:      line.Text,
				Stream:    line.Stream,
			}); err != nil {
				return
			}
		}
	}
}

// --- config.cred.set ---

type credSetParams struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Folder string `json:"folder"`
}

type credSetResult struct {
	Stored bool   `json:"stored"`
	Scope  string `json:"scope"`
}

func (s *Server) handleCredSet(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p credSetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	if err := s.authStore.Set(p.Key, p.Value, p.Folder); err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	scope := "global"
	if p.Folder != "" {
		scope = "folder"
	}
	w.sendEnd(credSetResult{Stored: true, Scope: scope}) //nolint:errcheck
}

// --- config.cred.unset ---

type credUnsetParams struct {
	Key    string `json:"key"`
	Folder string `json:"folder"`
}

type credUnsetResult struct {
	Removed bool `json:"removed"`
}

func (s *Server) handleCredUnset(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p credUnsetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	if err := s.authStore.Unset(p.Key, p.Folder); err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	w.sendEnd(credUnsetResult{Removed: true}) //nolint:errcheck
}

// --- config.cred.list ---

type credListParams struct {
	Folder string `json:"folder"`
}

type credListItem struct {
	Key         string `json:"key"`
	Scope       string `json:"scope"`
	MaskedValue string `json:"masked_value"`
}

type credListResult struct {
	Credentials []credListItem `json:"credentials"`
}

func (s *Server) handleCredList(ctx context.Context, w *connWriter, raw json.RawMessage) {
	var p credListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		w.sendError(fmt.Sprintf("invalid params: %v", err)) //nolint:errcheck
		return
	}

	creds, err := s.authStore.List(p.Folder)
	if err != nil {
		w.sendError(err.Error()) //nolint:errcheck
		return
	}

	items := make([]credListItem, 0, len(creds))
	for _, c := range creds {
		items = append(items, credListItem{
			Key:         c.Key,
			Scope:       c.Scope,
			MaskedValue: c.MaskedValue,
		})
	}
	if items == nil {
		items = []credListItem{}
	}

	w.sendEnd(credListResult{Credentials: items}) //nolint:errcheck
}

// --- helpers ---

// resolveSessionID resolves a session ID from explicit ID or repo path.
func (s *Server) resolveSessionID(sessionID, repo string) (string, error) {
	if sessionID != "" {
		return sessionID, nil
	}
	if repo == "" {
		return "", fmt.Errorf("session_id or repo must be provided")
	}
	sess := s.mgr.GetByRepo(repo)
	if sess == nil {
		return "", fmt.Errorf("no session found for repo %s", repo)
	}
	return sess.ID, nil
}

// SendProgress is exported for use by the daemon's reconcile/startup code
// to send progress messages over a connection writer. Not used by server
// dispatch directly.
func SendProgress(conn net.Conn, id, msg string) error {
	return writeResponse(conn, Response{
		ID:   id,
		Type: "data",
		Payload: progressPayload{
			Type:    "progress",
			Message: msg,
		},
	})
}
