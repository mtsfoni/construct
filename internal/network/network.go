// Package network provides port-check utilities and session network naming.
package network

import (
	"fmt"
	"net"
)

// SessionNetworkName returns the Docker bridge network name for a session.
func SessionNetworkName(shortID string) string {
	return fmt.Sprintf("construct-net-%s", shortID)
}

// DindContainerName returns the name of the DinD sidecar container for a session.
func DindContainerName(shortID string) string {
	return fmt.Sprintf("construct-dind-%s", shortID)
}

// FindFreePort finds a free TCP port starting at startPort, incrementing by 1
// until a free port is found. It does this by attempting to listen on the port;
// if the listen succeeds, the port is free.
func FindFreePort(startPort int) (int, error) {
	for port := startPort; port < 65535; port++ {
		if isPortFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found starting at %d", startPort)
}

// isPortFree returns true if the TCP port is available to listen on.
func isPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// ParsePortSpec parses a port spec string into host and container ports.
// Formats:
//
//	"3000:3000"   -> host=3000, container=3000
//	"8080:9000"   -> host=8080, container=9000
//	"8080"        -> host=0 (auto-assign), container=8080
func ParsePortSpec(spec string) (hostPort, containerPort int, err error) {
	var h, c int
	n, _ := fmt.Sscanf(spec, "%d:%d", &h, &c)
	if n == 2 {
		return h, c, nil
	}
	n, _ = fmt.Sscanf(spec, "%d", &c)
	if n == 1 {
		return 0, c, nil
	}
	return 0, 0, fmt.Errorf("invalid port spec: %q (expected <host>:<container> or <container>)", spec)
}
