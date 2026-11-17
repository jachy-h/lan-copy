package plugin

import (
	"context"
	"io"
	"time"
)

// Runtime is the only boundary between the plugin manager and a concrete
// container/process/remote implementation. Docker types never cross it.
type Runtime interface {
	Info(ctx context.Context) (RuntimeInfo, error)

	PullImage(ctx context.Context, req PullImageRequest) (ImageInfo, error)
	ImportImage(ctx context.Context, reader io.Reader, req ImportImageRequest) ([]ImageInfo, error)
	InspectImage(ctx context.Context, reference string) (*ImageInfo, error)
	RemoveImage(ctx context.Context, reference string, force bool) error

	EnsureNetwork(ctx context.Context, name string) error

	Create(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error)
	Start(ctx context.Context, runtimeID string) error
	Stop(ctx context.Context, runtimeID string, timeout time.Duration) error
	Restart(ctx context.Context, runtimeID string, timeout time.Duration) error
	Remove(ctx context.Context, runtimeID string) error

	Inspect(ctx context.Context, runtimeID string) (*InstanceInfo, error)
	List(ctx context.Context, labels map[string]string) ([]InstanceInfo, error)
	Logs(ctx context.Context, runtimeID string, opts LogOptions) (io.ReadCloser, error)
	Stats(ctx context.Context, runtimeID string) (*Stats, error)
}

// RuntimeSettings are the deployment-specific choices shared by the manager
// and the Docker implementation.
type RuntimeSettings struct {
	Type        string
	NetworkMode string // loopback | container
	Network     string
}

const (
	LabelManaged    = "io.lan-copy.managed"
	LabelPluginID   = "io.lan-copy.plugin.id"
	LabelInstanceID = "io.lan-copy.instance.id"
	LabelVersion    = "io.lan-copy.version"
)

func (s RuntimeSettings) Normalized() RuntimeSettings {
	if s.Type == "" {
		s.Type = "docker"
	}
	if s.NetworkMode != "container" {
		s.NetworkMode = "loopback"
	}
	if s.Network == "" {
		s.Network = "plugin-network"
	}
	return s
}
