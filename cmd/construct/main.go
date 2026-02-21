package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mtsfoni/construct/internal/runner"
	"github.com/mtsfoni/construct/internal/stacks"
	"github.com/mtsfoni/construct/internal/tools"
)

func main() {
	toolName := flag.String("tool", "", "AI tool to run: copilot, opencode")
	stackName := flag.String("stack", "node", "Stack image to use: base, node, dotnet, python")
	rebuild := flag.Bool("rebuild", false, "Force rebuild of the stack and tool images")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: construct --tool <tool> [--stack <stack>] [--rebuild] [path]\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack dotnet /path/to/repo\n")
		fmt.Fprintf(os.Stderr, "  construct --tool copilot --stack node .\n")
		fmt.Fprintf(os.Stderr, "  construct --tool opencode --stack python ~/projects/myapp\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *toolName == "" {
		fmt.Fprintln(os.Stderr, "error: --tool is required")
		flag.Usage()
		os.Exit(1)
	}

	tool, err := tools.Get(*toolName)
	if err != nil {
		log.Fatal(err)
	}

	if !stacks.IsValid(*stackName) {
		log.Fatalf("unknown stack %q; supported stacks: base, node, dotnet, python", *stackName)
	}

	// Resolve the repo path (default: current working directory).
	repoPath := "."
	if flag.NArg() > 0 {
		repoPath = flag.Arg(0)
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
	}); err != nil {
		log.Fatal(err)
	}
}
