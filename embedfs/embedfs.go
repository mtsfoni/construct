// Package embedfs holds the embedded filesystem containing all Dockerfiles
// and build contexts for construct stacks and the daemon.
//
// Both the CLI and daemon binaries import this package to get access to the
// embedded files. The package lives at the repository root so the embed
// directive can reference the sibling stacks/ directory.
package embedfs

import "embed"

//go:embed all:stacks
var FS embed.FS // FS contains the stacks/ subdirectory tree
