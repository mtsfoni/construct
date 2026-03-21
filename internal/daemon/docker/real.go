package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// RealClient wraps the official Docker SDK client to implement the Client interface.
type RealClient struct {
	c *dockerclient.Client
}

// NewRealClient creates a new RealClient using environment-based configuration.
func NewRealClient() (*RealClient, error) {
	c, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &RealClient{c: c}, nil
}

func (r *RealClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkConfig *network.NetworkingConfig, platform *specs.Platform, name string) (container.CreateResponse, error) {
	return r.c.ContainerCreate(ctx, config, hostConfig, networkConfig, platform, name)
}

func (r *RealClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	return r.c.ContainerStart(ctx, containerID, options)
}

func (r *RealClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	return r.c.ContainerStop(ctx, containerID, options)
}

func (r *RealClient) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	return r.c.ContainerRemove(ctx, containerID, options)
}

func (r *RealClient) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return r.c.ContainerInspect(ctx, containerID)
}

func (r *RealClient) ContainerExecCreate(ctx context.Context, containerID string, config container.ExecOptions) (types.IDResponse, error) {
	return r.c.ContainerExecCreate(ctx, containerID, config)
}

func (r *RealClient) ContainerExecStart(ctx context.Context, execID string, config container.ExecStartOptions) error {
	return r.c.ContainerExecStart(ctx, execID, config)
}

func (r *RealClient) ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error) {
	return r.c.ContainerExecAttach(ctx, execID, config)
}

func (r *RealClient) ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error) {
	return r.c.ContainerExecInspect(ctx, execID)
}

func (r *RealClient) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	return r.c.ContainerList(ctx, options)
}

func (r *RealClient) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	return r.c.ContainerLogs(ctx, containerID, options)
}

func (r *RealClient) ContainerKill(ctx context.Context, containerID string, signal string) error {
	return r.c.ContainerKill(ctx, containerID, signal)
}

func (r *RealClient) ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	return r.c.ImageBuild(ctx, buildContext, options)
}

func (r *RealClient) ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error) {
	return r.c.ImageInspectWithRaw(ctx, imageID)
}

func (r *RealClient) ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
	return r.c.ImageList(ctx, options)
}

func (r *RealClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return r.c.NetworkCreate(ctx, name, options)
}

func (r *RealClient) NetworkRemove(ctx context.Context, networkID string) error {
	return r.c.NetworkRemove(ctx, networkID)
}

func (r *RealClient) NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error) {
	return r.c.NetworkList(ctx, options)
}

func (r *RealClient) VolumeCreate(ctx context.Context, options volume.CreateOptions) (volume.Volume, error) {
	return r.c.VolumeCreate(ctx, options)
}

func (r *RealClient) VolumeRemove(ctx context.Context, volumeID string, force bool) error {
	return r.c.VolumeRemove(ctx, volumeID, force)
}

func (r *RealClient) ServerVersion(ctx context.Context) (types.Version, error) {
	return r.c.ServerVersion(ctx)
}

func (r *RealClient) Ping(ctx context.Context) (types.Ping, error) {
	return r.c.Ping(ctx)
}

func (r *RealClient) Close() error {
	return r.c.Close()
}
