package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultContainerImage = "docker.io/library/python:3.12-slim-bookworm"

type ContainerOptions struct {
	Backend, Host, Image, Runtime, Network string
	CPUs                                   uint32
	MemoryMiB                              uint64
	MaxRunning                             int
	MemoryBudgetMiB                        uint64
	Store                                  *gorm.DB
}

// Container uses the official Docker Go SDK. Podman is accessed through its
// documented Docker v1.40 compatibility API, with a rootless daemon required.
type Container struct {
	opts    ContainerOptions
	api     *client.Client
	locks   sync.Map
	probeMu sync.Mutex
	probed  bool
}

func NewContainer(o ContainerOptions) (*Container, error) {
	if o.Backend != "gvisor" && o.Backend != "podman" {
		return nil, errors.New("container backend must be gvisor or podman")
	}
	if o.Host == "" {
		o.Host = "unix:///var/run/docker.sock"
		if o.Backend == "podman" {
			o.Host = "unix:///run/wave-podman/podman.sock"
		}
	}
	if o.Backend == "podman" {
		o.Runtime = ""
	}
	if !strings.HasPrefix(o.Host, "unix://") {
		return nil, errors.New("container API requires a local Unix socket; use a secured local tunnel for remote engines")
	}
	if o.Image == "" {
		o.Image = DefaultContainerImage
	}
	if o.Network == "" {
		o.Network = "none"
	}
	if o.Network == "host" || strings.HasPrefix(o.Network, "container:") {
		return nil, errors.New("host/shared-container networking is not a sandbox network")
	}
	if o.CPUs == 0 {
		o.CPUs = 1
	}
	if o.MemoryMiB == 0 {
		o.MemoryMiB = 1024
	}
	if o.MaxRunning == 0 {
		o.MaxRunning = 2
	}
	if o.MemoryBudgetMiB == 0 {
		o.MemoryBudgetMiB = 4096
	}
	if o.Backend == "gvisor" && o.Runtime == "" {
		o.Runtime = "wave-runsc"
	}
	options := []client.Opt{client.WithHost(o.Host)}
	if o.Backend == "podman" {
		options = append(options, client.WithAPIVersion("1.40"))
	}
	api, err := client.New(options...)
	if err != nil {
		return nil, err
	}
	return &Container{opts: o, api: api}, nil
}
func (c *Container) String() string { return c.opts.Backend }
func (c *Container) Close() error   { return c.api.Close() }
func (c *Container) Resources(profile string) (Resources, error) {
	return profileResources(profile, Resources{c.opts.CPUs, c.opts.MemoryMiB})
}
func (c *Container) Reserve(tx *gorm.DB, sid string, r Resources) error {
	return reserve(tx, sid, r, allocation{Backend: c.opts.Backend, Host: c.opts.Host, Image: c.opts.Image, Runtime: c.opts.Runtime, Network: c.opts.Network, MaxRunning: c.opts.MaxRunning, MemoryBudgetMiB: c.opts.MemoryBudgetMiB})
}
func (c *Container) lock(sid string) func() {
	v, _ := c.locks.LoadOrStore(sid, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
func (c *Container) record(ctx context.Context, sid string) (Record, error) {
	var r Record
	err := c.opts.Store.WithContext(ctx).Where(clause.Eq{Column: "session_id", Value: sid}).Take(&r).Error
	return r, err
}
func (c *Container) state(ctx context.Context, sid, state string) error {
	return c.opts.Store.WithContext(ctx).Model(&Record{}).Where(clause.Eq{Column: "session_id", Value: sid}).Update("state", state).Error
}

func (c *Container) probe(ctx context.Context) error {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	if c.probed {
		return nil
	}
	info, err := c.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("%s engine unavailable: %w", c.opts.Backend, err)
	}
	if c.opts.Backend == "gvisor" {
		runtime, ok := info.Info.Runtimes[c.opts.Runtime]
		if !ok || !strings.Contains(runtime.Path, "runsc") {
			return fmt.Errorf("gVisor runtime %q is not registered as runsc; refusing ordinary-container fallback", c.opts.Runtime)
		}
		for _, required := range []string{"--platform=systrap", "--overlay2=none", "--file-access=shared"} {
			if !slices.Contains(runtime.Args, required) {
				return fmt.Errorf("gVisor runtime requires %s; run deploy/install-sandbox-host.sh", required)
			}
		}
	} else {
		rootless := false
		for _, option := range info.Info.SecurityOptions {
			if option == "name=rootless" || option == "rootless" {
				rootless = true
			}
		}
		if !rootless {
			return errors.New("podman backend requires a rootless Podman API socket")
		}
	}
	if !info.Info.MemoryLimit || (c.opts.Backend == "gvisor" && !info.Info.CPUCfsQuota) || (c.opts.Backend == "podman" && info.Info.CgroupVersion != "2") || !info.Info.PidsLimit {
		return errors.New("container host must enforce memory, CPU quota and PID limits (rootless Podman needs cgroup v2 delegation)")
	}
	c.probed = true
	return nil
}

func (c *Container) ensure(ctx context.Context, sid string) (Record, error) {
	if err := c.probe(ctx); err != nil {
		return Record{}, err
	}
	if err := c.opts.Store.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return c.Reserve(tx, sid, Resources{c.opts.CPUs, c.opts.MemoryMiB}) }); err != nil {
		return Record{}, err
	}
	r, err := c.record(ctx, sid)
	if err != nil {
		return r, err
	}
	if err := c.prepareStorage(ctx, &r); err != nil {
		return r, err
	}
	ref := r.BackendID
	if ref == "" {
		ref = r.Name
	}
	inspected, err := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) && r.BackendID == "" {
		if err = c.ensureImage(ctx, r.Image); err != nil {
			return r, err
		}
		if err = c.state(ctx, sid, "creating"); err != nil {
			return r, err
		}
		created, e := c.api.ContainerCreate(ctx, c.createOptions(r, false))
		if e != nil && !errdefs.IsConflict(e) {
			return r, e
		}
		if e == nil {
			ref = created.ID
		} else {
			ref = r.Name
		}
		inspected, err = c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	}
	if err != nil {
		return r, fmt.Errorf("inspect retained sandbox (never replaced automatically): %w", err)
	}
	if err = c.verify(inspected.Container, r, false); err != nil {
		return r, err
	}
	r.BackendID = inspected.Container.ID
	if err = c.opts.Store.WithContext(ctx).Model(&r).Update("backend_id", r.BackendID).Error; err != nil {
		return r, err
	}
	if inspected.Container.State == nil {
		return r, errors.New("container has no state")
	}
	if !inspected.Container.State.Running {
		if err = c.copyDirectories(ctx, r.BackendID, []string{"workspace", "mnt/session/outputs", "mnt/memory"}); err != nil {
			return r, err
		}
		if err = c.state(ctx, sid, "starting"); err != nil {
			return r, err
		}
		if _, err = c.api.ContainerStart(ctx, r.BackendID, client.ContainerStartOptions{}); err != nil {
			return r, err
		}
		inspected, err = c.api.ContainerInspect(ctx, r.BackendID, client.ContainerInspectOptions{})
		if err != nil {
			return r, err
		}
		if inspected.Container.State == nil || !inspected.Container.State.Running {
			return r, errors.New("container failed to reach running state")
		}
	}
	err = c.state(ctx, sid, "running")
	return r, err
}

func (c *Container) Ensure(ctx context.Context, sid string) error {
	defer c.lock(sid)()
	_, err := c.ensure(ctx, sid)
	return err
}
func (c *Container) Identity(ctx context.Context, sid string) (string, error) {
	r, err := c.record(ctx, sid)
	if err != nil {
		return "", err
	}
	if r.BackendID == "" {
		return "", errors.New("sandbox not provisioned")
	}
	return r.Backend + ":" + r.BackendID, nil
}

func (c *Container) Stop(ctx context.Context, sid string) error {
	defer c.lock(sid)()
	r, err := c.record(ctx, sid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Backend != c.opts.Backend {
		return errors.New("sandbox backend mismatch")
	}
	if r.State == "stopped" {
		return nil
	}
	if r.BackendID == "" && r.State == "reserved" {
		return c.state(ctx, sid, "stopped")
	}
	ref := r.BackendID
	if ref == "" {
		ref = r.Name
	}
	info, err := c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if err = c.verify(info.Container, r, false); err != nil {
		return err
	}
	if info.Container.State != nil && info.Container.State.Running {
		timeout := 2
		if _, err = c.api.ContainerStop(ctx, ref, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
			return err
		}
	}
	info, err = c.api.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if info.Container.State == nil || info.Container.State.Running {
		return errors.New("container stop not confirmed")
	}
	return c.state(ctx, sid, "stopped")
}

func (c *Container) ensureImage(ctx context.Context, image string) error {
	if _, err := c.api.ImageInspect(ctx, image); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	pull, err := c.api.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer pull.Close()
	if _, err = io.Copy(io.Discard, pull); err != nil {
		return err
	}
	_, err = c.api.ImageInspect(ctx, image)
	return err
}

func labels(r Record, stager bool) map[string]string {
	role := "sandbox"
	if stager {
		role = "stager"
	}
	return map[string]string{"wave.managed": "true", "wave.session": r.SessionID, "wave.backend": r.Backend, "wave.role": role}
}
func volumeName(r Record, kind string) string { return strings.ToLower(r.Name) + "-" + kind }
func (c *Container) createOptions(r Record, stager bool) client.ContainerCreateOptions {
	pids := int64(256)
	host := &container.HostConfig{Runtime: r.Runtime, NetworkMode: container.NetworkMode(r.Network), CapDrop: []string{"ALL"}, CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETUID", "SETGID"}, SecurityOpt: []string{"no-new-privileges:true"}, Resources: container.Resources{Memory: int64(r.MemoryMiB) << 20, MemorySwap: int64(r.MemoryMiB) << 20, CPUPeriod: 100000, CPUQuota: int64(r.CPUs) * 100000, PidsLimit: &pids}}
	name := r.Name
	for _, kind := range []string{"uploads", "skills"} {
		dest := "/mnt/session/uploads"
		if kind == "skills" {
			dest = "/mnt/skills"
		}
		if stager {
			dest = "/" + kind
		}
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: volumeName(r, kind), Target: dest, ReadOnly: !stager})
	}
	if stager {
		name += "-stager"
		host.NetworkMode = "none"
	}
	return client.ContainerCreateOptions{Name: name, Image: r.Image, Config: &container.Config{Cmd: []string{"sleep", "infinity"}, User: "0:0", WorkingDir: "/workspace", Env: []string{"HOME=/workspace", "LANG=C.UTF-8", "TZ=UTC"}, Labels: labels(r, stager)}, HostConfig: host}
}

func (c *Container) verify(info container.InspectResponse, r Record, stager bool) error {
	if info.Config == nil || info.HostConfig == nil {
		return errors.New("incomplete container inspection")
	}
	for k, v := range labels(r, stager) {
		if info.Config.Labels[k] != v {
			return errors.New("container ownership labels do not match session")
		}
	}
	h := info.HostConfig
	if h.Privileged || h.NetworkMode == "host" || h.PidMode == "host" || len(h.Devices) > 0 {
		return errors.New("unsafe container configuration")
	}
	if r.Backend == "gvisor" && h.Runtime != r.Runtime {
		return errors.New("container is not using the required gVisor runtime")
	}
	if h.Memory != int64(r.MemoryMiB)<<20 || h.CPUQuota != int64(r.CPUs)*100000 || h.CPUPeriod != 100000 || h.PidsLimit == nil || *h.PidsLimit != 256 {
		return errors.New("container resource limits do not match reserved specification")
	}
	if len(info.Mounts) != 2 {
		return errors.New("unexpected sandbox mounts")
	}
	for _, kind := range []string{"uploads", "skills"} {
		dest := "/mnt/session/uploads"
		if kind == "skills" {
			dest = "/mnt/skills"
		}
		if stager {
			dest = "/" + kind
		}
		found := false
		for _, m := range info.Mounts {
			if m.Type == mount.TypeVolume && m.Destination == dest && m.Name == volumeName(r, kind) && m.RW == stager {
				found = true
			}
		}
		if !found {
			return errors.New("sandbox volume type or permissions do not match")
		}
	}
	noNewPrivileges := false
	for _, opt := range h.SecurityOpt {
		if opt == "no-new-privileges" || opt == "no-new-privileges:true" {
			noNewPrivileges = true
		}
	}
	if !noNewPrivileges {
		return errors.New("sandbox requires no-new-privileges")
	}

	return nil
}

func (c *Container) prepareStorage(ctx context.Context, r *Record) error {
	if r.StagerID != "" {
		return nil
	}
	if err := c.ensureImage(ctx, r.Image); err != nil {
		return err
	}
	for _, kind := range []string{"uploads", "skills"} {
		v, err := c.api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: volumeName(*r, kind), Labels: labels(*r, true)})
		if err != nil {
			return err
		}
		if v.Volume.Labels["wave.session"] != r.SessionID {
			return errors.New("sandbox volume name collision")
		}
	}
	name := r.Name + "-stager"
	i, err := c.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		_, err = c.api.ContainerCreate(ctx, c.createOptions(*r, true))
		if err != nil && !errdefs.IsConflict(err) {
			return err
		}
		i, err = c.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	}
	if err != nil {
		return err
	}
	if err = c.verify(i.Container, *r, true); err != nil {
		return err
	}
	if i.Container.State != nil && i.Container.State.Running {
		return errors.New("input staging container must never run")
	}
	r.StagerID = i.Container.ID
	return c.opts.Store.WithContext(ctx).Model(r).Update("stager_id", r.StagerID).Error
}
