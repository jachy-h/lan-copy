package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type fakeRuntime struct {
	mu           sync.Mutex
	info         RuntimeInfo
	imported     []ImageInfo
	containers   map[string]*InstanceInfo
	created      []CreateInstanceRequest
	address      string
	nextID       int
	failNext     error
	startErr     error
	statsErr     error
	statsErrByID map[string]error
	logs         string
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		info: RuntimeInfo{
			Type: "docker", Available: true, Version: "test", OS: "linux", Architecture: "arm64",
			NetworkMode: "loopback", Network: "plugin-network",
		},
		containers: map[string]*InstanceInfo{},
	}
}

func (f *fakeRuntime) Info(context.Context) (RuntimeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}

// StartRuntime simulates the plugin page bringing Podman up.
func (f *fakeRuntime) StartRuntime(_ context.Context, progress func(int, string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if progress != nil {
		progress(50, "starting podman")
	}
	if f.startErr != nil {
		err := f.startErr
		f.startErr = nil
		return err
	}
	f.info.Type = "podman"
	f.info.Available = true
	f.info.Error = ""
	f.info.StartAvailable = false
	return nil
}

func (f *fakeRuntime) PullImage(context.Context, PullImageRequest) (ImageInfo, error) {
	return ImageInfo{}, ErrUnsupported
}

func (f *fakeRuntime) ImportImage(_ context.Context, reader io.Reader, _ ImportImageRequest) ([]ImageInfo, error) {
	_, _ = io.Copy(io.Discard, reader)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	return append([]ImageInfo(nil), f.imported...), nil
}

func (f *fakeRuntime) InspectImage(_ context.Context, reference string) (*ImageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, image := range f.imported {
		if image.ID == reference {
			copy := image
			return &copy, nil
		}
		for _, ref := range image.References {
			if ref == reference {
				copy := image
				return &copy, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRuntime) RemoveImage(context.Context, string, bool) error { return nil }

func (f *fakeRuntime) EnsureNetwork(context.Context, string) error { return nil }

func (f *fakeRuntime) Create(_ context.Context, req CreateInstanceRequest) (*InstanceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return nil, err
	}
	f.nextID++
	id := fmt.Sprintf("container-%d", f.nextID)
	info := &InstanceInfo{
		RuntimeID: id, Name: req.Name, Image: req.ImageRef,
		Status: "created", Address: f.address, Labels: req.Labels,
	}
	f.containers[id] = info
	f.created = append(f.created, req)
	copy := *info
	return &copy, nil
}

func (f *fakeRuntime) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.containers[id]
	if !ok {
		return ErrNotFound
	}
	info.Running = true
	info.Status = "running"
	info.StartedAt = time.Now().UTC()
	return nil
}

func (f *fakeRuntime) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.containers[id]
	if !ok {
		return ErrNotFound
	}
	info.Running = false
	info.Status = "exited"
	return nil
}

func (f *fakeRuntime) Restart(ctx context.Context, id string, timeout time.Duration) error {
	if err := f.Stop(ctx, id, timeout); err != nil {
		return err
	}
	return f.Start(ctx, id)
}

func (f *fakeRuntime) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[id]; !ok {
		return ErrNotFound
	}
	delete(f.containers, id)
	return nil
}

func (f *fakeRuntime) Inspect(_ context.Context, id string) (*InstanceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.containers[id]
	if !ok {
		return nil, ErrNotFound
	}
	copy := *info
	return &copy, nil
}

func (f *fakeRuntime) List(_ context.Context, labels map[string]string) ([]InstanceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]InstanceInfo, 0, len(f.containers))
	for _, info := range f.containers {
		match := true
		for name, value := range labels {
			if info.Labels[name] != value {
				match = false
				break
			}
		}
		if match {
			out = append(out, *info)
		}
	}
	return out, nil
}

func (f *fakeRuntime) Logs(context.Context, string, LogOptions) (io.ReadCloser, error) {
	if f.logs == "" {
		f.logs = "plugin log line\n"
	}
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func (f *fakeRuntime) Stats(_ context.Context, runtimeID string) (*Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	if err := f.statsErrByID[runtimeID]; err != nil {
		return nil, err
	}
	return &Stats{CPUPercent: 1.5, MemoryBytes: 1024, MemoryLimitBytes: 2048, PIDs: 3}, nil
}

func (f *fakeRuntime) setContainerRunning(id string, running bool, exitCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if info, ok := f.containers[id]; ok {
		info.Running = running
		if running {
			info.Status = "running"
		} else {
			info.Status = "exited"
		}
		info.ExitCode = exitCode
	}
}

func (f *fakeRuntime) removeContainer(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.containers, id)
}

var errFake = errors.New("fake runtime failure")
