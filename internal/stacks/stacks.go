package stacks

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mtsfoni/construct/internal/buildinfo"
)

//go:embed dockerfiles
var dockerfiles embed.FS

// validStacks is the ordered list of supported stack names.
var validStacks = []string{"base", "dotnet", "dotnet-big", "dotnet-big-ui", "dotnet-ui", "go", "ruby", "ruby-ui", "ui"}

// stackDeps maps a stack name to the ordered list of prerequisite stack images
// that must be present before that stack can be built.
var stackDeps = map[string][]string{
	"dotnet-big-ui": {"base", "dotnet-big"},
	"dotnet-ui":     {"base", "dotnet"},
	"ruby-ui":       {"base", "ruby"},
	"ui":            {"base"},
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
// already exist, if rebuild is true, or if they were built by a different
// version of construct.
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
		if rebuild || !imageExists(depName) || !imageVersionCurrent(depName) {
			if err := build(dep, depName); err != nil {
				return fmt.Errorf("build %s image: %w", dep, err)
			}
		}
	}

	name := ImageName(stack)
	if rebuild || !imageExists(name) || !imageVersionCurrent(name) {
		if err := build(stack, name); err != nil {
			return fmt.Errorf("build %s image: %w", stack, err)
		}
	}
	return nil
}

// build writes the embedded Dockerfile for stack to a temp directory and runs
// docker build, tagging the result as imageName. When buildinfo.Version is set,
// the image is labelled with io.construct.version so future runs can detect
// whether a rebuild is needed.
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

	args := []string{"build", "-t", imageName}
	if buildinfo.Version != "" {
		args = append(args, "--label", "io.construct.version="+buildinfo.Version)
	}
	args = append(args, dir)
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// imageExists returns true when the named Docker image is present locally.
func imageExists(name string) bool {
	out, err := exec.Command("docker", "images", "-q", name).Output()
	return err == nil && len(out) > 0
}

// imageLabel is the function used to retrieve a Docker image label value.
// It is a variable so tests can substitute a fake without shelling out to Docker.
// The function must return ("", error) when the image is not found.
var imageLabel = func(imageName, label string) (string, error) {
	out, err := exec.Command(
		"docker", "image", "inspect",
		"--format", `{{index .Config.Labels "`+label+`"}}`,
		imageName,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// imageVersionCurrent returns true when the named image carries an
// io.construct.version label that matches the running binary's version, or
// when buildinfo.Version is empty (dev build — skip the check).
// Returns false when the image was built by a different version or predates
// this feature (no label), triggering an automatic rebuild.
func imageVersionCurrent(name string) bool {
	if buildinfo.Version == "" {
		return true // dev build: never force a rebuild based on version
	}
	got, err := imageLabel(name, "io.construct.version")
	if err != nil {
		return false // image not found or inspect failed — treat as stale
	}
	return got == buildinfo.Version
}
