package stacks

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed dockerfiles
var dockerfiles embed.FS

// validStacks is the ordered list of supported stack names.
var validStacks = []string{"base", "dotnet", "go", "ui"}

// stackDeps maps a stack name to the ordered list of prerequisite stack images
// that must be present before that stack can be built.
var stackDeps = map[string][]string{
	"ui": {"base"},
}

// ImageName returns the Docker image name for a given stack.
func ImageName(stack string) string {
	return "construct-" + stack
}

// All returns the list of supported stack names.
func All() []string {
	return validStacks
}

// IsValid reports whether the given stack name is supported.
func IsValid(stack string) bool {
	for _, s := range validStacks {
		if s == stack {
			return true
		}
	}
	return false
}

// EnsureBuilt builds the stack image (and its dependencies) if they do not
// already exist, or if rebuild is true.
func EnsureBuilt(stack string, rebuild bool) error {
	if !IsValid(stack) {
		return fmt.Errorf("unknown stack %q; supported stacks: %s", stack, strings.Join(validStacks, ", "))
	}

	// Build explicit dependencies declared in stackDeps first.
	// For stacks without an entry the implicit rule applies: every non-base
	// stack depends on the base image.
	deps, hasDeps := stackDeps[stack]
	if !hasDeps && stack != "base" {
		deps = []string{"base"}
	}
	for _, dep := range deps {
		depName := ImageName(dep)
		if rebuild || !imageExists(depName) {
			if err := build(dep, depName); err != nil {
				return fmt.Errorf("build %s image: %w", dep, err)
			}
		}
	}

	name := ImageName(stack)
	if rebuild || !imageExists(name) {
		if err := build(stack, name); err != nil {
			return fmt.Errorf("build %s image: %w", stack, err)
		}
	}
	return nil
}

// build writes the embedded Dockerfile for stack to a temp directory and runs
// docker build, tagging the result as imageName.
func build(stack, imageName string) error {
	dir, err := os.MkdirTemp("", "construct-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	data, err := dockerfiles.ReadFile("dockerfiles/" + stack + "/Dockerfile")
	if err != nil {
		return fmt.Errorf("read embedded Dockerfile for %s: %w", stack, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), data, 0o644); err != nil {
		return err
	}

	cmd := exec.Command("docker", "build", "-t", imageName, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// imageExists returns true when the named Docker image is present locally.
func imageExists(name string) bool {
	out, err := exec.Command("docker", "images", "-q", name).Output()
	return err == nil && len(out) > 0
}
