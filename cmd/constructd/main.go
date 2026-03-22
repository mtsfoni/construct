// Command constructd is the construct daemon binary.
// It runs inside the construct-daemon Docker container, listens on a Unix socket,
// and manages session containers via the Docker API.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"

	"github.com/construct-run/construct/internal/auth"
	dockeriface "github.com/construct-run/construct/internal/daemon/docker"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/daemon/server"
	"github.com/construct-run/construct/internal/daemon/session"
	"github.com/construct-run/construct/internal/quickstart"
)

const (
	// stateDir is the path to the state directory inside the daemon container.
	// This maps to <construct-config-dir> on the host.
	stateDir = "/state"
)

func main() {
	if err := runDaemon(); err != nil {
		log.Fatalf("daemon error: %v", err)
	}
}

func runDaemon() error {
	socketPath := filepath.Join(stateDir, "daemon.sock")
	statePath := filepath.Join(stateDir, "daemon-state.json")

	// Ensure state directory exists.
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// Create Docker client.
	dockerClient, err := dockeriface.NewRealClient()
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer dockerClient.Close()

	// Load session registry.
	reg := registry.New(statePath)
	if err := reg.Load(); err != nil {
		log.Printf("Warning: could not load session registry: %v", err)
	}

	// Create supporting stores.
	authStore := auth.NewStore(stateDir)
	qsStore := quickstart.NewStore(stateDir)

	// Create session manager.
	// Respect CONSTRUCT_LOG_BUFFER env var for ring buffer size.
	logBufSize := 0
	if envSize := os.Getenv("CONSTRUCT_LOG_BUFFER"); envSize != "" {
		if n, err := strconv.Atoi(envSize); err == nil && n > 0 {
			logBufSize = n
		}
	}
	// CONSTRUCT_HOST_STATE_DIR is the host-side path that maps to /state inside
	// this container. It is used as the bind mount source when creating session
	// containers, because Docker resolves bind mounts relative to the host, not
	// relative to this container's filesystem.
	hostStateDir := os.Getenv("CONSTRUCT_HOST_STATE_DIR")
	if hostStateDir == "" {
		// Fallback: if not set (e.g. in tests), assume stateDir is already the
		// host path (which is true when running directly on the host).
		hostStateDir = stateDir
	}
	mgr := session.NewManagerWithBufferSize(dockerClient, reg, authStore, qsStore, stateDir, hostStateDir, logBufSize)

	// Reconcile registry against actual Docker state.
	ctx := context.Background()
	reconcile(ctx, mgr, reg, dockerClient)

	// Create and start the server.
	srv := server.New(socketPath, mgr, authStore, qsStore)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	defer srv.Close()

	log.Printf("constructd listening on %s", socketPath)

	// Wait for signals.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv.Serve(ctx)
	log.Printf("constructd shutting down")
	return nil
}

// reconcile synchronises the in-memory registry with actual Docker container states,
// re-attaches log streaming for running sessions, and removes orphaned containers
// and networks that are not tracked in the registry.
func reconcile(ctx context.Context, mgr *session.Manager, reg *registry.Registry, dockerClient dockeriface.Client) {
	// --- Step 1: reconcile known sessions ---
	knownContainers := make(map[string]struct{})
	knownNetworks := make(map[string]struct{})

	sessions := reg.List()
	for _, s := range sessions {
		knownContainers[s.ContainerName] = struct{}{}
		knownContainers[fmt.Sprintf("construct-dind-%s", s.ShortID())] = struct{}{}
		knownNetworks[fmt.Sprintf("construct-net-%s", s.ShortID())] = struct{}{}

		ctrJSON, err := dockerClient.ContainerInspect(ctx, s.ContainerName)
		if err != nil {
			log.Printf("Warning: could not inspect container %s: %v; removing from registry", s.ContainerName, err)
			reg.Remove(s.ID) //nolint:errcheck
			continue
		}

		actualStatus := ""
		if ctrJSON.State != nil {
			actualStatus = ctrJSON.State.Status
		}

		switch {
		case actualStatus == "running" && s.Status == registry.StatusStopped:
			log.Printf("Reconcile: session %s container running but registry says stopped; updating", s.ShortID())
			reg.UpdateStatus(s.ID, registry.StatusRunning, nil, nil) //nolint:errcheck
			// Re-attach log streaming for newly-discovered running container.
			if err := mgr.AttachLogStream(ctx, s.ID); err != nil {
				log.Printf("Warning: could not attach log stream for session %s: %v", s.ShortID(), err)
			}

		case (actualStatus == "exited" || actualStatus == "created" || actualStatus == "stopped") && s.Status == registry.StatusRunning:
			log.Printf("Reconcile: session %s container stopped but registry says running; updating", s.ShortID())
			reg.UpdateStatus(s.ID, registry.StatusStopped, nil, nil) //nolint:errcheck

		case actualStatus == "running" && s.Status == registry.StatusRunning:
			// Already consistent — re-attach log streaming so new output is captured.
			if err := mgr.AttachLogStream(ctx, s.ID); err != nil {
				log.Printf("Warning: could not attach log stream for session %s: %v", s.ShortID(), err)
			}
		}
	}

	// --- Step 2: orphan cleanup ---
	// Find construct-* containers not tracked in the registry.
	ctrs, err := dockerClient.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "construct-")),
	})
	if err != nil {
		log.Printf("Warning: could not list containers for orphan cleanup: %v", err)
	} else {
		for _, ctr := range ctrs {
			for _, name := range ctr.Names {
				// Docker names have a leading slash.
				trimmed := strings.TrimPrefix(name, "/")
				if strings.HasPrefix(trimmed, "construct-") && trimmed != "construct-daemon" {
					if _, known := knownContainers[trimmed]; !known {
						log.Printf("Warning: removing orphan container %s", trimmed)
						_ = dockerClient.ContainerRemove(ctx, trimmed, container.RemoveOptions{Force: true})
					}
				}
			}
		}
	}

	// Find construct-net-* networks not tracked in the registry.
	nets, err := dockerClient.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("name", "construct-net-")),
	})
	if err != nil {
		log.Printf("Warning: could not list networks for orphan cleanup: %v", err)
	} else {
		for _, net := range nets {
			if strings.HasPrefix(net.Name, "construct-net-") {
				if _, known := knownNetworks[net.Name]; !known {
					log.Printf("Warning: removing orphan network %s", net.Name)
					_ = dockerClient.NetworkRemove(ctx, net.ID)
				}
			}
		}
	}
}
