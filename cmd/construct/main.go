// Command construct is the CLI for the construct agent runner.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/construct-run/construct/internal/bootstrap"
	"github.com/construct-run/construct/internal/cli"
	"github.com/construct-run/construct/internal/config"
	"github.com/construct-run/construct/internal/platform"
	"github.com/construct-run/construct/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	if err := runCLI(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runCLI() error {
	// Global flags
	var (
		globalDebug      bool
		globalSocketPath string
		globalVersion    bool
	)

	// We'll parse global flags manually before the subcommand.
	args := os.Args[1:]
	args, globalDebug, globalSocketPath, globalVersion = parseGlobalFlags(args)

	if globalVersion {
		fmt.Printf("construct %s\n", version.Version)
		return nil
	}

	// Bare invocation or explicit --help/-help: print help and exit (R-UX-6).
	if len(args) == 0 || args[0] == "--help" || args[0] == "-help" {
		printHelp(os.Stdout)
		return nil
	}

	// Resolve the config dir and socket path.
	constructConfigDir := config.ConstructConfigDir()
	if globalSocketPath == "" {
		globalSocketPath = filepath.Join(constructConfigDir, "daemon.sock")
	}

	// Determine subcommand.
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "run", "qs", "ls", "list", "attach", "stop", "destroy", "purge", "logs", "config":
			cmd = args[0]
			if cmd == "list" {
				cmd = "ls"
			}
			args = args[1:]
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Bootstrap daemon (except for commands that don't need it).
	if cmd != "version" && cmd != "purge" {
		// Check host platform requirements (kernel ≥ 5.12, Docker ≥ 25.0).
		dockerVer, err := bootstrap.DockerServerVersion(ctx)
		if err != nil {
			return fmt.Errorf("connect to Docker: %w", err)
		}
		if err := platform.Check(dockerVer); err != nil {
			return err
		}

		sockPath, err := bootstrap.EnsureDaemon(ctx, bootstrap.Options{
			ConstructConfigDir: constructConfigDir,
			Progress:           os.Stderr,
		})
		if err != nil {
			return fmt.Errorf("daemon bootstrap: %w", err)
		}
		globalSocketPath = sockPath
	}

	c := cli.New(globalSocketPath)
	w := os.Stdout
	errW := os.Stderr

	switch cmd {
	case "run":
		flags, err := parseRunFlags(args, globalDebug)
		if err == flag.ErrHelp {
			return nil
		}
		if err != nil {
			return err
		}
		if flags.Folder == "" {
			var err error
			flags.Folder, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
		}
		flags.HostUID = os.Getuid()
		flags.HostGID = os.Getgid()
		flags.OpenCodeConfigDir = config.OpenCodeConfigDir()
		flags.OpenCodeDataDir = config.OpenCodeDataDir()
		if !flags.NoWeb {
			flags.Web = true
		}
		return c.Run(ctx, flags, w, errW)

	case "qs":
		var folder string
		fs := flag.NewFlagSet("qs", flag.ContinueOnError)
		fs.StringVar(&folder, "folder", "", "folder path")
		fs.Parse(args) //nolint:errcheck
		return c.Quickstart(ctx, folder, w, errW)

	case "ls":
		var jsonOutput bool
		fs := flag.NewFlagSet("ls", flag.ContinueOnError)
		fs.BoolVar(&jsonOutput, "json", false, "output as JSON")
		fs.Parse(args) //nolint:errcheck
		return c.Ls(ctx, jsonOutput, w)

	case "attach":
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		return c.Attach(ctx, arg, w, errW)

	case "stop":
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		return c.Stop(ctx, arg, w)

	case "destroy":
		var force bool
		var positional string
		fs := flag.NewFlagSet("destroy", flag.ContinueOnError)
		fs.BoolVar(&force, "force", false, "skip confirmation")
		fs.Parse(args) //nolint:errcheck
		if fs.NArg() > 0 {
			positional = fs.Arg(0)
		}
		return c.Destroy(ctx, positional, force, w, errW)

	case "purge":
		var force bool
		fs := flag.NewFlagSet("purge", flag.ContinueOnError)
		fs.BoolVar(&force, "force", false, "skip confirmation")
		fs.Parse(args) //nolint:errcheck
		return cli.Purge(ctx, constructConfigDir, force, w, errW)

	case "logs":
		var follow bool
		var tail int
		var positional string
		fs := flag.NewFlagSet("logs", flag.ContinueOnError)
		fs.BoolVar(&follow, "follow", false, "follow log output")
		fs.BoolVar(&follow, "f", false, "follow log output (shorthand)")
		fs.IntVar(&tail, "tail", 0, "number of lines to show from end")
		fs.Parse(args) //nolint:errcheck
		if fs.NArg() > 0 {
			positional = fs.Arg(0)
		}
		return c.Logs(ctx, positional, follow, tail, w)

	case "config":
		return runConfigCmd(ctx, c, args, w, errW)

	default:
		return fmt.Errorf("unknown command %q. Run 'construct --help' for usage", cmd)
	}
}

func runConfigCmd(ctx context.Context, c *cli.CLI, args []string, w, errW *os.File) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: construct config cred <set|unset|list>")
	}
	if args[0] != "cred" {
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
	args = args[1:]

	if len(args) == 0 {
		return fmt.Errorf("usage: construct config cred <set|unset|list>")
	}

	switch args[0] {
	case "set":
		args = args[1:]
		if len(args) == 0 {
			return fmt.Errorf("usage: construct config cred set <key> [--folder <path>]")
		}
		key := args[0]
		var folder string
		fs := flag.NewFlagSet("cred-set", flag.ContinueOnError)
		fs.StringVar(&folder, "folder", "", "store under per-folder scope")
		fs.Parse(args[1:]) //nolint:errcheck

		// Read value from stdin (hidden).
		fmt.Fprintf(errW, "Enter value for %s: ", key)
		reader := bufio.NewReader(os.Stdin)
		value, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read value: %w", err)
		}
		value = strings.TrimRight(value, "\r\n")
		return c.CredSet(ctx, key, value, folder, w)

	case "unset":
		args = args[1:]
		if len(args) == 0 {
			return fmt.Errorf("usage: construct config cred unset <key> [--folder <path>]")
		}
		key := args[0]
		var folder string
		fs := flag.NewFlagSet("cred-unset", flag.ContinueOnError)
		fs.StringVar(&folder, "folder", "", "remove from per-folder scope")
		fs.Parse(args[1:]) //nolint:errcheck
		return c.CredUnset(ctx, key, folder, w)

	case "list":
		var folder string
		fs := flag.NewFlagSet("cred-list", flag.ContinueOnError)
		fs.StringVar(&folder, "folder", "", "include folder-specific credentials")
		fs.Parse(args[1:]) //nolint:errcheck
		return c.CredList(ctx, folder, w)

	default:
		return fmt.Errorf("unknown cred subcommand %q", args[0])
	}
}

// parseGlobalFlags strips known global flags from args and returns the rest.
func parseGlobalFlags(args []string) (rest []string, debug bool, socketPath string, ver bool) {
	var i int
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--debug" || arg == "-debug":
			debug = true
			i++
		case arg == "--version" || arg == "-version":
			ver = true
			i++
		case strings.HasPrefix(arg, "--daemon-socket="):
			socketPath = strings.TrimPrefix(arg, "--daemon-socket=")
			i++
		case arg == "--daemon-socket" || arg == "-daemon-socket":
			if i+1 < len(args) {
				socketPath = args[i+1]
				i += 2
			} else {
				i++
			}
		default:
			rest = append(rest, args[i:]...)
			return
		}
	}
	return
}

// parseRunFlags parses flags for the run command.
func parseRunFlags(args []string, globalDebug bool) (cli.RunFlags, error) {
	var flags cli.RunFlags
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.StringVar(&flags.Folder, "folder", "", "folder path")
	fs.StringVar(&flags.Tool, "tool", "opencode", "agent tool")
	fs.StringVar(&flags.Stack, "stack", "base", "stack image")
	fs.StringVar(&flags.DockerMode, "docker", "none", "docker mode")
	fs.BoolVar(&flags.Debug, "debug", globalDebug, "drop into shell instead of starting agent")
	fs.BoolVar(&flags.Web, "web", true, "open web UI in browser")
	fs.BoolVar(&flags.NoWeb, "no-web", false, "disable auto-open of web UI")

	var portFlags multiFlag
	fs.Var(&portFlags, "port", "publish a container port (repeatable)")

	// If the first argument looks like a path (not a flag), extract it as the
	// folder before calling fs.Parse. Go's flag package stops at the first
	// non-flag argument, so "construct run ~/src --port 5000:5000" would
	// otherwise leave "--port 5000:5000" unparsed.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		flags.Folder = args[0]
		args = args[1:]
	}

	if err := fs.Parse(args); err != nil {
		return cli.RunFlags{}, err
	}

	flags.Ports = []string(portFlags)
	// A bare positional argument after flags is also accepted as the folder.
	if flags.Folder == "" && fs.NArg() > 0 {
		flags.Folder = fs.Arg(0)
	}
	return flags, nil
}

// multiFlag is a flag.Value that collects repeated string flags.
type multiFlag []string

func (f *multiFlag) String() string     { return strings.Join(*f, ",") }
func (f *multiFlag) Set(v string) error { *f = append(*f, v); return nil }

// printHelp writes the command summary to w (R-UX-6).
func printHelp(w *os.File) {
	fmt.Fprintln(w, "Usage: construct [global-flags] <command> [flags] [args]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --debug               Enable verbose logging (or drop into shell for 'run')")
	fmt.Fprintln(w, "  --daemon-socket <p>   Override daemon socket path")
	fmt.Fprintln(w, "  --version             Print version and exit")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run       Start or attach to a session for a folder (default command)")
	fmt.Fprintln(w, "  qs        Quickstart: replay last invocation for a folder")
	fmt.Fprintln(w, "  ls        List all sessions")
	fmt.Fprintln(w, "  attach    Attach to a running session")
	fmt.Fprintln(w, "  stop      Stop a running session")
	fmt.Fprintln(w, "  destroy   Permanently destroy a session and all its state")
	fmt.Fprintln(w, "  purge     Remove all construct containers, volumes, and images")
	fmt.Fprintln(w, "  logs      View or stream session log output")
	fmt.Fprintln(w, "  config    Manage credentials (config cred set/unset/list)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'construct <command> --help' for command-specific flags.")
}
