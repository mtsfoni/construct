package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/mtsfoni/construct/internal/config"
	"github.com/mtsfoni/construct/internal/runner"
	"github.com/mtsfoni/construct/internal/stacks"
	"github.com/mtsfoni/construct/internal/tools"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		runConfig(os.Args[2:])
		return
	}
	runAgent(os.Args[1:])
}

// runAgent is the original construct behaviour: build images and launch the agent.
func runAgent(args []string) {
	fs := flag.NewFlagSet("construct", flag.ExitOnError)
	toolName := fs.String("tool", "", "AI tool to run: copilot, opencode")
	stackName := fs.String("stack", "node", "Stack image to use: base, node, dotnet, python")
	rebuild := fs.Bool("rebuild", false, "Force rebuild of the stack and tool images")
	debug := fs.Bool("debug", false, "Start an interactive shell instead of the agent (for troubleshooting)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: construct --tool <tool> [--stack <stack>] [--rebuild] [--debug] [path]\n\n")
		fmt.Fprintf(os.Stderr, "Subcommands:\n")
		fmt.Fprintf(os.Stderr, "  config    Manage credential environment variables\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack dotnet /path/to/repo\n")
		fmt.Fprintf(os.Stderr, "  construct --tool copilot --stack node .\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack python ~/projects/myapp\n\n")
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
		log.Fatalf("unknown stack %q; supported stacks: base, node, dotnet, python", *stackName)
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

	if err := runner.Run(&runner.Config{
		Tool:     tool,
		Stack:    *stackName,
		RepoPath: absRepoPath,
		Rebuild:  *rebuild,
		Debug:    *debug,
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
