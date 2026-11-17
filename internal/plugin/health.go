package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

type healthPayload struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// probeHealth performs one bounded health request and returns the record.
func (m *Manager) probeHealth(ctx context.Context, instance Instance) (HealthRecord, bool) {
	record := HealthRecord{At: m.now(), Status: string(HealthUnhealthy)}
	if instance.InternalAddress == "" {
		record.Error = "实例没有可用地址"
		return record, false
	}
	timeout := time.Duration(instance.Config.HealthCheck.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 3 * time.Second
	}
	path := instance.Config.HealthCheck.Path
	if path == "" {
		path = "/health"
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(instance.InternalAddress, "/")+path, nil)
	if err != nil {
		record.Error = truncate(err.Error(), MaxHealthError)
		return record, false
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.httpClient.Do(request)
	record.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		record.Error = truncate(friendlyClientError(err), MaxHealthError)
		return record, false
	}
	defer response.Body.Close()
	record.StatusCode = response.StatusCode
	if response.StatusCode != http.StatusOK {
		record.Error = fmt.Sprintf("健康检查返回 %d", response.StatusCode)
		return record, false
	}
	var payload healthPayload
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&payload); err != nil {
		record.Error = "健康检查响应不是合法 JSON"
		return record, false
	}
	if !strings.EqualFold(strings.TrimSpace(payload.Status), "ok") {
		record.Error = fmt.Sprintf("健康检查状态为 %q", truncate(payload.Status, 128))
		return record, false
	}
	record.Status = string(HealthHealthy)
	return record, true
}

func friendlyClientError(err error) string {
	if err == nil {
		return ""
	}
	var netError interface{ Timeout() bool }
	if errors.As(err, &netError) && netError.Timeout() {
		return "健康检查超时"
	}
	return err.Error()
}

func (m *Manager) healthLoop(ctx context.Context) {
	for _, instance := range m.instances() {
		if instance.Status != StatusRunning && instance.Status != StatusUnhealthy {
			continue
		}
		record, healthy := m.probeHealth(ctx, instance)
		if ctx.Err() != nil {
			return
		}
		if err := m.applyHealth(instance, record, healthy); err != nil {
			m.logger.Printf("保存插件健康记录失败: %v", err)
		}
	}
}

func (m *Manager) applyHealth(instance Instance, record HealthRecord, healthy bool) error {
	m.mu.Lock()
	record.Status = truncate(record.Status, 32)
	records := append(m.state.HealthRecords[instance.ID], record)
	if len(records) > MaxHealthRecords {
		records = append([]HealthRecord(nil), records[len(records)-MaxHealthRecords:]...)
	}
	m.state.HealthRecords[instance.ID] = records
	now := record.At
	for index := range m.state.Instances {
		current := &m.state.Instances[index]
		if current.ID != instance.ID {
			continue
		}
		current.LastHealthAt = &now
		if healthy {
			current.HealthStatus = HealthHealthy
			if current.Status == StatusUnhealthy {
				current.Status = StatusRunning
				current.StatusReason = ""
			}
		} else {
			current.HealthStatus = HealthUnhealthy
			if current.Status == StatusRunning {
				current.Status = StatusUnhealthy
				current.StatusReason = record.Error
			}
		}
		current.UpdatedAt = m.now()
		if pluginIndex := pluginIndex(m.state.Plugins, current.PluginID); pluginIndex >= 0 {
			m.state.Plugins[pluginIndex].Status = current.Status
			m.state.Plugins[pluginIndex].UpdatedAt = current.UpdatedAt
		}
		break
	}
	m.mu.Unlock()
	return m.persist()
}

// verifyManifest fetches and validates the manifest served after startup.
func (m *Manager) verifyManifest(ctx context.Context, instance Instance, expected Manifest) ([]string, error) {
	if instance.InternalAddress == "" {
		return nil, fmt.Errorf("%w: no instance address", ErrPluginProtocolMismatch)
	}
	timeout := time.Duration(instance.Config.HealthCheck.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := strings.TrimRight(instance.InternalAddress, "/") + expected.Endpoints.Manifest
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := m.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPluginUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: manifest returned %d", ErrPluginProtocolMismatch, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	served, err := ParseManifest(string(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPluginProtocolMismatch, err)
	}
	if served.ID != expected.ID {
		return nil, fmt.Errorf("%w: served id %q does not match installed id %q", ErrPluginProtocolMismatch, served.ID, expected.ID)
	}
	if !ProtocolCompatible(served.ProtocolVersion) {
		return nil, fmt.Errorf("%w: served protocol %q", ErrPluginProtocolMismatch, served.ProtocolVersion)
	}
	var warnings []string
	if served.Version != expected.Version {
		warnings = append(warnings, fmt.Sprintf("插件运行版本 %s 与镜像 Manifest 版本 %s 不一致", served.Version, expected.Version))
	}
	return warnings, nil
}

// reconcile aligns persisted records with actual runtime containers.
func (m *Manager) reconcile(ctx context.Context) {
	reconcileCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	infos, err := m.runtime.List(reconcileCtx, map[string]string{LabelManaged: "true"})
	if err != nil {
		m.logRuntimeError(err)
		return
	}
	m.clearRuntimeError()
	byID := make(map[string]InstanceInfo, len(infos))
	byInstance := make(map[string]InstanceInfo, len(infos))
	for _, info := range infos {
		byID[info.RuntimeID] = info
		if id := infoLabel(info, LabelInstanceID); id != "" {
			byInstance[id] = info
		}
	}
	orphans := make([]string, 0)
	changed := false
	m.mu.Lock()
	known := make(map[string]struct{}, len(m.state.Instances))
	for _, instance := range m.state.Instances {
		known[instance.ID] = struct{}{}
	}
	for _, info := range infos {
		if id := infoLabel(info, LabelInstanceID); id != "" {
			if _, ok := known[id]; !ok {
				orphans = append(orphans, info.Name)
			}
		}
	}
	for index := range m.state.Instances {
		instance := &m.state.Instances[index]
		info, ok := byID[instance.RuntimeInstanceID]
		if !ok {
			info, ok = byInstance[instance.ID]
		}
		if !ok {
			if instance.Status == StatusRunning || instance.Status == StatusUnhealthy || instance.Status == StatusStarting || instance.Status == StatusStopping || instance.Status == StatusFailed {
				instance.Status = StatusFailed
				instance.HealthStatus = HealthUnknown
				instance.StatusReason = "容器不存在，可尝试重新启动"
				instance.UpdatedAt = m.now()
				m.syncPluginLocked(instance)
				changed = true
			}
			continue
		}
		statusBefore := instance.Status
		if applyObservation(instance, info, m.now()) {
			if instance.Status != statusBefore {
				m.syncPluginLocked(instance)
			}
			changed = true
		}
	}
	m.orphans = orphans
	m.mu.Unlock()
	if changed {
		if err := m.persist(); err != nil {
			m.logger.Printf("插件状态同步写盘失败: %v", err)
		}
	}
}

func applyObservation(instance *Instance, info InstanceInfo, now time.Time) bool {
	before := *instance
	if info.RuntimeID != "" && instance.RuntimeInstanceID != info.RuntimeID {
		instance.RuntimeInstanceID = info.RuntimeID
		instance.ContainerName = info.Name
	}
	if instance.InternalAddress != info.Address {
		instance.InternalAddress = info.Address
	}
	if instance.HostPort != info.HostPort {
		instance.HostPort = info.HostPort
	}
	if instance.RestartCount != info.RestartCount {
		instance.RestartCount = info.RestartCount
	}
	if !info.StartedAt.IsZero() && (instance.StartedAt == nil || !instance.StartedAt.Equal(info.StartedAt)) {
		started := info.StartedAt
		instance.StartedAt = &started
	}
	applyObservedStatus(instance, info, now)
	return !reflect.DeepEqual(before, *instance)
}

func applyObservedStatus(instance *Instance, info InstanceInfo, now time.Time) {
	if instance.FatalError || instance.Status == StatusStarting || instance.Status == StatusStopping || instance.Status == StatusInstalling || instance.Status == StatusUninstalling {
		return
	}
	previousStatus := instance.Status
	previousReason := instance.StatusReason
	previousHealth := instance.HealthStatus
	switch info.Status {
	case "running":
		if instance.Status != StatusUnhealthy {
			instance.Status = StatusRunning
			instance.StatusReason = ""
		}
	case "restarting":
		instance.Status = StatusStarting
		instance.StatusReason = "容器正在重启"
	case "created":
		instance.Status = StatusStopped
	case "paused":
		instance.Status = StatusFailed
		instance.StatusReason = "容器已暂停"
	case "exited", "dead":
		instance.HealthStatus = HealthUnknown
		if info.OOMKilled {
			instance.Status = StatusFailed
			instance.StatusReason = "容器因内存不足被终止"
		} else if info.ExitCode != 0 {
			instance.Status = StatusFailed
			instance.StatusReason = fmt.Sprintf("容器异常退出（退出码 %d）", info.ExitCode)
		} else {
			instance.Status = StatusStopped
			instance.StatusReason = ""
		}
	default:
		instance.Status = StatusFailed
		instance.StatusReason = "容器状态未知: " + info.Status
	}
	if previousStatus != instance.Status || previousReason != instance.StatusReason || previousHealth != instance.HealthStatus {
		instance.UpdatedAt = now
	}
}

func (m *Manager) syncPluginLocked(instance *Instance) {
	if pluginIndex := pluginIndex(m.state.Plugins, instance.PluginID); pluginIndex >= 0 {
		m.state.Plugins[pluginIndex].Status = instance.Status
		m.state.Plugins[pluginIndex].UpdatedAt = instance.UpdatedAt
	}
}

func infoLabel(info InstanceInfo, label string) string {
	if info.Labels == nil {
		return ""
	}
	return info.Labels[label]
}

func (m *Manager) logRuntimeError(err error) {
	if errors.Is(err, ErrRuntimeUnavailable) {
		m.mu.Lock()
		last := m.lastRuntimeError
		m.lastRuntimeError = "unavailable"
		m.mu.Unlock()
		if last != "unavailable" {
			m.logger.Printf("插件运行时不可用: %v", err)
		}
		return
	}
	m.logger.Printf("插件状态同步失败: %v", err)
}

func (m *Manager) clearRuntimeError() {
	m.mu.Lock()
	m.lastRuntimeError = ""
	m.mu.Unlock()
}
