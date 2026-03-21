// Package docker wraps Docker SDK calls behind an interface for testability.
package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// IDMap represents a single UID or GID mapping for an idmap mount.
// ContainerID is the ID inside the container (typically 0 for root),
// HostID is the corresponding ID on the host, and Size is the range size (typically 1).
type IDMap struct {
	ContainerID uint32
	HostID      uint32
	Size        uint32
}

// IDMapping holds the UID and GID mappings for an idmap bind mount.
// These are passed to Docker API as BindOptions.IDMapping (available since API 1.44 / Docker 25.0).
type IDMapping struct {
	UIDMappings []IDMap
	GIDMappings []IDMap
}

// Client defines all Docker operations used by construct.
// This interface is implemented by the real Docker SDK client and by fakes
// for testing.
type Client interface {
	// Container operations
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkConfig *network.NetworkingConfig, platform *specs.Platform, name string) (container.CreateResponse, error)
	// ContainerCreateWithIDMap is like ContainerCreate but allows specifying an idmap
	// for the bind mount at bindSrc/bindDst. This is needed because the Go SDK's
	// mount.BindOptions does not expose the IDMapping field even though the Docker
	// API (v1.44+) supports it.
	ContainerCreateWithIDMap(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkConfig *network.NetworkingConfig, platform *specs.Platform, name string, bindSrc, bindDst string, idmap IDMapping) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (types.IDResponse, error)
	ContainerExecStart(ctx context.Context, execID string, config container.ExecStartOptions) error
	ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerKill(ctx context.Context, containerID string, signal string) error

	// Image operations
	ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error)
	ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error)

	// Network operations
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkRemove(ctx context.Context, networkID string) error
	NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error)

	// Volume operations
	VolumeCreate(ctx context.Context, options volume.CreateOptions) (volume.Volume, error)
	VolumeRemove(ctx context.Context, volumeID string, force bool) error

	// Server info
	ServerVersion(ctx context.Context) (types.Version, error)
	Ping(ctx context.Context) (types.Ping, error)

	Close() error
}
