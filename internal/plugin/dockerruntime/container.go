package dockerruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

func (r *Runtime) Create(ctx context.Context, req plugin.CreateInstanceRequest) (*plugin.InstanceInfo, error) {
	config, hostConfig, networking, err := buildCreateConfigs(req)
	if err != nil {
		return nil, err
	}
	result, err := r.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: config, HostConfig: hostConfig, NetworkingConfig: networking, Name: req.Name,
	})
	if err != nil {
		return nil, mapError(err)
	}
	info, err := r.Inspect(ctx, result.ID)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (r *Runtime) Start(ctx context.Context, runtimeID string) error {
	_, err := r.client.ContainerStart(ctx, runtimeID, client.ContainerStartOptions{})
	return mapError(err)
}

func (r *Runtime) Stop(ctx context.Context, runtimeID string, timeout time.Duration) error {
	seconds := int(timeout / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	_, err := r.client.ContainerStop(ctx, runtimeID, client.ContainerStopOptions{Timeout: &seconds})
	return mapError(err)
}

func (r *Runtime) Restart(ctx context.Context, runtimeID string, timeout time.Duration) error {
	seconds := int(timeout / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	_, err := r.client.ContainerRestart(ctx, runtimeID, client.ContainerRestartOptions{Timeout: &seconds})
	return mapError(err)
}

func (r *Runtime) Remove(ctx context.Context, runtimeID string) error {
	_, err := r.client.ContainerRemove(ctx, runtimeID, client.ContainerRemoveOptions{Force: false, RemoveVolumes: false})
	return mapError(err)
}

func (r *Runtime) Inspect(ctx context.Context, runtimeID string) (*plugin.InstanceInfo, error) {
	result, err := r.client.ContainerInspect(ctx, runtimeID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, mapError(err)
	}
	info := r.describe(result.Container)
	return &info, nil
}

func (r *Runtime) List(ctx context.Context, labels map[string]string) ([]plugin.InstanceInfo, error) {
	filters := make(client.Filters)
	for name, value := range labels {
		filters.Add("label", name+"="+value)
	}
	if len(labels) == 0 {
		filters.Add("label", plugin.LabelManaged+"=true")
	}
	result, err := r.client.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, mapError(err)
	}
	infos := make([]plugin.InstanceInfo, 0, len(result.Items))
	for _, summary := range result.Items {
		inspect, err := r.client.ContainerInspect(ctx, summary.ID, client.ContainerInspectOptions{})
		if err != nil {
			if errdefsNotFound(err) {
				continue
			}
			return nil, mapError(err)
		}
		infos = append(infos, r.describe(inspect.Container))
	}
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

func (r *Runtime) describe(value container.InspectResponse) plugin.InstanceInfo {
	info := plugin.InstanceInfo{
		RuntimeID: value.ID, Name: strings.TrimPrefix(value.Name, "/"),
		Image: value.Image, RestartCount: value.RestartCount,
	}
	if value.Config != nil {
		info.Labels = value.Config.Labels
	}
	if value.State != nil {
		info.Status = string(value.State.Status)
		info.Running = value.State.Running
		info.ExitCode = value.State.ExitCode
		info.OOMKilled = value.State.OOMKilled
		info.Error = value.State.Error
		info.StartedAt = parseDockerTime(value.State.StartedAt)
		info.FinishedAt = parseDockerTime(value.State.FinishedAt)
	}
	info.HostPort, info.Address = r.address(value)
	return info
}

func (r *Runtime) address(value container.InspectResponse) (int, string) {
	port, ok := exposedPort(value)
	if !ok {
		return 0, ""
	}
	if r.settings.NetworkMode == "container" {
		name := strings.TrimPrefix(value.Name, "/")
		if name == "" {
			return 0, ""
		}
		return 0, fmt.Sprintf("http://%s:%d", name, port.Num())
	}
	if value.NetworkSettings == nil {
		return 0, ""
	}
	bindings := value.NetworkSettings.Ports[port]
	for _, binding := range bindings {
		if binding.HostPort == "" {
			continue
		}
		hostPort, err := strconv.Atoi(binding.HostPort)
		if err != nil || hostPort <= 0 {
			continue
		}
		return hostPort, fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	}
	return 0, ""
}

func exposedPort(value container.InspectResponse) (network.Port, bool) {
	if value.Config != nil {
		for port := range value.Config.ExposedPorts {
			if strings.EqualFold(string(port.Proto()), "tcp") {
				return port, true
			}
		}
	}
	if value.HostConfig != nil {
		for port := range value.HostConfig.PortBindings {
			if strings.EqualFold(string(port.Proto()), "tcp") {
				return port, true
			}
		}
	}
	return network.Port{}, false
}

func parseDockerTime(value string) time.Time {
	if value == "" || strings.HasPrefix(value, "0001-01-01") {
		return time.Time{}
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func (r *Runtime) Logs(ctx context.Context, runtimeID string, opts plugin.LogOptions) (io.ReadCloser, error) {
	options := client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Timestamps: opts.Timestamps,
		Tail: strconv.Itoa(opts.Tail),
	}
	if !opts.Since.IsZero() {
		options.Since = strconv.FormatInt(opts.Since.Unix(), 10)
	}
	source, err := r.client.ContainerLogs(ctx, runtimeID, options)
	if err != nil {
		return nil, mapError(err)
	}
	reader, writer := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(writer, writer, source)
		_ = writer.CloseWithError(copyErr)
	}()
	return &logReadCloser{PipeReader: reader, source: source}, nil
}

type logReadCloser struct {
	*io.PipeReader
	source io.Closer
}

func (l *logReadCloser) Close() error {
	readerErr := l.PipeReader.Close()
	sourceErr := l.source.Close()
	if readerErr != nil {
		return readerErr
	}
	return sourceErr
}

func (r *Runtime) Stats(ctx context.Context, runtimeID string) (*plugin.Stats, error) {
	result, err := r.client.ContainerStats(ctx, runtimeID, client.ContainerStatsOptions{Stream: false, IncludePreviousSample: true})
	if err != nil {
		return nil, mapError(err)
	}
	defer result.Body.Close()
	var response container.StatsResponse
	decoder := json.NewDecoder(io.LimitReader(result.Body, 4<<20))
	if err := decoder.Decode(&response); err != nil {
		return nil, err
	}
	stats := &plugin.Stats{
		MemoryBytes:      int64(response.MemoryStats.Usage),
		MemoryLimitBytes: int64(response.MemoryStats.Limit),
		PIDs:             int64(response.PidsStats.Current),
		SampledAt:        response.Read,
	}
	cpuDelta := float64(response.CPUStats.CPUUsage.TotalUsage) - float64(response.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(response.CPUStats.SystemUsage) - float64(response.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && systemDelta > 0 {
		online := float64(response.CPUStats.OnlineCPUs)
		if online == 0 {
			online = float64(len(response.CPUStats.CPUUsage.PercpuUsage))
		}
		if online == 0 {
			online = 1
		}
		stats.CPUPercent = cpuDelta / systemDelta * online * 100
	}
	return stats, nil
}

func errdefsNotFound(err error) bool {
	return mapError(err) == plugin.ErrNotFound
}
