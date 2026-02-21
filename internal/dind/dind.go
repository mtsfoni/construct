package dind

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Instance represents a running Docker-in-Docker container and the network it
// shares with the agent container.
type Instance struct {
	SessionID     string
	ContainerName string
	NetworkName   string
}

// ContainerName returns the deterministic name for the dind container.
func containerName(sessionID string) string {
	return "construct-dind-" + sessionID
}

// networkName returns the deterministic name for the shared Docker network.
func networkName(sessionID string) string {
	return "construct-net-" + sessionID
}

// DockerHost returns the DOCKER_HOST value the agent container should use to
// reach the dind daemon.
func (i *Instance) DockerHost() string {
	return "tcp://" + i.ContainerName + ":2375"
}

// Start creates a Docker network and launches a privileged dind container
// attached to that network, then waits for the daemon inside to be ready.
func Start(sessionID string) (*Instance, error) {
	netName := networkName(sessionID)
	ctrName := containerName(sessionID)

	// Create an isolated bridge network.
	if out, err := run("docker", "network", "create", netName); err != nil {
		return nil, fmt.Errorf("create network %s: %w\n%s", netName, err, out)
	}

	// Start the dind container with TLS disabled (port 2375, localhost-scoped).
	args := []string{
		"run", "-d",
		"--name", ctrName,
		"--network", netName,
		"--privileged",
		"-e", "DOCKER_TLS_CERTDIR=",
		"docker:dind",
	}
	if out, err := run("docker", args...); err != nil {
		// Best-effort cleanup of the network we just created.
		run("docker", "network", "rm", netName) //nolint:errcheck
		return nil, fmt.Errorf("start dind container: %w\n%s", err, out)
	}

	inst := &Instance{
		SessionID:     sessionID,
		ContainerName: ctrName,
		NetworkName:   netName,
	}

	if err := inst.waitReady(); err != nil {
		inst.Stop()
		return nil, err
	}

	return inst, nil
}

// Stop removes the dind container and the shared network. Errors are printed to
// stderr but not returned so that deferred calls always complete.
func (i *Instance) Stop() {
	if out, err := run("docker", "stop", i.ContainerName); err != nil {
		fmt.Fprintf(os.Stderr, "construct: stop dind: %v\n%s\n", err, out)
	}
	if out, err := run("docker", "rm", i.ContainerName); err != nil {
		fmt.Fprintf(os.Stderr, "construct: rm dind: %v\n%s\n", err, out)
	}
	if out, err := run("docker", "network", "rm", i.NetworkName); err != nil {
		fmt.Fprintf(os.Stderr, "construct: rm network: %v\n%s\n", err, out)
	}
}

// waitReady polls until the daemon inside the dind container responds to
// "docker info" or a timeout is reached.
func (i *Instance) waitReady() error {
	const maxAttempts = 30
	for attempt := range maxAttempts {
		out, _ := exec.Command("docker", "exec", i.ContainerName, "docker", "info").CombinedOutput()
		if strings.Contains(string(out), "Server Version") {
			return nil
		}
		if attempt < maxAttempts-1 {
			time.Sleep(time.Second)
		}
	}
	return fmt.Errorf("dind daemon in %s did not become ready after %d seconds", i.ContainerName, maxAttempts)
}

// run executes a command and returns its combined output and any error.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
