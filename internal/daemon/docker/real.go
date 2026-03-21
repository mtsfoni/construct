package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
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

// bindOptionsWithIDMapping mirrors mount.BindOptions with an additional IDMapping
// field that the Go SDK does not expose. The Docker daemon accepts it since API v1.44.
type bindOptionsWithIDMapping struct {
	Propagation            mount.Propagation `json:",omitempty"`
	NonRecursive           bool              `json:",omitempty"`
	CreateMountpoint       bool              `json:",omitempty"`
	ReadOnlyNonRecursive   bool              `json:",omitempty"`
	ReadOnlyForceRecursive bool              `json:",omitempty"`
	IDMapping              *idMappingJSON    `json:",omitempty"`
}

type idMappingJSON struct {
	UIDMappings []idMapJSON `json:",omitempty"`
	GIDMappings []idMapJSON `json:",omitempty"`
}

type idMapJSON struct {
	ContainerID uint32
	HostID      uint32
	Size        uint32
}

// mountJSON mirrors mount.Mount but uses bindOptionsWithIDMapping for BindOptions.
type mountJSON struct {
	Type          mount.Type                `json:",omitempty"`
	Source        string                    `json:",omitempty"`
	Target        string                    `json:",omitempty"`
	ReadOnly      bool                      `json:",omitempty"`
	Consistency   mount.Consistency         `json:",omitempty"`
	BindOptions   *bindOptionsWithIDMapping `json:",omitempty"`
	VolumeOptions *mount.VolumeOptions      `json:",omitempty"`
	TmpfsOptions  *mount.TmpfsOptions       `json:",omitempty"`
}

// containerCreateRequest mirrors the Docker API /containers/create body, but uses
// mountJSON for Mounts so we can inject IDMapping.
type containerCreateRequest struct {
	*container.Config
	HostConfig       *hostConfigWithMountJSON  `json:",omitempty"`
	NetworkingConfig *network.NetworkingConfig `json:",omitempty"`
	Platform         *specs.Platform           `json:",omitempty"`
}

// hostConfigWithMountJSON wraps container.HostConfig but replaces Mounts with mountJSON.
// We embed the whole HostConfig and then override Mounts via a custom marshaler.
type hostConfigWithMountJSON struct {
	container.HostConfig
	Mounts []mountJSON
}

// MarshalJSON serializes the HostConfig but replaces the Mounts field with our extended version.
func (h *hostConfigWithMountJSON) MarshalJSON() ([]byte, error) {
	// Marshal the base HostConfig.
	base, err := json.Marshal(h.HostConfig)
	if err != nil {
		return nil, err
	}
	// Parse it as a raw map.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	// Replace Mounts with our extended version.
	mountsJSON, err := json.Marshal(h.Mounts)
	if err != nil {
		return nil, err
	}
	m["Mounts"] = mountsJSON
	return json.Marshal(m)
}

// ContainerCreateWithIDMap creates a container like ContainerCreate but injects an
// IDMapping into the bind mount for bindSrc/bindDst. This works around the limitation
// that mount.BindOptions in the Go SDK doesn't expose IDMapping.
func (r *RealClient) ContainerCreateWithIDMap(
	ctx context.Context,
	config *container.Config,
	hostConfig *container.HostConfig,
	networkConfig *network.NetworkingConfig,
	platform *specs.Platform,
	name string,
	bindSrc, bindDst string,
	idmap IDMapping,
) (container.CreateResponse, error) {
	var response container.CreateResponse

	// Convert mounts to mountJSON, injecting IDMapping for the target bind.
	mounts := make([]mountJSON, 0, len(hostConfig.Mounts))
	for _, m := range hostConfig.Mounts {
		mj := mountJSON{
			Type:          m.Type,
			Source:        m.Source,
			Target:        m.Target,
			ReadOnly:      m.ReadOnly,
			Consistency:   m.Consistency,
			VolumeOptions: m.VolumeOptions,
			TmpfsOptions:  m.TmpfsOptions,
		}
		if m.BindOptions != nil {
			mj.BindOptions = &bindOptionsWithIDMapping{
				Propagation:            m.BindOptions.Propagation,
				NonRecursive:           m.BindOptions.NonRecursive,
				CreateMountpoint:       m.BindOptions.CreateMountpoint,
				ReadOnlyNonRecursive:   m.BindOptions.ReadOnlyNonRecursive,
				ReadOnlyForceRecursive: m.BindOptions.ReadOnlyForceRecursive,
			}
		}
		// Inject IDMapping for the target bind mount.
		if m.Type == mount.TypeBind && m.Source == bindSrc && m.Target == bindDst {
			if mj.BindOptions == nil {
				mj.BindOptions = &bindOptionsWithIDMapping{}
			}
			uidMaps := make([]idMapJSON, len(idmap.UIDMappings))
			for i, u := range idmap.UIDMappings {
				uidMaps[i] = idMapJSON{ContainerID: u.ContainerID, HostID: u.HostID, Size: u.Size}
			}
			gidMaps := make([]idMapJSON, len(idmap.GIDMappings))
			for i, g := range idmap.GIDMappings {
				gidMaps[i] = idMapJSON{ContainerID: g.ContainerID, HostID: g.HostID, Size: g.Size}
			}
			mj.BindOptions.IDMapping = &idMappingJSON{
				UIDMappings: uidMaps,
				GIDMappings: gidMaps,
			}
		}
		mounts = append(mounts, mj)
	}

	hcExt := &hostConfigWithMountJSON{
		HostConfig: *hostConfig,
		Mounts:     mounts,
	}
	// Clear the Mounts on the embedded HostConfig to avoid duplication.
	hcExt.HostConfig.Mounts = nil

	body := containerCreateRequest{
		Config:           config,
		HostConfig:       hcExt,
		NetworkingConfig: networkConfig,
		Platform:         platform,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return response, fmt.Errorf("marshal container create body: %w", err)
	}

	// Build request URL.
	daemonHost := r.c.DaemonHost()
	apiVersion := r.c.ClientVersion()
	reqURL, err := buildContainerCreateURL(daemonHost, apiVersion, name, platform)
	if err != nil {
		return response, fmt.Errorf("build container create URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return response, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := r.c.HTTPClient()
	resp, err := httpClient.Do(req)
	if err != nil {
		return response, fmt.Errorf("docker API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return response, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		var errResp struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(respBody, &errResp)
		return response, fmt.Errorf("docker API error %d: %s", resp.StatusCode, errResp.Message)
	}

	if err := json.Unmarshal(respBody, &response); err != nil {
		return response, fmt.Errorf("decode response: %w", err)
	}
	return response, nil
}

// buildContainerCreateURL constructs the full URL for /containers/create.
func buildContainerCreateURL(daemonHost, apiVersion, name string, platform *specs.Platform) (string, error) {
	u, err := url.Parse(daemonHost)
	if err != nil {
		// Could be "unix:///var/run/docker.sock" — handle scheme-less forms.
		// The HTTP client already handles unix socket transport; we just need
		// the URL path for the request.
		u = &url.URL{Scheme: "http", Host: "localhost"}
	}
	if u.Scheme == "unix" {
		u.Scheme = "http"
		u.Host = "localhost"
	}
	u.Path = path.Join("/v"+apiVersion, "/containers/create")
	q := u.Query()
	if name != "" {
		q.Set("name", name)
	}
	if platform != nil {
		p := platform.OS
		if platform.Architecture != "" {
			p += "/" + platform.Architecture
		}
		if platform.Variant != "" {
			p += "/" + platform.Variant
		}
		if p != "" {
			q.Set("platform", p)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
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
