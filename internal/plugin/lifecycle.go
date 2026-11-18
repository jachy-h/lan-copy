package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	defaultStopTimeout = 10 * time.Second
	startPollInterval  = 500 * time.Millisecond
)

// Install creates the plugin and container records, then optionally starts and
// verifies the service. It never pulls or imports an image.
func (m *Manager) Install(ctx context.Context, req InstallRequest) (InstallResult, error) {
	image, err := m.Image(req.ImageID)
	if err != nil {
		return InstallResult{}, err
	}
	if req.ExpectedPluginID != "" && req.ExpectedPluginID != image.PluginID {
		return InstallResult{}, fmt.Errorf("%w: image plugin id %q does not match %q", ErrInvalidManifest, image.PluginID, req.ExpectedPluginID)
	}
	config := MergeConfig(DefaultConfig(image.Manifest), req.Config)
	if err := m.validateConfig(&config, image.Manifest); err != nil {
		return InstallResult{}, err
	}

	unlock, err := m.lockInstance(image.PluginID)
	if err != nil {
		return InstallResult{}, err
	}
	defer unlock()

	m.mu.Lock()
	if _, exists := instanceForPlugin(m.state.Instances, image.PluginID); exists {
		m.mu.Unlock()
		return InstallResult{}, fmt.Errorf("%w: plugin %s", ErrAlreadyInstalled, image.PluginID)
	}
	instanceID, err := randomID("inst-")
	if err != nil {
		m.mu.Unlock()
		return InstallResult{}, err
	}
	now := m.now()
	instance := Instance{
		ID: instanceID, PluginID: image.PluginID, ImageID: image.ID,
		RuntimeType: m.settings.Type, ContainerName: containerName(image.PluginID, instanceID),
		Status: StatusInstalling, HealthStatus: HealthUnknown, Config: config,
		CreatedAt: now, UpdatedAt: now,
	}
	previousStatus := StatusNotInstalled
	if index := pluginIndex(m.state.Plugins, image.PluginID); index >= 0 {
		previousStatus = m.state.Plugins[index].Status
		m.state.Plugins[index].Status = StatusInstalling
		m.state.Plugins[index].CurrentVersion = image.Manifest.Version
		m.state.Plugins[index].UpdatedAt = now
	} else {
		m.state.Plugins = append(m.state.Plugins, Plugin{
			ID: image.PluginID, Name: image.Manifest.Name, Description: image.Manifest.Description,
			Category: image.Manifest.Category, ProtocolVersion: image.Manifest.ProtocolVersion,
			CurrentVersion: image.Manifest.Version, Status: StatusInstalling,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	m.state.Instances = append(m.state.Instances, instance)
	snapshot := m.state.Clone()
	m.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return InstallResult{}, err
	}

	info, err := m.createContainer(ctx, instance, image)
	if err != nil {
		m.rollbackInstall(image.PluginID, instance.ID, previousStatus)
		return InstallResult{}, err
	}
	if err := m.updateInstance(image.PluginID, func(current *Instance) {
		current.RuntimeInstanceID = info.RuntimeID
		current.InternalAddress = info.Address
		current.HostPort = info.HostPort
		current.Status = StatusStopped
		current.StatusReason = ""
	}); err != nil {
		return InstallResult{}, err
	}
	if req.Start {
		if err := m.startLocked(ctx, image.PluginID); err != nil {
			return InstallResult{}, err
		}
	}
	return m.installResult(image.PluginID)
}

func (m *Manager) installResult(pluginID string) (InstallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pluginEntry, ok := m.pluginLocked(pluginID)
	if !ok {
		return InstallResult{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		return InstallResult{}, fmt.Errorf("%w: plugin %s instance", ErrNotFound, pluginID)
	}
	return InstallResult{Plugin: pluginEntry, Instance: MaskInstance(instance)}, nil
}

func (m *Manager) rollbackInstall(pluginID, instanceID string, previousStatus Status) {
	m.mu.Lock()
	instances := m.state.Instances[:0]
	for _, instance := range m.state.Instances {
		if instance.ID != instanceID {
			instances = append(instances, instance)
		}
	}
	m.state.Instances = instances
	delete(m.state.HealthRecords, instanceID)
	if index := pluginIndex(m.state.Plugins, pluginID); index >= 0 {
		m.state.Plugins[index].Status = previousStatus
		m.state.Plugins[index].UpdatedAt = m.now()
	}
	snapshot := m.state.Clone()
	m.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		m.logger.Printf("回滚插件安装记录失败: %v", err)
	}
}

func (m *Manager) createContainer(ctx context.Context, instance Instance, image Image) (*InstanceInfo, error) {
	if err := m.runtime.EnsureNetwork(ctx, m.settings.Network); err != nil {
		return nil, err
	}
	env := envList(instance.Config.Env)
	if token, err := m.store.EventToken(instance.ID); err == nil && token != "" {
		env = append(env, EventTokenEnv+"="+token)
	}
	return m.runtime.Create(ctx, CreateInstanceRequest{
		Name: instance.ContainerName, ImageRef: image.RuntimeImageID,
		Env: env, ContainerPort: image.Manifest.ContainerPort,
		NetworkName: m.settings.Network, PublishLoopback: m.settings.NetworkMode == "loopback",
		Mounts: instance.Config.Mounts, CPULimit: instance.Config.CPULimit,
		MemoryMB: instance.Config.MemoryMB, PIDsLimit: instance.Config.PIDsLimit,
		RestartPolicy: instance.Config.RestartPolicy, LogMaxSizeMB: instance.Config.LogMaxSizeMB,
		LogMaxFiles: instance.Config.LogMaxFiles,
		Labels: map[string]string{
			LabelManaged: "true", LabelPluginID: instance.PluginID,
			LabelInstanceID: instance.ID, LabelVersion: image.Manifest.Version,
		},
		StopTimeoutSecs: int(defaultStopTimeout / time.Second),
	})
}

// Start brings the instance to running only after health and manifest checks.
func (m *Manager) Start(ctx context.Context, pluginID string) error {
	unlock, err := m.lockInstance(pluginID)
	if err != nil {
		return err
	}
	defer unlock()
	return m.startLocked(ctx, pluginID)
}

func (m *Manager) startLocked(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	image, ok := imageByID(m.state.Images, instance.ImageID)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: plugin image is missing", ErrConflict)
	}
	if instance.Status == StatusRunning && instance.HealthStatus == HealthHealthy && !instance.ConfigDirty {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	startupTimeout := time.Duration(instance.Config.HealthCheck.StartupTimeoutSeconds) * time.Second
	if startupTimeout <= 0 || startupTimeout > MaxInstallTimeout {
		startupTimeout = 60 * time.Second
	}
	operationCtx, cancel := context.WithTimeout(ctx, startupTimeout+45*time.Second)
	defer cancel()

	if err := m.updateInstance(pluginID, func(current *Instance) {
		current.Status = StatusStarting
		current.HealthStatus = HealthChecking
		current.StatusReason = ""
		current.FatalError = false
	}); err != nil {
		return err
	}
	if err := m.ensureContainer(operationCtx, pluginID, image); err != nil {
		_ = m.updateInstance(pluginID, func(current *Instance) {
			current.Status = StatusFailed
			current.StatusReason = friendlyRuntimeError(err)
		})
		return err
	}
	m.mu.Lock()
	instance, ok = instanceForPlugin(m.state.Instances, pluginID)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if err := m.runtime.Start(operationCtx, instance.RuntimeInstanceID); err != nil {
		_ = m.updateInstance(pluginID, func(current *Instance) {
			current.Status = StatusFailed
			current.StatusReason = friendlyRuntimeError(err)
		})
		return err
	}
	// Published ports are not always visible until the container starts.
	if started, inspectErr := m.runtime.Inspect(operationCtx, instance.RuntimeInstanceID); inspectErr == nil {
		instance.InternalAddress = started.Address
		instance.HostPort = started.HostPort
		_ = m.updateInstance(pluginID, func(current *Instance) {
			current.InternalAddress = started.Address
			current.HostPort = started.HostPort
		})
	}

	deadline := time.Now().Add(startupTimeout)
	var lastRecord HealthRecord
	var lastHealthy bool
	for {
		if err := ctx.Err(); err != nil {
			_ = m.finishStartFailure(pluginID, lastRecord, "启动已取消")
			return err
		}
		info, err := m.runtime.Inspect(operationCtx, instance.RuntimeInstanceID)
		if err != nil {
			_ = m.finishStartFailure(pluginID, lastRecord, friendlyRuntimeError(err))
			return err
		}
		if !info.Running {
			reason := fmt.Sprintf("容器未保持运行（状态 %s，退出码 %d）", info.Status, info.ExitCode)
			if info.Error != "" {
				reason += ": " + info.Error
			}
			_ = m.finishStartFailure(pluginID, lastRecord, reason)
			return fmt.Errorf("%w: %s", ErrPluginNotRunning, reason)
		}
		if instance.InternalAddress == "" && info.Address != "" {
			instance.InternalAddress = info.Address
			instance.HostPort = info.HostPort
			_ = m.updateInstance(pluginID, func(current *Instance) {
				current.InternalAddress = info.Address
				current.HostPort = info.HostPort
			})
		}
		lastRecord, lastHealthy = m.probeHealth(operationCtx, instance)
		if lastHealthy {
			warnings, err := m.verifyManifest(operationCtx, instance, image.Manifest)
			if err == nil {
				return m.finishStartSuccess(pluginID, instance, image, lastRecord, warnings)
			}
			lastRecord = HealthRecord{
				At: m.now(), Status: string(HealthUnhealthy), LatencyMS: lastRecord.LatencyMS,
				Error: truncate(err.Error(), MaxHealthError),
			}
			// An identity/protocol mismatch is not a transient health issue.
			_ = m.runtime.Stop(operationCtx, instance.RuntimeInstanceID, defaultStopTimeout)
			_ = m.finishStartFailureStatus(pluginID, lastRecord, lastRecord.Error, StatusFailed, true)
			return err
		}
		if time.Now().After(deadline) {
			reason := lastRecord.Error
			if reason == "" {
				reason = "健康检查在启动超时前未通过"
			}
			_ = m.finishStartFailure(pluginID, lastRecord, reason)
			return fmt.Errorf("%w: %s", ErrPluginUnhealthy, reason)
		}
		select {
		case <-ctx.Done():
		case <-time.After(startPollInterval):
		}
	}
}

func (m *Manager) finishStartSuccess(pluginID string, instance Instance, image Image, record HealthRecord, warnings []string) error {
	m.mu.Lock()
	record.Status = string(HealthHealthy)
	now := record.At
	for index := range m.state.Instances {
		current := &m.state.Instances[index]
		if current.ID != instance.ID {
			continue
		}
		current.Status = StatusRunning
		current.HealthStatus = HealthHealthy
		current.StatusReason = ""
		current.FatalError = false
		current.Warnings = warnings
		current.LastHealthAt = &now
		current.StartedAt = &now
		current.InternalAddress = instance.InternalAddress
		current.HostPort = instance.HostPort
		current.UpdatedAt = m.now()
		if pluginIndex := pluginIndex(m.state.Plugins, pluginID); pluginIndex >= 0 {
			m.state.Plugins[pluginIndex].Status = StatusRunning
			m.state.Plugins[pluginIndex].CurrentVersion = image.Manifest.Version
			m.state.Plugins[pluginIndex].UpdatedAt = current.UpdatedAt
		}
		break
	}
	records := append(m.state.HealthRecords[instance.ID], record)
	if len(records) > MaxHealthRecords {
		records = append([]HealthRecord(nil), records[len(records)-MaxHealthRecords:]...)
	}
	m.state.HealthRecords[instance.ID] = records
	snapshot := m.state.Clone()
	m.mu.Unlock()
	return m.store.Save(snapshot)
}

func (m *Manager) finishStartFailure(pluginID string, record HealthRecord, reason string) error {
	return m.finishStartFailureStatus(pluginID, record, reason, StatusUnhealthy, false)
}

func (m *Manager) finishStartFailureStatus(pluginID string, record HealthRecord, reason string, status Status, fatal bool) error {
	m.mu.Lock()
	for index := range m.state.Instances {
		current := &m.state.Instances[index]
		if current.PluginID != pluginID {
			continue
		}
		current.Status = status
		current.HealthStatus = HealthUnhealthy
		current.StatusReason = truncate(reason, MaxHealthError)
		current.FatalError = fatal
		current.UpdatedAt = m.now()
		if !record.At.IsZero() {
			now := record.At
			current.LastHealthAt = &now
			record.Status = string(HealthUnhealthy)
			records := append(m.state.HealthRecords[current.ID], record)
			if len(records) > MaxHealthRecords {
				records = append([]HealthRecord(nil), records[len(records)-MaxHealthRecords:]...)
			}
			m.state.HealthRecords[current.ID] = records
		}
		if pluginIndex := pluginIndex(m.state.Plugins, pluginID); pluginIndex >= 0 {
			m.state.Plugins[pluginIndex].Status = status
			m.state.Plugins[pluginIndex].UpdatedAt = current.UpdatedAt
		}
		break
	}
	snapshot := m.state.Clone()
	m.mu.Unlock()
	return m.store.Save(snapshot)
}

// ensureContainer restarts an existing container or recreates one that no
// longer exists, using the persisted validated configuration.
func (m *Manager) ensureContainer(ctx context.Context, pluginID string, image Image) error {
	m.mu.Lock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if instance.ConfigDirty && instance.RuntimeInstanceID != "" {
		if err := m.runtime.Remove(ctx, instance.RuntimeInstanceID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		instance.RuntimeInstanceID = ""
		if err := m.updateInstance(pluginID, func(current *Instance) {
			current.RuntimeInstanceID = ""
			current.InternalAddress = ""
			current.HostPort = 0
		}); err != nil {
			return err
		}
	}
	if instance.RuntimeInstanceID != "" {
		if info, err := m.runtime.Inspect(ctx, instance.RuntimeInstanceID); err == nil {
			if info.Address != instance.InternalAddress || info.HostPort != instance.HostPort {
				_ = m.updateInstance(pluginID, func(current *Instance) {
					current.InternalAddress = info.Address
					current.HostPort = info.HostPort
				})
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	info, err := m.createContainer(ctx, instance, image)
	if err != nil {
		return err
	}
	return m.updateInstance(pluginID, func(current *Instance) {
		current.RuntimeInstanceID = info.RuntimeID
		current.InternalAddress = info.Address
		current.HostPort = info.HostPort
		current.ContainerName = info.Name
		current.ConfigDirty = false
	})
}

func (m *Manager) Stop(ctx context.Context, pluginID string) error {
	unlock, err := m.lockInstance(pluginID)
	if err != nil {
		return err
	}
	defer unlock()
	return m.stopLocked(ctx, pluginID)
}

func (m *Manager) stopLocked(ctx context.Context, pluginID string) error {
	m.mu.Lock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if instance.RuntimeInstanceID == "" {
		return m.updateInstance(pluginID, func(current *Instance) {
			current.Status = StatusStopped
			current.HealthStatus = HealthUnknown
			current.StatusReason = ""
		})
	}
	if err := m.updateInstance(pluginID, func(current *Instance) {
		current.Status = StatusStopping
		current.StatusReason = ""
	}); err != nil {
		return err
	}
	stopCtx, cancel := context.WithTimeout(ctx, defaultStopTimeout+5*time.Second)
	defer cancel()
	err := m.runtime.Stop(stopCtx, instance.RuntimeInstanceID, defaultStopTimeout)
	if err != nil && !errors.Is(err, ErrNotFound) {
		_ = m.updateInstance(pluginID, func(current *Instance) {
			current.Status = StatusFailed
			current.StatusReason = friendlyRuntimeError(err)
		})
		return err
	}
	return m.updateInstance(pluginID, func(current *Instance) {
		current.Status = StatusStopped
		current.HealthStatus = HealthUnknown
		current.StatusReason = ""
	})
}

func (m *Manager) Restart(ctx context.Context, pluginID string) error {
	unlock, err := m.lockInstance(pluginID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.stopLocked(ctx, pluginID); err != nil {
		return err
	}
	return m.startLocked(ctx, pluginID)
}

// Uninstall removes the container and records. Images and data are retained
// unless explicitly requested.
func (m *Manager) Uninstall(ctx context.Context, pluginID string, req UninstallRequest) error {
	unlock, err := m.lockInstance(pluginID)
	if err != nil {
		return err
	}
	defer unlock()

	m.mu.Lock()
	instance, hasInstance := instanceForPlugin(m.state.Instances, pluginID)
	images := imagesForPlugin(m.state.Images, pluginID)
	_, hasPlugin := m.pluginLocked(pluginID)
	m.mu.Unlock()
	if !hasPlugin {
		return fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if hasInstance {
		_ = m.updateInstance(pluginID, func(current *Instance) {
			current.Status = StatusUninstalling
		})
		if instance.RuntimeInstanceID != "" {
			uninstallCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if instance.Status == StatusRunning || instance.Status == StatusUnhealthy || instance.Status == StatusStarting {
				_ = m.runtime.Stop(uninstallCtx, instance.RuntimeInstanceID, defaultStopTimeout)
			}
			if err := m.runtime.Remove(uninstallCtx, instance.RuntimeInstanceID); err != nil && !errors.Is(err, ErrNotFound) {
				_ = m.updateInstance(pluginID, func(current *Instance) {
					current.Status = StatusFailed
					current.StatusReason = friendlyRuntimeError(err)
				})
				return err
			}
		}
	}

	var dataPaths []string
	if req.DeleteData {
		for _, mount := range instance.Config.Mounts {
			path, err := PrepareHostMount(mount.HostPath, m.roots)
			if err != nil {
				return err
			}
			dataPaths = append(dataPaths, path)
		}
	}

	m.mu.Lock()
	instances := m.state.Instances[:0]
	for _, current := range m.state.Instances {
		if current.PluginID != pluginID {
			instances = append(instances, current)
			continue
		}
		delete(m.state.HealthRecords, current.ID)
	}
	m.state.Instances = instances
	if queue, ok := m.eventQueues[pluginID]; ok {
		close(queue)
		delete(m.eventQueues, pluginID)
	}
	delete(m.eventRecords, pluginID)
	delete(m.eventStats, pluginID)
	delete(m.eventCircuit, pluginID)
	if req.DeleteImages {
		kept := m.state.Images[:0]
		for _, image := range m.state.Images {
			if image.PluginID != pluginID {
				kept = append(kept, image)
			}
		}
		m.state.Images = kept
	}
	if pluginIndex := pluginIndex(m.state.Plugins, pluginID); pluginIndex >= 0 {
		if req.DeleteImages || len(imagesForPlugin(m.state.Images, pluginID)) == 0 {
			m.state.Plugins = append(m.state.Plugins[:pluginIndex], m.state.Plugins[pluginIndex+1:]...)
		} else {
			m.state.Plugins[pluginIndex].Status = StatusNotInstalled
			m.state.Plugins[pluginIndex].UpdatedAt = m.now()
		}
	}
	delete(m.metrics, pluginID)
	snapshot := m.state.Clone()
	m.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return err
	}

	if req.DeleteImages {
		for _, image := range images {
			reference := image.RuntimeImageID
			if reference == "" {
				reference = image.Reference
			}
			if err := m.runtime.RemoveImage(ctx, reference, false); err != nil && !errors.Is(err, ErrNotFound) {
				m.logger.Printf("卸载插件时删除镜像 %s 失败: %v", image.Reference, err)
			}
		}
	}
	if req.DeleteData {
		for _, path := range dataPaths {
			if err := os.RemoveAll(path); err != nil {
				m.logger.Printf("删除插件数据目录失败: %v", err)
			}
		}
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, pluginID string) (StatusView, error) {
	m.mu.Lock()
	pluginEntry, ok := m.pluginLocked(pluginID)
	if !ok {
		m.mu.Unlock()
		return StatusView{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	instance, hasInstance := instanceForPlugin(m.state.Instances, pluginID)
	m.mu.Unlock()
	view := StatusView{PluginID: pluginID, Status: pluginEntry.Status, HealthStatus: HealthUnknown}
	if !hasInstance {
		return view, nil
	}
	view = StatusView{
		PluginID: pluginID, InstanceID: instance.ID, Status: instance.Status,
		HealthStatus: instance.HealthStatus, StatusReason: instance.StatusReason,
		Warnings: instance.Warnings, Address: instance.InternalAddress,
		StartedAt: instance.StartedAt, LastHealthAt: instance.LastHealthAt,
		RestartCount: instance.RestartCount,
	}
	m.mu.Lock()
	view.HealthRecords = append([]HealthRecord{}, m.state.HealthRecords[instance.ID]...)
	view.Metrics = m.metricsLocked(pluginID)
	m.mu.Unlock()
	if instance.RuntimeInstanceID != "" {
		if info, err := m.runtime.Inspect(ctx, instance.RuntimeInstanceID); err == nil {
			changed := false
			m.mu.Lock()
			for index := range m.state.Instances {
				if m.state.Instances[index].ID != instance.ID {
					continue
				}
				current := &m.state.Instances[index]
				statusBefore := current.Status
				changed = applyObservation(current, *info, m.now())
				if changed && current.Status != statusBefore {
					m.syncPluginLocked(current)
				}
				instance = current.Clone()
				break
			}
			m.mu.Unlock()
			if changed {
				if err := m.persist(); err != nil {
					m.logger.Printf("插件状态写盘失败: %v", err)
				}
			}
			view.Status = instance.Status
			view.HealthStatus = instance.HealthStatus
			view.StatusReason = instance.StatusReason
			view.Warnings = instance.Warnings
			view.RestartCount = instance.RestartCount
			view.StartedAt = instance.StartedAt
			view.LastHealthAt = instance.LastHealthAt
			if instance.InternalAddress != "" {
				view.Address = instance.InternalAddress
			}
		} else if errors.Is(err, ErrNotFound) {
			_ = m.updateInstance(pluginID, func(current *Instance) {
				current.Status = StatusFailed
				current.HealthStatus = HealthUnknown
				current.StatusReason = "容器不存在，可尝试重新启动"
			})
			view.Status = StatusFailed
			view.StatusReason = "容器不存在，可尝试重新启动"
		}
	}
	if stats, err := m.stats(ctx, instance); err == nil {
		view.Stats = stats
	}
	return view, nil
}

func (m *Manager) stats(ctx context.Context, instance Instance) (*Stats, error) {
	if instance.RuntimeInstanceID == "" || instance.Status != StatusRunning {
		return nil, nil
	}
	m.mu.Lock()
	cached, ok := m.statsCache[instance.ID]
	m.mu.Unlock()
	if ok && time.Since(cached.at) < 2*time.Second {
		return cached.stats, nil
	}
	stats, err := m.runtime.Stats(ctx, instance.RuntimeInstanceID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.statsCache[instance.ID] = statsEntry{stats: stats, at: time.Now()}
	m.mu.Unlock()
	return stats, nil
}

// Logs returns a bounded stream owned by the caller.
func (m *Manager) Logs(ctx context.Context, pluginID string, opts LogOptions) (io.ReadCloser, error) {
	m.mu.Lock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if instance.RuntimeInstanceID == "" {
		return nil, fmt.Errorf("%w: plugin %s has no container", ErrPluginNotInstalled, pluginID)
	}
	if opts.Tail <= 0 {
		opts.Tail = 200
	}
	if opts.Tail > 2000 {
		opts.Tail = 2000
	}
	if opts.MaxBytes <= 0 || opts.MaxBytes > 512<<10 {
		opts.MaxBytes = 512 << 10
	}
	return m.runtime.Logs(ctx, instance.RuntimeInstanceID, opts)
}

func envList(variables []EnvVar) []string {
	out := make([]string, 0, len(variables))
	for _, variable := range variables {
		out = append(out, variable.Name+"="+variable.Value)
	}
	return out
}

func containerName(pluginID, instanceID string) string {
	suffix := strings.TrimPrefix(instanceID, "inst-")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return "lan-copy-plugin-" + pluginID + "-" + suffix
}
