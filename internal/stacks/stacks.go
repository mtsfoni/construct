// Package stacks provides stack image names, image name derivation, and build
// context extraction for all construct stacks and the daemon image.
package stacks

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/construct-run/construct/embedfs"
	"github.com/construct-run/construct/internal/version"
)

// Known stack names.
const (
	StackBase      = "base"
	StackNode      = "node"
	StackGo        = "go"
	StackPython    = "python"
	StackDotnet    = "dotnet"
	StackDotnetBig = "dotnet-big"
	StackRuby      = "ruby"
	StackBaseUI    = "base-ui"
)

// ValidStacks is the set of valid stack names.
var ValidStacks = map[string]bool{
	StackBase:      true,
	StackNode:      true,
	StackGo:        true,
	StackPython:    true,
	StackDotnet:    true,
	StackDotnetBig: true,
	StackRuby:      true,
	StackBaseUI:    true,
}

// ImageName returns the Docker image name for a given stack name.
// E.g. "base" -> "construct-stack-base:latest"
func ImageName(name string) string {
	return fmt.Sprintf("construct-stack-%s:latest", name)
}

// DaemonImageName returns the Docker image name for the construct daemon.
func DaemonImageName() string {
	return "construct-daemon:latest"
}

// VersionLabel is the Docker image label used for version stamping.
const VersionLabel = "io.construct.version"

// ExtractBuildContext extracts the embedded build context for a named stack
// (or "daemon") to a temporary directory and returns the path.
// The caller is responsible for removing the directory when done.
func ExtractBuildContext(name string) (string, error) {
	var subdir string
	switch name {
	case "daemon":
		subdir = "stacks/daemon"
	default:
		if !ValidStacks[name] {
			return "", fmt.Errorf("unknown stack: %q", name)
		}
		subdir = fmt.Sprintf("stacks/%s", name)
	}

	dir, err := os.MkdirTemp("", fmt.Sprintf("construct-build-%s-*", name))
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	sub, err := fs.Sub(embedfs.FS, subdir)
	if err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("sub FS for %s: %w", name, err)
	}

	if err := fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, path)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
		// Make shell scripts executable
		if filepath.Ext(path) == ".sh" {
			if err := os.Chmod(dest, 0o755); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("extract build context for %s: %w", name, err)
	}

	return dir, nil
}

// BuildArgs returns the default build args for a stack or daemon image.
func BuildArgs() map[string]*string {
	v := version.Version
	return map[string]*string{
		"VERSION": &v,
	}
}
