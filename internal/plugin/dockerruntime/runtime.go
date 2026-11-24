// Package dockerruntime implements the plugin Runtime boundary with the
// official Docker Engine SDK. Podman is served through its Docker-compatible
// API, so no second client is needed. It is the only package that imports
// Docker types.
package dockerruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

type Runtime struct {
	clientMu sync.RWMutex
	client   *client.Client

	startMu   sync.Mutex
	settings  plugin.RuntimeSettings
	detectErr error

	mu          sync.RWMutex
	runtimeType string
}

type Options struct {
	Host        string
	Type        string // "docker" or "podman"
	Settings    plugin.RuntimeSettings
	DetectError error // startup discovery failure, surfaced through Info
}

func New(opts Options) (*Runtime, error) {
	clientOptions := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if opts.Host != "" {
		clientOptions = append(clientOptions, client.WithHost(opts.Host))
	}
	cli, err := client.New(clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", plugin.ErrRuntimeUnavailable, err)
	}
	return &Runtime{
		client: cli, settings: opts.Settings.Normalized(),
		detectErr: opts.DetectError, runtimeType: normalizeType(opts.Type),
	}, nil
}

// Type reports the engine detected at startup, or after StartRuntime. Info
// refreshes it from the server version so an engine swap does not leave a stale
// name behind.
func (r *Runtime) Type() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtimeType
}

func (r *Runtime) setType(value string) string {
	value = normalizeType(value)
	r.mu.Lock()
	r.runtimeType = value
	r.mu.Unlock()
	return value
}

func (r *Runtime) api() *client.Client {
	r.clientMu.RLock()
	defer r.clientMu.RUnlock()
	return r.client
}

// canStart reports whether the plugin page may try to bring Podman up. An
// explicit DOCKER_HOST/CONTAINER_HOST is never overridden by a start action.
func (r *Runtime) canStart() bool {
	if strings.TrimSpace(os.Getenv("DOCKER_HOST")) != "" || strings.TrimSpace(os.Getenv("CONTAINER_HOST")) != "" {
		return false
	}
	return platformStartAvailable != nil && platformStartAvailable()
}

func (r *Runtime) unavailableInfo() plugin.RuntimeInfo {
	return plugin.RuntimeInfo{
		Type: r.Type(), NetworkMode: r.settings.NetworkMode, Network: r.settings.Network,
		StartAvailable: r.canStart(),
	}
}

func (r *Runtime) Info(ctx context.Context) (plugin.RuntimeInfo, error) {
	api := r.api()
	ping, err := api.Ping(ctx, client.PingOptions{})
	if err != nil {
		return r.unavailableInfo(), r.unavailableError(err)
	}
	version, err := api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return r.unavailableInfo(), r.unavailableError(err)
	}
	runtimeType := r.setType(engineType(version))
	return plugin.RuntimeInfo{
		Type: runtimeType, Available: true, Version: version.Version, APIVersion: ping.APIVersion,
		OS: version.Os, Architecture: version.Arch,
		NetworkMode: r.settings.NetworkMode, Network: r.settings.Network,
	}, nil
}

// unavailableError keeps the startup discovery explanation attached, so the
// page can tell the user which Docker or Podman endpoints were tried.
func (r *Runtime) unavailableError(err error) error {
	mapped := mapError(err)
	r.mu.RLock()
	detectErr := r.detectErr
	r.mu.RUnlock()
	if detectErr == nil || !errors.Is(mapped, plugin.ErrRuntimeUnavailable) {
		return mapped
	}
	return fmt.Errorf("%w: %v", plugin.ErrRuntimeUnavailable, detectErr)
}

const (
	startRuntimeWindow = 30 * time.Second
	startRuntimePoll   = time.Second
)

// StartRuntime starts the local Podman API on demand, re-detects endpoints and
// swaps the Docker client in place. It is safe to call while the runtime is
// unavailable; concurrent calls get ErrBusy.
func (r *Runtime) StartRuntime(ctx context.Context, progress func(percent int, message string)) error {
	if !r.startMu.TryLock() {
		return fmt.Errorf("%w: 容器运行时正在启动", plugin.ErrBusy)
	}
	defer r.startMu.Unlock()

	if _, err := r.Info(ctx); err == nil {
		return fmt.Errorf("%w: 容器运行时已可用", plugin.ErrConflict)
	}
	if !r.canStart() {
		return fmt.Errorf("%w: 当前环境无法自动启动 Podman", plugin.ErrUnsupported)
	}
	if progress != nil {
		progress(5, "正在启动 Podman")
	}
	if err := platformRuntimeStart(ctx, progress); err != nil {
		return err
	}
	if progress != nil {
		progress(60, "正在等待 Podman API")
	}

	deadline := time.Now().Add(startRuntimeWindow)
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, detectProbeTimeout)
		detected, failure := probeCandidates(probeCtx, podmanProbeCandidates())
		cancel()
		if failure == nil {
			cli, err := client.New(client.WithHost(detected.Host), client.WithAPIVersionNegotiation())
			if err == nil {
				r.markAvailable(cli, detected)
				if progress != nil {
					progress(100, "容器运行时已启动")
				}
				return nil
			}
			lastErr = err
		} else {
			lastErr = failure
		}
		if time.Now().After(deadline) {
			break
		}
		timer := time.NewTimer(startRuntimePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %v", plugin.ErrRuntimeUnavailable, lastErr)
}

func (r *Runtime) markAvailable(cli *client.Client, detected Detected) {
	r.clientMu.Lock()
	previous := r.client
	r.client = cli
	r.clientMu.Unlock()
	r.mu.Lock()
	r.runtimeType = normalizeType(detected.Type)
	r.detectErr = nil
	r.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

func (r *Runtime) PullImage(context.Context, plugin.PullImageRequest) (plugin.ImageInfo, error) {
	return plugin.ImageInfo{}, fmt.Errorf("%w: registry pull is not implemented in this version", plugin.ErrUnsupported)
}

type loadMessage struct {
	Stream      string `json:"stream"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func (r *Runtime) ImportImage(ctx context.Context, reader io.Reader, _ plugin.ImportImageRequest) ([]plugin.ImageInfo, error) {
	result, err := r.api().ImageLoad(ctx, reader)
	if err != nil {
		return nil, mapError(err)
	}
	defer result.Close()
	references := make([]string, 0, 2)
	decoder := json.NewDecoder(result)
	for {
		var message loadMessage
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if message.Error != "" || message.ErrorDetail != nil && message.ErrorDetail.Message != "" {
			detail := message.Error
			if detail == "" {
				detail = message.ErrorDetail.Message
			}
			return nil, errors.New(detail)
		}
		for _, line := range strings.Split(message.Stream, "\n") {
			line = strings.TrimSpace(line)
			if value, ok := strings.CutPrefix(line, "Loaded image: "); ok && value != "" {
				references = append(references, value)
			}
			if value, ok := strings.CutPrefix(line, "Loaded image ID: "); ok && value != "" {
				references = append(references, value)
			}
		}
	}
	references = uniqueStrings(references)
	if len(references) == 0 {
		return nil, errors.New("docker load did not report a loaded image")
	}
	if len(references) > 20 {
		references = references[:20]
	}
	infos := make([]plugin.ImageInfo, 0, len(references))
	var lastErr error
	for _, reference := range references {
		info, err := r.InspectImage(ctx, reference)
		if err != nil {
			lastErr = err
			continue
		}
		infos = append(infos, *info)
	}
	if len(infos) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("no imported image could be inspected")
	}
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}

func (r *Runtime) InspectImage(ctx context.Context, reference string) (*plugin.ImageInfo, error) {
	result, err := r.api().ImageInspect(ctx, reference)
	if err != nil {
		return nil, mapError(err)
	}
	labels := map[string]string{}
	user := ""
	if result.Config != nil {
		labels = result.Config.Labels
		user = result.Config.User
	}
	references := append([]string(nil), result.RepoTags...)
	if len(references) == 0 {
		references = []string{result.ID}
	}
	digest := result.ID
	if len(result.RepoDigests) > 0 {
		digest = result.RepoDigests[0]
	}
	created := time.Time{}
	if result.Created != "" {
		created, _ = time.Parse(time.RFC3339Nano, result.Created)
	}
	return &plugin.ImageInfo{
		ID: result.ID, References: uniqueStrings(references), Digest: digest,
		SizeBytes: result.Size, Architecture: result.Architecture, OS: result.Os,
		Labels: labels, User: user, Created: created,
	}, nil
}

func (r *Runtime) RemoveImage(ctx context.Context, reference string, force bool) error {
	_, err := r.api().ImageRemove(ctx, reference, client.ImageRemoveOptions{Force: force, PruneChildren: true})
	return mapError(err)
}

func (r *Runtime) EnsureNetwork(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("plugin network name is empty")
	}
	if _, err := r.api().NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return mapError(err)
	}
	_, err := r.api().NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{plugin.LabelManaged: "true", "io.lan-copy.role": "plugin-network"},
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			if _, inspectErr := r.api().NetworkInspect(ctx, name, client.NetworkInspectOptions{}); inspectErr == nil {
				return nil
			}
		}
		return mapError(err)
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
