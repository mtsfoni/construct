package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mtsfoni/construct/internal/buildinfo"
	"github.com/mtsfoni/construct/internal/config"
	"github.com/mtsfoni/construct/internal/runner"
	"github.com/mtsfoni/construct/internal/stacks"
	"github.com/mtsfoni/construct/internal/tools"
)

// portFlag is a repeatable --port flag value. Each call to Set appends one
// "host:container" (or bare "port") mapping, allowing:
//
//	construct --port 3000 --port 8080:8080 ...
type portFlag []string

func (p *portFlag) String() string { return strings.Join(*p, ", ") }
func (p *portFlag) Set(v string) error {
	*p = append(*p, v)
	return nil
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		v := buildinfo.Version
		if v == "" {
			v = "dev"
		}
		fmt.Println("construct " + v)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "config" {
		runConfig(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "qs" {
		runQuickstart(os.Args[2:])
		return
	}
	runAgent(os.Args[1:])
}

// runAgent is the original construct behaviour: build images and launch the agent.
func runAgent(args []string) {
	allTools := tools.All()
	sort.Strings(allTools)
	allStacks := stacks.All()

	fs := flag.NewFlagSet("construct", flag.ExitOnError)
	toolName := fs.String("tool", "", "AI tool to run: "+strings.Join(allTools, ", "))
	stackName := fs.String("stack", "base", "Stack image to use: "+strings.Join(allStacks, ", "))
	rebuild := fs.Bool("rebuild", false, "Force rebuild of the stack and tool images")
	debug := fs.Bool("debug", false, "Start an interactive shell instead of the agent (for troubleshooting)")
	reset := fs.Bool("reset", false, "Wipe and re-seed the agent home volume before starting")
	mcp := fs.Bool("mcp", false, "Activate MCP servers (e.g. @playwright/mcp); requires --stack ui for browser automation")
	dockerMode := fs.String("docker", "none", "Docker access mode: none (default, no Docker), dood (Docker-outside-of-Docker via host socket), dind (Docker-in-Docker sidecar)")
	var ports portFlag
	fs.Var(&ports, "port", "Publish a container port to the host (repeatable): --port 3000 --port 8080:8080")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: construct --tool <tool> [--stack <stack>] [--docker <mode>] [--rebuild] [--reset] [--debug] [--mcp] [--port <port>] [path]\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n")
		fmt.Fprintf(os.Stderr, "  config    Manage credential environment variables\n")
		fmt.Fprintf(os.Stderr, "  qs        Re-run the last tool/stack used in the current repo\n\n")
		fmt.Fprintf(os.Stderr, "Other flags:\n")
		fmt.Fprintf(os.Stderr, "  --version  Print the construct version and exit\n\n")
		fmt.Fprintf(os.Stderr, "Available tools:\n")
		for _, t := range allTools {
			fmt.Fprintf(os.Stderr, "  %s\n", t)
		}
		fmt.Fprintf(os.Stderr, "\nAvailable stacks:\n")
		for _, s := range allStacks {
			fmt.Fprintf(os.Stderr, "  %s\n", s)
		}
		fmt.Fprintf(os.Stderr, "\nDocker modes:\n")
		fmt.Fprintf(os.Stderr, "  none   No Docker access inside the agent container (default)\n")
		fmt.Fprintf(os.Stderr, "  dood   Docker-outside-of-Docker: bind-mounts the host socket (/var/run/docker.sock)\n")
		fmt.Fprintf(os.Stderr, "  dind   Docker-in-Docker: starts a privileged dind sidecar container\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack dotnet /path/to/repo\n")
		fmt.Fprintf(os.Stderr, "  construct --tool copilot --stack base .\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack go ~/projects/myapp\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack ui --mcp --port 3000 --port 8080 .\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --docker dood .\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --docker dind .\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *toolName == "" {
		fmt.Fprintln(os.Stderr, "error: --tool is required")
		fs.Usage()
		os.Exit(1)
	}

	tool, err := tools.Get(*toolName)
	if err != nil {
		log.Fatal(err)
	}

	if !stacks.IsValid(*stackName) {
		log.Fatalf("unknown stack %q; supported stacks: %s", *stackName, strings.Join(allStacks, ", "))
	}

	switch *dockerMode {
	case "none", "dood", "dind":
		// valid
	default:
		log.Fatalf("unknown docker mode %q; supported modes: none, dood, dind", *dockerMode)
	}

	repoPath := "."
	if fs.NArg() > 0 {
		repoPath = fs.Arg(0)
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		log.Fatalf("resolve path: %v", err)
	}
	if _, err := os.Stat(absRepoPath); os.IsNotExist(err) {
		log.Fatalf("path does not exist: %s", absRepoPath)
	}

	// Persist so `construct qs` can replay this invocation.
	if err := config.SaveLastUsed(absRepoPath, *toolName, *stackName, *mcp, []string(ports), *dockerMode); err != nil {
		log.Printf("warning: could not save last-used settings: %v", err)
	}

	if err := runner.Run(&runner.Config{
		Tool:       tool,
		Stack:      *stackName,
		RepoPath:   absRepoPath,
		Rebuild:    *rebuild,
		Debug:      *debug,
		Reset:      *reset,
		MCP:        *mcp,
		Ports:      []string(ports),
		DockerMode: *dockerMode,
	}); err != nil {
		log.Fatal(err)
	}
}

// runConfig handles the "construct config" subcommand.
func runConfig(args []string) {
	configUsage := func() {
		fmt.Fprintf(os.Stderr, "Usage: construct config <set|unset|list> [--local] [KEY [VALUE]]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  set KEY VALUE   Write or update a credential\n")
		fmt.Fprintf(os.Stderr, "  unset KEY       Remove a credential\n")
		fmt.Fprintf(os.Stderr, "  list            Show all configured keys (values are masked)\n\n")
		fmt.Fprintf(os.Stderr, "Flag (placed after the command name):\n")
		fmt.Fprintf(os.Stderr, "  --local         Operate on .construct/.env in the current directory\n")
	}

	if len(args) == 0 {
		configUsage()
		os.Exit(1)
	}

	subcmd := args[0]
	rest := args[1:]

	// Each sub-command gets its own FlagSet so --local can follow the command name.
	fs := flag.NewFlagSet("construct config "+subcmd, flag.ExitOnError)
	local := fs.Bool("local", false, "operate on .construct/.env in the current directory instead of ~/.construct/.env")
	if err := fs.Parse(rest); err != nil {
		os.Exit(1)
	}

	envFile, err := targetEnvFile(*local)
	if err != nil {
		log.Fatal(err)
	}

	switch subcmd {
	case "set":
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "error: usage: construct config set [--local] KEY VALUE")
			os.Exit(1)
		}
		key, value := fs.Arg(0), fs.Arg(1)
		if err := config.Set(envFile, key, value); err != nil {
			log.Fatalf("config set: %v", err)
		}
		fmt.Printf("Set %s in %s\n", key, envFile)

	case "unset":
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "error: usage: construct config unset [--local] KEY")
			os.Exit(1)
		}
		key := fs.Arg(0)
		if err := config.Unset(envFile, key); err != nil {
			log.Fatalf("config unset: %v", err)
		}
		fmt.Printf("Unset %s from %s\n", key, envFile)

	case "list":
		m, err := config.List(envFile)
		if err != nil {
			log.Fatalf("config list: %v", err)
		}
		if len(m) == 0 {
			fmt.Printf("No keys configured in %s\n", envFile)
			return
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("Keys configured in %s:\n", envFile)
		for _, k := range keys {
			fmt.Printf("  %s=****\n", k)
		}

	default:
		fmt.Fprintf(os.Stderr, "error: unknown config command %q\n", subcmd)
		configUsage()
		os.Exit(1)
	}
}

// targetEnvFile returns the .env path to operate on.
func targetEnvFile(local bool) (string, error) {
	if local {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		return config.LocalFile(cwd), nil
	}
	return config.GlobalFile()
}

// runQuickstart re-runs the last tool/stack recorded for the target repo.
func runQuickstart(args []string) {
	fs := flag.NewFlagSet("construct qs", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: construct qs [path]\n\n")
		fmt.Fprintf(os.Stderr, "Re-runs the last tool and stack used for the given repo (defaults to cwd).\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	repoPath := "."
	if fs.NArg() > 0 {
		repoPath = fs.Arg(0)
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		log.Fatalf("resolve path: %v", err)
	}
	if _, err := os.Stat(absRepoPath); os.IsNotExist(err) {
		log.Fatalf("path does not exist: %s", absRepoPath)
	}

	last, err := config.LoadLastUsed(absRepoPath)
	if err != nil {
		log.Fatalf("qs: load last-used: %v", err)
	}
	if last.Tool == "" {
		log.Fatalf("qs: no previous run recorded for %s", absRepoPath)
	}

	// Default docker mode for old entries that pre-date the --docker flag.
	dockerMode := last.DockerMode
	if dockerMode == "" {
		dockerMode = "none"
	}

	// Build the status line showing every flag that will be replayed.
	statusLine := fmt.Sprintf("construct qs: reusing --tool %s --stack %s --docker %s", last.Tool, last.Stack, dockerMode)
	if last.MCP {
		statusLine += " --mcp"
	}
	for _, p := range last.Ports {
		statusLine += " --port " + p
	}
	fmt.Fprintln(os.Stderr, statusLine)

	agentArgs := []string{"--tool", last.Tool, "--stack", last.Stack, "--docker", dockerMode}
	if last.MCP {
		agentArgs = append(agentArgs, "--mcp")
	}
	for _, p := range last.Ports {
		agentArgs = append(agentArgs, "--port", p)
	}
	agentArgs = append(agentArgs, absRepoPath)
	runAgent(agentArgs)
}
