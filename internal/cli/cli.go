// Package cli implements the construct CLI commands.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	dockervolume "github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"

	"github.com/construct-run/construct/internal/client"
	"github.com/construct-run/construct/internal/config"
	"github.com/construct-run/construct/internal/quickstart"
)

// CLI is the top-level CLI state.
type CLI struct {
	socketPath string
	client     *client.Client
}

// New creates a new CLI instance for the given daemon socket path.
func New(socketPath string) *CLI {
	return &CLI{
		socketPath: socketPath,
		client:     client.New(socketPath),
	}
}

// --- RunFlags holds flags for the run/attach command ---

// RunFlags holds all parameters for the run/attach/qs commands.
type RunFlags struct {
	Folder            string
	Tool              string
	Stack             string
	DockerMode        string
	Ports             []string
	Web               bool // true = open web UI (default)
	NoWeb             bool // --no-web
	Debug             bool
	HostUID           int
	HostGID           int
	OpenCodeConfigDir string
	OpenCodeDataDir   string
}

// Run starts or attaches to a session.
func (c *CLI) Run(ctx context.Context, flags RunFlags, w io.Writer, errW io.Writer) error {
	folder, err := canonicalPath(flags.Folder)
	if err != nil {
		return fmt.Errorf("resolve folder: %w", err)
	}

	params := map[string]interface{}{
		"repo":                folder,
		"tool":                flags.Tool,
		"stack":               flags.Stack,
		"docker_mode":         flags.DockerMode,
		"ports":               flags.Ports,
		"debug":               flags.Debug,
		"host_uid":            flags.HostUID,
		"host_gid":            flags.HostGID,
		"opencode_config_dir": flags.OpenCodeConfigDir,
		"opencode_data_dir":   flags.OpenCodeDataDir,
	}

	var result map[string]interface{}

	err = c.client.StreamRaw(ctx, "session.start", params, func(resp client.Response) error {
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(resp.Payload, &payloadMap); err != nil {
			return nil // ignore parse errors for progress frames
		}
		if resp.Type == "data" {
			// Progress message
			if msg, ok := payloadMap["message"].(string); ok {
				fmt.Fprintf(errW, "  %s\n", msg)
			}
			return nil
		}
		// end
		result = payloadMap
		return nil
	})
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("no response from daemon")
	}

	// Print warning if flags were ignored — legacy; now replaced by settings_conflict prompt.
	// (kept for forward compatibility with older daemons)
	if warn, ok := result["warning"].(string); ok && warn != "" {
		fmt.Fprintf(errW, "warning: %s\n", warn)
	}

	// If the daemon reports a settings conflict, prompt the user.
	if conflict, _ := result["settings_conflict"].(bool); conflict {
		choice, err := promptSettingsConflict(result, flags, errW)
		if err != nil {
			return err
		}
		switch choice {
		case "cancel":
			fmt.Fprintln(errW, "Cancelled.")
			return nil
		case "attach":
			// proceed with the existing session's result as-is
		case "restart":
			// destroy existing session then start fresh
			sess, _ := result["session"].(map[string]interface{})
			sessionID, _ := sess["id"].(string)
			folder, _ := sess["repo"].(string)
			if _, err := c.client.Do(ctx, "session.destroy", map[string]interface{}{
				"session_id": sessionID,
				"repo":       folder,
			}); err != nil {
				return fmt.Errorf("destroy session for restart: %w", err)
			}
			// Re-run with the same flags (which will now create a fresh session).
			return c.Run(ctx, flags, w, errW)
		}
	}

	webURL, _ := result["web_url"].(string)

	if flags.Debug {
		// Debug mode requires an interactive terminal (it drops into a shell).
		if !isTerminal(os.Stdout) {
			fmt.Fprintln(errW, "--debug requires an interactive terminal")
			return fmt.Errorf("--debug requires an interactive terminal")
		}
		// In debug mode, exec directly into the container shell.
		sess, ok := result["session"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("missing session in response")
		}
		containerName, _ := sess["container_name"].(string)
		if containerName == "" {
			return fmt.Errorf("missing container_name in session")
		}
		fmt.Fprintf(errW, "Debug mode: dropping into shell in container %s\n", containerName)
		return execDockerShell(containerName)
	}

	tui, _ := result["tui_hint"].(string)

	if webURL != "" {
		fmt.Fprintf(errW, "Waiting for agent...")
		if err := waitForURL(ctx, webURL, 60*time.Second); err != nil {
			fmt.Fprintf(errW, "\nWarning: agent did not become ready: %v\n", err)
		} else {
			fmt.Fprintf(errW, " ready.\n")
			fmt.Fprintf(w, "Web UI: %s\n", webURL)
			if !flags.NoWeb && flags.Web {
				openBrowser(webURL)
			}
		}
	}

	if tui != "" {
		fmt.Fprintf(w, "Attach TUI: %s\n", tui)
	}

	return nil
}

// Attach connects to an existing session.
func (c *CLI) Attach(ctx context.Context, sessionIDOrFolder string, w io.Writer, errW io.Writer) error {
	folder, id, err := c.resolveArg(ctx, sessionIDOrFolder)
	if err != nil {
		return err
	}

	if folder == "" && id != "" {
		// Look up the folder from the ID via session.list.
		sessions, err := c.listSessions(ctx)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, id) {
				folder = s.Repo
				id = s.ID
				break
			}
		}
		if folder == "" {
			return fmt.Errorf("no session found for id %s. Use 'construct run' to start one", id)
		}
	}

	if folder == "" {
		return fmt.Errorf("no session found for %s. Use 'construct run' to start one", sessionIDOrFolder)
	}

	// Verify session exists.
	sessions, err := c.listSessions(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, s := range sessions {
		if s.Repo == folder || s.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no session found for %s. Use 'construct run' to start one", sessionIDOrFolder)
	}

	// Send session.start which will attach to existing or restart stopped session.
	flags := RunFlags{
		Folder:            folder,
		HostUID:           os.Getuid(),
		HostGID:           os.Getgid(),
		OpenCodeConfigDir: config.OpenCodeConfigDir(),
		OpenCodeDataDir:   config.OpenCodeDataDir(),
		Web:               true,
	}
	return c.Run(ctx, flags, w, errW)
}

// Quickstart replays the last invocation for the current folder.
func (c *CLI) Quickstart(ctx context.Context, folder string, w io.Writer, errW io.Writer) error {
	if folder == "" {
		var err error
		folder, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	canonical, err := canonicalPath(folder)
	if err != nil {
		return fmt.Errorf("resolve folder: %w", err)
	}

	stateDir := config.ConstructConfigDir()
	qsStore := quickstart.NewStore(stateDir)
	rec, err := qsStore.Load(canonical)
	if err != nil {
		if err == quickstart.ErrNoRecord {
			return fmt.Errorf("no quickstart record for %s. Run 'construct' first to create one", canonical)
		}
		return fmt.Errorf("load quickstart record: %w", err)
	}

	flags := RunFlags{
		Folder:            canonical,
		Tool:              rec.Tool,
		Stack:             rec.Stack,
		DockerMode:        rec.DockerMode,
		Ports:             rec.Ports,
		Web:               true,
		HostUID:           os.Getuid(),
		HostGID:           os.Getgid(),
		OpenCodeConfigDir: config.OpenCodeConfigDir(),
		OpenCodeDataDir:   config.OpenCodeDataDir(),
	}
	return c.Run(ctx, flags, w, errW)
}

// Ls lists all sessions.
func (c *CLI) Ls(ctx context.Context, jsonOutput bool, w io.Writer) error {
	sessions, err := c.listSessions(ctx)
	if err != nil {
		return err
	}

	if jsonOutput {
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal sessions: %w", err)
		}
		fmt.Fprintf(w, "%s\n", data)
		return nil
	}

	isTTY := isTerminal(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO\tTOOL\tSTACK\tDOCKER\tSTATUS\tPORTS\tURL\tAGE")

	for _, s := range sessions {
		shortID := s.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		ports := formatPorts(s.Ports)
		url := ""
		if s.Status == "running" && s.WebPort > 0 {
			url = fmt.Sprintf("http://localhost:%d", s.WebPort)
		}

		age := formatAge(s.CreatedAt)
		statusStr := s.Status
		if isTTY {
			switch s.Status {
			case "running":
				statusStr = "\033[32mrunning\033[0m"
			case "stopped":
				statusStr = "\033[33mstopped\033[0m"
			}
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID, s.Repo, s.Tool, s.Stack, s.DockerMode,
			statusStr, ports, url, age)
	}
	tw.Flush()
	return nil
}

// Stop stops a session.
func (c *CLI) Stop(ctx context.Context, sessionIDOrFolder string, w io.Writer) error {
	id, repo, err := c.resolveToIDAndRepo(ctx, sessionIDOrFolder)
	if err != nil {
		return err
	}

	var result map[string]interface{}
	responses, err := c.client.Do(ctx, "session.stop", map[string]interface{}{
		"session_id": id,
		"repo":       repo,
	})
	if err != nil {
		return err
	}
	if len(responses) > 0 {
		json.Unmarshal(responses[0].Payload, &result) //nolint:errcheck
	}

	sess, _ := result["session"].(map[string]interface{})
	if sess != nil {
		repoStr, _ := sess["repo"].(string)
		fmt.Fprintf(w, "Session for %s stopped.\n", repoStr)
	} else {
		fmt.Fprintf(w, "Session stopped.\n")
	}
	return nil
}

// Destroy destroys a session.
func (c *CLI) Destroy(ctx context.Context, sessionIDOrFolder string, force bool, w io.Writer, errW io.Writer) error {
	id, repo, err := c.resolveToIDAndRepo(ctx, sessionIDOrFolder)
	if err != nil {
		return err
	}

	displayName := repo
	if displayName == "" {
		displayName = id
	}

	if !force {
		fmt.Fprintf(errW, "Destroy session for %s? This cannot be undone. [y/N] ", displayName)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(w, "Aborted.")
			return nil
		}
	}

	_, err = c.client.Do(ctx, "session.destroy", map[string]interface{}{
		"session_id": id,
		"repo":       repo,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Session for %s destroyed.\n", displayName)
	return nil
}

// Logs streams or displays session output.
func (c *CLI) Logs(ctx context.Context, sessionIDOrFolder string, follow bool, tail int, w io.Writer) error {
	id, repo, err := c.resolveToIDAndRepo(ctx, sessionIDOrFolder)
	if err != nil {
		return err
	}
	return c.streamLogs(ctx, id, repo, follow, tail, w)
}

func (c *CLI) streamLogs(ctx context.Context, id, repo string, follow bool, tail int, w io.Writer) error {
	params := map[string]interface{}{
		"session_id": id,
		"repo":       repo,
		"follow":     follow,
		"tail":       tail,
	}

	return c.client.StreamRaw(ctx, "session.logs", params, func(resp client.Response) error {
		if resp.Type == "end" {
			return nil
		}
		if resp.Type != "data" {
			return nil
		}
		var line struct {
			Timestamp string `json:"timestamp"`
			Line      string `json:"line"`
			Stream    string `json:"stream"`
		}
		if err := json.Unmarshal(resp.Payload, &line); err != nil {
			return nil
		}
		fmt.Fprintf(w, "%s\n", line.Line)
		return nil
	})
}

// CredSet stores a credential.
func (c *CLI) CredSet(ctx context.Context, key, value, folder string, w io.Writer) error {
	var result map[string]interface{}
	responses, err := c.client.Do(ctx, "config.cred.set", map[string]interface{}{
		"key":    key,
		"value":  value,
		"folder": folder,
	})
	if err != nil {
		return err
	}
	if len(responses) > 0 {
		json.Unmarshal(responses[0].Payload, &result) //nolint:errcheck
	}

	scope, _ := result["scope"].(string)
	if scope == "" {
		scope = "global"
	}
	fmt.Fprintf(w, "Credential %s stored (%s scope).\n", key, scope)
	return nil
}

// CredUnset removes a credential.
func (c *CLI) CredUnset(ctx context.Context, key, folder string, w io.Writer) error {
	_, err := c.client.Do(ctx, "config.cred.unset", map[string]interface{}{
		"key":    key,
		"folder": folder,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Credential %s removed.\n", key)
	return nil
}

// CredList lists credentials.
func (c *CLI) CredList(ctx context.Context, folder string, w io.Writer) error {
	responses, err := c.client.Do(ctx, "config.cred.list", map[string]interface{}{
		"folder": folder,
	})
	if err != nil {
		return err
	}
	var result map[string]interface{}
	if len(responses) > 0 {
		json.Unmarshal(responses[0].Payload, &result) //nolint:errcheck
	}

	creds, _ := result["credentials"].([]interface{})
	if len(creds) == 0 {
		fmt.Fprintln(w, "No credentials stored.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tSCOPE\tVALUE")
	for _, raw := range creds {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		key, _ := c["key"].(string)
		scope, _ := c["scope"].(string)
		masked, _ := c["masked_value"].(string)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", key, scope, masked)
	}
	tw.Flush()
	return nil
}

// --- helpers ---

// promptSettingsConflict prints a conflict summary and prompts the user to
// choose restart, attach, or cancel. Returns "restart", "attach", or "cancel".
// If stdin is not a TTY, returns "cancel".
func promptSettingsConflict(result map[string]interface{}, flags RunFlags, errW io.Writer) (string, error) {
	sess, _ := result["session"].(map[string]interface{})

	existing := struct {
		tool, stack, docker string
		ports               []interface{}
	}{
		tool:   strVal(sess, "tool"),
		stack:  strVal(sess, "stack"),
		docker: strVal(sess, "docker_mode"),
		ports:  sliceVal(sess, "ports"),
	}

	fmt.Fprintf(errW, "Session for %s is already running with different settings:\n", strVal(sess, "repo"))
	printConflictField(errW, "tool", existing.tool, flags.Tool)
	printConflictField(errW, "stack", existing.stack, flags.Stack)
	printConflictField(errW, "docker", existing.docker, flags.DockerMode)

	existingPorts := formatPortMappings(existing.ports)
	requestedPorts := strings.Join(flags.Ports, ",")
	if requestedPorts == "" {
		requestedPorts = "(none)"
	}
	if existingPorts == "" {
		existingPorts = "(none)"
	}
	label := "same"
	if existingPorts != requestedPorts {
		label = "differ"
	}
	fmt.Fprintf(errW, "  ports:  %-20s (requested: %-20s) [%s]\n", existingPorts, requestedPorts, label)

	// Non-interactive: cancel.
	if !isTerminal(os.Stderr) {
		fmt.Fprintln(errW, "Non-interactive: use 'construct destroy' then 'construct run' to restart with new settings.")
		return "cancel", nil
	}

	fmt.Fprint(errW, "[r] restart with new settings  [a] attach to existing  [c] cancel\nChoice: ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	switch answer {
	case "r", "restart":
		return "restart", nil
	case "a", "attach":
		return "attach", nil
	default:
		return "cancel", nil
	}
}

func printConflictField(w io.Writer, label, existing, requested string) {
	diff := "same"
	if existing != requested {
		diff = "differ"
	}
	fmt.Fprintf(w, "  %-8s%-20s (requested: %-20s) [%s]\n", label+":", existing, requested, diff)
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func sliceVal(m map[string]interface{}, key string) []interface{} {
	v, _ := m[key].([]interface{})
	return v
}

func formatPortMappings(ports []interface{}) string {
	parts := make([]string, 0, len(ports))
	for _, raw := range ports {
		pm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		hp, _ := pm["host_port"].(float64)
		cp, _ := pm["container_port"].(float64)
		parts = append(parts, fmt.Sprintf("%d:%d", int(hp), int(cp)))
	}
	return strings.Join(parts, ",")
}

// sessionRecord is a lightweight representation of a session for client-side use.
type sessionRecord struct {
	ID            string        `json:"id"`
	Repo          string        `json:"repo"`
	Tool          string        `json:"tool"`
	Stack         string        `json:"stack"`
	DockerMode    string        `json:"docker_mode"`
	Debug         bool          `json:"debug"`
	Ports         []portMapping `json:"ports"`
	WebPort       int           `json:"web_port"`
	ContainerName string        `json:"container_name"`
	HostUID       int           `json:"host_uid"`
	HostGID       int           `json:"host_gid"`
	Status        string        `json:"status"`
	CreatedAt     time.Time     `json:"created_at"`
}

type portMapping struct {
	HostPort      int `json:"host_port"`
	ContainerPort int `json:"container_port"`
}

func (c *CLI) listSessions(ctx context.Context) ([]sessionRecord, error) {
	responses, err := c.client.Do(ctx, "session.list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	if len(responses) == 0 {
		return nil, nil
	}
	var result struct {
		Sessions []sessionRecord `json:"sessions"`
	}
	if err := json.Unmarshal(responses[0].Payload, &result); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	return result.Sessions, nil
}

// resolveArg resolves a session-id-or-folder argument per the spec.
// Returns (folder, id, error). One of folder or id may be empty.
func (c *CLI) resolveArg(ctx context.Context, arg string) (folder, id string, err error) {
	if arg == "" {
		folder, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("get working directory: %w", err)
		}
		folder, err = canonicalPath(folder)
		return folder, "", err
	}

	// Explicit path
	if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
		folder, err = canonicalPath(arg)
		return folder, "", err
	}

	// Looks like UUID or 8+ hex chars → treat as ID prefix.
	if isHexPrefix(arg) {
		return "", arg, nil
	}

	// Otherwise try as folder first, then as ID prefix.
	folder, err = canonicalPath(arg)
	if err == nil {
		return folder, "", nil
	}
	return "", arg, nil
}

// resolveToIDAndRepo resolves a session-id-or-folder to a (session_id, repo) pair
// for use in daemon requests. At least one will be non-empty.
func (c *CLI) resolveToIDAndRepo(ctx context.Context, arg string) (id, repo string, err error) {
	folder, idPrefix, err := c.resolveArg(ctx, arg)
	if err != nil {
		return "", "", err
	}
	if folder != "" {
		return "", folder, nil
	}
	// We have an ID prefix — resolve to full ID if possible.
	if idPrefix != "" {
		sessions, err := c.listSessions(ctx)
		if err != nil {
			return "", "", err
		}
		var matches []sessionRecord
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, idPrefix) {
				matches = append(matches, s)
			}
		}
		switch len(matches) {
		case 0:
			return "", "", fmt.Errorf("no session found for id prefix %q", idPrefix)
		case 1:
			return matches[0].ID, "", nil
		default:
			return "", "", fmt.Errorf("ambiguous session ID prefix %q: matches %d sessions", idPrefix, len(matches))
		}
	}
	return "", "", fmt.Errorf("could not resolve session from %q", arg)
}

func canonicalPath(p string) (string, error) {
	if p == "" {
		p = "."
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// If EvalSymlinks fails (path doesn't exist), return the Abs path.
		return abs, nil
	}
	return resolved, nil
}

func isHexPrefix(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-') {
			return false
		}
	}
	return true
}

func formatPorts(ports []portMapping) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	return strings.Join(parts, "\n")
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours >= 24 {
		days := hours / 24
		h := hours % 24
		return fmt.Sprintf("%dd %dh", days, h)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

func openBrowser(url string) {
	// Best-effort; ignore errors.
	exec.Command("xdg-open", url).Start() //nolint:errcheck
}

func execDockerShell(containerName string) error {
	cmd := exec.Command("docker", "exec", "-it", containerName, "/bin/bash")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- purge ---

// Purge removes all construct Docker resources (containers, volumes, images)
// and the daemon state directory. Auth credentials are preserved (R-LIFE-5).
// It operates directly against the Docker daemon — no construct daemon needed.
func Purge(ctx context.Context, constructConfigDir string, force bool, w io.Writer, errW io.Writer) error {
	if !force {
		fmt.Fprint(errW, "Purge all construct containers, volumes, and images? This cannot be undone. [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(w, "Aborted.")
			return nil
		}
	}

	dc, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("connect to Docker: %w", err)
	}
	defer dc.Close() //nolint:errcheck

	var removedContainers, removedVolumes, removedImages int

	// --- containers ---
	containers, err := dc.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	for _, c := range containers {
		for _, name := range c.Names {
			// Docker prefixes names with "/"
			trimmed := strings.TrimPrefix(name, "/")
			if strings.HasPrefix(trimmed, "construct-") {
				fmt.Fprintf(w, "Removing container %s...\n", trimmed)
				_ = dc.ContainerStop(ctx, c.ID, container.StopOptions{})
				if err := dc.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
					fmt.Fprintf(errW, "warning: remove container %s: %v\n", trimmed, err)
				} else {
					removedContainers++
				}
				break
			}
		}
	}

	// --- volumes ---
	volumeList, err := dc.VolumeList(ctx, dockervolume.ListOptions{})
	if err != nil {
		return fmt.Errorf("list volumes: %w", err)
	}
	for _, v := range volumeList.Volumes {
		if strings.HasPrefix(v.Name, "construct-") {
			fmt.Fprintf(w, "Removing volume %s...\n", v.Name)
			if err := dc.VolumeRemove(ctx, v.Name, true); err != nil {
				fmt.Fprintf(errW, "warning: remove volume %s: %v\n", v.Name, err)
			} else {
				removedVolumes++
			}
		}
	}

	// --- images ---
	imageList, err := dc.ImageList(ctx, dockerimage.ListOptions{All: false})
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	for _, img := range imageList {
		for _, tag := range img.RepoTags {
			repo := strings.SplitN(tag, ":", 2)[0]
			if strings.HasPrefix(repo, "construct-") {
				fmt.Fprintf(w, "Removing image %s...\n", tag)
				if _, err := dc.ImageRemove(ctx, img.ID, dockerimage.RemoveOptions{Force: true}); err != nil {
					fmt.Fprintf(errW, "warning: remove image %s: %v\n", tag, err)
				} else {
					removedImages++
				}
				break
			}
		}
	}

	// --- state dirs (sessions + quickstart; credentials preserved) ---
	for _, subdir := range []string{"sessions", "quickstart"} {
		dir := filepath.Join(constructConfigDir, subdir)
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(errW, "warning: remove %s: %v\n", dir, err)
		}
	}
	// Remove daemon state file so stale session records don't persist.
	stateFile := filepath.Join(constructConfigDir, "daemon-state.json")
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(errW, "warning: remove daemon-state.json: %v\n", err)
	}

	fmt.Fprintf(w, "Purge complete: %d container(s), %d volume(s), %d image(s) removed.\n",
		removedContainers, removedVolumes, removedImages)
	fmt.Fprintln(w, "Auth credentials preserved. Run 'construct run' to start fresh.")
	return nil
}

// waitForURL polls url with HTTP GET every 250 ms until it responds with any
// status code, or the timeout elapses. The session keeps running regardless.
func waitForURL(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	hc := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
