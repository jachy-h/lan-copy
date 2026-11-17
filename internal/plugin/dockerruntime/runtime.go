// Package dockerruntime implements the plugin Runtime boundary with the
// official Docker Engine SDK. It is the only package that imports Docker types.
package dockerruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

type Runtime struct {
	client   *client.Client
	settings plugin.RuntimeSettings
}

type Options struct {
	Host     string
	Settings plugin.RuntimeSettings
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
	return &Runtime{client: cli, settings: opts.Settings.Normalized()}, nil
}

func (r *Runtime) Info(ctx context.Context) (plugin.RuntimeInfo, error) {
	ping, err := r.client.Ping(ctx, client.PingOptions{})
	if err != nil {
		return plugin.RuntimeInfo{Type: "docker", NetworkMode: r.settings.NetworkMode, Network: r.settings.Network}, mapError(err)
	}
	version, err := r.client.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return plugin.RuntimeInfo{Type: "docker", NetworkMode: r.settings.NetworkMode, Network: r.settings.Network}, mapError(err)
	}
	return plugin.RuntimeInfo{
		Type: "docker", Available: true, Version: version.Version, APIVersion: ping.APIVersion,
		OS: version.Os, Architecture: version.Arch,
		NetworkMode: r.settings.NetworkMode, Network: r.settings.Network,
	}, nil
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
	result, err := r.client.ImageLoad(ctx, reader)
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
	result, err := r.client.ImageInspect(ctx, reference)
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
	_, err := r.client.ImageRemove(ctx, reference, client.ImageRemoveOptions{Force: force, PruneChildren: true})
	return mapError(err)
}

func (r *Runtime) EnsureNetwork(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("plugin network name is empty")
	}
	if _, err := r.client.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return mapError(err)
	}
	_, err := r.client.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{plugin.LabelManaged: "true", "io.lan-copy.role": "plugin-network"},
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			if _, inspectErr := r.client.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); inspectErr == nil {
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
