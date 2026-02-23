package dind

import (
	"testing"
)

func TestDockerHost_ReturnsStaticAlias(t *testing.T) {
	inst := &Instance{
		SessionID:     "abc123",
		ContainerName: "construct-dind-abc123",
		NetworkName:   "construct-net-abc123",
	}
	want := "tcp://dind:2375"
	if got := inst.DockerHost(); got != want {
		t.Errorf("DockerHost() = %q, want %q", got, want)
	}
}

// TestStart_IncludesNetworkAlias verifies that the args slice passed to
// "docker run" when starting the dind sidecar includes "--network-alias" "dind".
// We do this by inspecting the args that buildStartArgs constructs rather than
// executing Docker, so the test runs without a Docker daemon.
func TestStart_IncludesNetworkAlias(t *testing.T) {
	args := buildStartArgs("testsession", "construct-dind-testsession", "construct-net-testsession")

	aliasIdx := -1
	for i, a := range args {
		if a == "--network-alias" {
			aliasIdx = i
			break
		}
	}
	if aliasIdx == -1 {
		t.Fatalf("args do not contain --network-alias: %v", args)
	}
	if aliasIdx+1 >= len(args) {
		t.Fatalf("--network-alias flag has no value in args: %v", args)
	}
	if got := args[aliasIdx+1]; got != "dind" {
		t.Errorf("--network-alias value = %q, want %q", got, "dind")
	}
}
