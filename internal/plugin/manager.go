package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	HealthInterval  = 15 * time.Second
	ReconcilePeriod = 30 * time.Second
)

type Options struct {
	Runtime          RuntimeSettings
	AllowedDataRoots []string
	StopOnExit       bool
	MaxImageBytes    int64
	Logger           *log.Logger
	Now              func() time.Time
}

// Manager owns the persisted registry and coordinates the runtime. Mutable
// state and its lock live here; runtime calls are always made outside the lock.
type Manager struct {
	store         *Store
	runtime       Runtime
	settings      RuntimeSettings
	roots         []string
	stopExit      bool
	maxImageBytes int64
	logger        *log.Logger
	now           func() time.Time

	mu               sync.Mutex
	state            State
	locks            map[string]*sync.Mutex
	metrics          map[string]*CallMetrics
	orphans          []string
	taskSlots        chan struct{}
	tasks            sync.WaitGroup
	httpClient       *http.Client
	lastRuntimeError string
	statsCache       map[string]statsEntry

	eventQueues      map[string]chan Event
	eventRecords     map[string][]EventDelivery
	eventStats       map[string]*EventStats
	eventCircuit     map[string]eventCircuitState
	eventLastDropLog time.Time
	eventWG          sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
	runWG  sync.WaitGroup
}

func NewManager(store *Store, runtime Runtime, opts Options) (*Manager, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("plugin manager requires store and runtime")
	}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	changed := RecoverTasks(&state)
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.Now == nil {
		opts.Now = nowUTC
	}
	roots := append([]string(nil), opts.AllowedDataRoots...)
	if len(roots) == 0 {
		roots = []string{store.DataDir()}
	}
	maxImageBytes := opts.MaxImageBytes
	if maxImageBytes <= 0 {
		maxImageBytes = MaxImageUploadBytes
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		store:         store,
		runtime:       runtime,
		settings:      opts.Runtime.Normalized(),
		roots:         roots,
		stopExit:      opts.StopOnExit,
		logger:        opts.Logger,
		now:           opts.Now,
		maxImageBytes: maxImageBytes,
		state:         state,
		locks:         make(map[string]*sync.Mutex),
		metrics:       make(map[string]*CallMetrics),
		taskSlots:     make(chan struct{}, DefaultTaskSlots),
		httpClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		statsCache:   make(map[string]statsEntry),
		eventQueues:  make(map[string]chan Event),
		eventRecords: make(map[string][]EventDelivery),
		eventStats:   make(map[string]*EventStats),
		eventCircuit: make(map[string]eventCircuitState),
		ctx:          ctx,
		cancel:       cancel,
	}
	if changed {
		if err := manager.persist(); err != nil {
			manager.logger.Printf("插件任务恢复写盘失败: %v", err)
		}
	}
	return manager, nil
}

func (m *Manager) Store() *Store { return m.store }

// Run performs one reconciliation and then keeps health and drift checks going
// until Shutdown. It never blocks HTTP startup.
func (m *Manager) Run() {
	defer m.runWG.Done()
	if m.ctx.Err() != nil {
		return
	}
	m.reconcile(m.ctx)
	healthTicker := time.NewTicker(HealthInterval)
	defer healthTicker.Stop()
	reconcileTicker := time.NewTicker(ReconcilePeriod)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-healthTicker.C:
			m.healthLoop(m.ctx)
		case <-reconcileTicker.C:
			m.reconcile(m.ctx)
		}
	}
}

func (m *Manager) StartBackground() {
	m.runWG.Add(1)
	go m.Run()
}

// Shutdown cancels background work and optionally stops managed containers.
func (m *Manager) Shutdown(ctx context.Context) {
	m.cancel()
	m.eventWG.Wait()
	done := make(chan struct{})
	go func() {
		m.tasks.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		m.logger.Printf("插件任务未在关闭期限内结束")
	}
	if m.stopExit {
		m.stopAll(ctx)
	}
	m.runWG.Wait()
	if err := m.persist(); err != nil {
		m.logger.Printf("插件状态写盘失败: %v", err)
	}
}

func (m *Manager) stopAll(ctx context.Context) {
	instances := m.instances()
	for _, instance := range instances {
		if instance.RuntimeInstanceID == "" {
			continue
		}
		if instance.Status != StatusRunning && instance.Status != StatusUnhealthy && instance.Status != StatusStarting {
			continue
		}
		timeout := 10 * time.Second
		if err := m.runtime.Stop(ctx, instance.RuntimeInstanceID, timeout); err != nil {
			m.logger.Printf("退出时停止插件 %s 失败: %v", instance.PluginID, err)
			continue
		}
		_ = m.updateInstance(instance.PluginID, func(current *Instance) {
			current.Status = StatusStopped
			current.HealthStatus = HealthUnknown
			current.StatusReason = "服务退出时停止"
		})
	}
}

func (m *Manager) RuntimeInfo(ctx context.Context) RuntimeInfo {
	info, err := m.runtime.Info(ctx)
	settings := m.settings
	info.NetworkMode = settings.NetworkMode
	info.Network = settings.Network
	info.DataRoots = append([]string(nil), m.roots...)
	if err != nil {
		info.Type = settings.Type
		info.Available = false
		info.Error = friendlyRuntimeError(err)
		return info
	}
	m.mu.Lock()
	info.Orphans = append([]string(nil), m.orphans...)
	m.mu.Unlock()
	return info
}

func (m *Manager) Plugins() []PluginView {
	m.mu.Lock()
	defer m.mu.Unlock()
	views := make([]PluginView, 0, len(m.state.Plugins))
	for _, pluginEntry := range m.state.Plugins {
		view := PluginView{Plugin: pluginEntry, Metrics: m.metricsLocked(pluginEntry.ID)}
		if instance, ok := instanceForPlugin(m.state.Instances, pluginEntry.ID); ok {
			copy := MaskInstance(instance)
			view.Instance = &copy
			if image, ok := imageByID(m.state.Images, instance.ImageID); ok {
				copyImage := image
				view.Image = &copyImage
			}
		} else if image, ok := latestImageForPlugin(m.state.Images, pluginEntry.ID); ok {
			view.Image = &image
		}
		views = append(views, view)
	}
	return views
}

func (m *Manager) Detail(ctx context.Context, pluginID string) (DetailView, error) {
	m.mu.Lock()
	pluginEntry, ok := m.pluginLocked(pluginID)
	if !ok {
		m.mu.Unlock()
		return DetailView{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	instance, hasInstance := instanceForPlugin(m.state.Instances, pluginID)
	var instanceCopy *Instance
	if hasInstance {
		copy := MaskInstance(instance)
		instanceCopy = &copy
	}
	images := imagesForPlugin(m.state.Images, pluginID)
	recordsKey := ""
	if instanceCopy != nil {
		recordsKey = instanceCopy.ID
	}
	records := append([]HealthRecord{}, m.state.HealthRecords[recordsKey]...)
	metrics := m.metricsLocked(pluginID)
	manifest := Manifest{}
	if instanceCopy != nil {
		if image, found := imageByID(m.state.Images, instanceCopy.ImageID); found {
			manifest = image.Manifest
		}
	}
	if manifest.ID == "" && len(images) > 0 {
		manifest = images[0].Manifest
	}
	m.mu.Unlock()
	runtimeInfo := m.RuntimeInfo(ctx)
	return DetailView{
		Plugin: pluginEntry, Images: images, Instance: instanceCopy,
		Manifest: manifest, HealthRecords: records, Metrics: metrics,
		Runtime: runtimeInfo,
	}, nil
}

func (m *Manager) Manifest(pluginID string) (Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if ok {
		if image, found := imageByID(m.state.Images, instance.ImageID); found {
			return image.Manifest, nil
		}
	}
	if image, ok := latestImageForPlugin(m.state.Images, pluginID); ok {
		return image.Manifest, nil
	}
	return Manifest{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
}

func (m *Manager) Capabilities(pluginID string) ([]string, error) {
	manifest, err := m.Manifest(pluginID)
	if err != nil {
		return nil, err
	}
	return append([]string{}, manifest.Capabilities...), nil
}

func (m *Manager) Config(pluginID string) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		return Config{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	return MaskConfig(instance.Config), nil
}

func (m *Manager) UpdateConfig(pluginID string, requested Config) (Config, error) {
	unlock, err := m.lockInstance(pluginID)
	if err != nil {
		return Config{}, err
	}
	defer unlock()
	m.mu.Lock()
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		m.mu.Unlock()
		return Config{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	if instance.Status == StatusRunning || instance.Status == StatusUnhealthy || instance.Status == StatusStarting || instance.Status == StatusStopping {
		m.mu.Unlock()
		return Config{}, fmt.Errorf("%w: stop the plugin before changing its configuration", ErrConflict)
	}
	image, ok := imageByID(m.state.Images, instance.ImageID)
	if !ok {
		m.mu.Unlock()
		return Config{}, fmt.Errorf("%w: plugin image record is missing", ErrConflict)
	}
	merged := MergeConfig(instance.Config, requested)
	m.mu.Unlock()
	if err := m.validateConfig(&merged, image.Manifest); err != nil {
		return Config{}, err
	}
	if err := m.updateInstance(pluginID, func(current *Instance) {
		current.Config = merged
		current.ConfigDirty = true
		current.StatusReason = "配置已更新，下次启动生效"
	}); err != nil {
		return Config{}, err
	}
	return MaskConfig(merged), nil
}

func (m *Manager) validateConfig(config *Config, manifest Manifest) error {
	if err := ValidateConfig(*config, manifest); err != nil {
		return err
	}
	for index, mount := range config.Mounts {
		hostPath, err := PrepareHostMount(mount.HostPath, m.roots)
		if err != nil {
			return err
		}
		config.Mounts[index].HostPath = hostPath
	}
	return nil
}

func (m *Manager) instances() []Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Instance(nil), m.state.Instances...)
}

func (m *Manager) Tasks(limit int) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > MaxTaskHistory {
		limit = 50
	}
	start := len(m.state.Tasks) - limit
	if start < 0 {
		start = 0
	}
	tasks := append([]Task{}, m.state.Tasks[start:]...)
	// Newest first for the UI.
	for left, right := 0, len(tasks)-1; left < right; left, right = left+1, right-1 {
		tasks[left], tasks[right] = tasks[right], tasks[left]
	}
	return tasks
}

func (m *Manager) Task(id string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range m.state.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return Task{}, fmt.Errorf("%w: task %s", ErrNotFound, id)
}

// RunTask reserves a bounded slot and executes fn in the background.
func (m *Manager) RunTask(kind, pluginID string, fn TaskFunc) (Task, error) {
	select {
	case m.taskSlots <- struct{}{}:
	default:
		return Task{}, fmt.Errorf("%w: too many plugin tasks", ErrBusy)
	}
	id, err := randomID("task-")
	if err != nil {
		<-m.taskSlots
		return Task{}, err
	}
	now := m.now()
	task := Task{
		ID: id, Kind: kind, PluginID: pluginID, Status: TaskPending,
		Progress: -1, CreatedAt: now, UpdatedAt: now,
	}
	m.mu.Lock()
	m.state.Tasks = append(m.state.Tasks, task)
	if len(m.state.Tasks) > MaxTaskHistory {
		m.state.Tasks = append([]Task(nil), m.state.Tasks[len(m.state.Tasks)-MaxTaskHistory:]...)
	}
	m.mu.Unlock()
	if err := m.persist(); err != nil {
		<-m.taskSlots
		return Task{}, err
	}
	m.tasks.Add(1)
	go func() {
		defer m.tasks.Done()
		defer func() { <-m.taskSlots }()
		m.setTask(id, func(current *Task) {
			current.Status = TaskRunning
			current.Message = "正在执行"
		}, true)
		result, runErr := fn(m.ctx, func(percent int, message string) {
			m.setTask(id, func(current *Task) {
				if current.Status == TaskRunning {
					current.Progress = percent
					if message != "" {
						current.Message = truncate(message, MaxTaskMessage)
					}
				}
			}, false)
		})
		m.setTask(id, func(current *Task) {
			current.Progress = 100
			if runErr != nil {
				current.Status = TaskFailed
				current.Error = truncate(runErr.Error(), MaxTaskError)
				current.Message = "失败"
				return
			}
			current.Status = TaskSucceeded
			current.Message = "完成"
			if result != nil {
				if data, err := json.Marshal(result); err == nil && len(data) <= 64<<10 {
					current.Result = data
				}
			}
		}, true)
	}()
	return task, nil
}

func (m *Manager) setTask(id string, mutate func(*Task), persist bool) {
	m.mu.Lock()
	for index := range m.state.Tasks {
		if m.state.Tasks[index].ID != id {
			continue
		}
		mutate(&m.state.Tasks[index])
		m.state.Tasks[index].UpdatedAt = m.now()
		break
	}
	m.mu.Unlock()
	if persist {
		if err := m.persist(); err != nil {
			m.logger.Printf("插件任务写盘失败: %v", err)
		}
	}
}

func (m *Manager) lockInstance(pluginID string) (func(), error) {
	m.mu.Lock()
	lock, ok := m.locks[pluginID]
	if !ok {
		lock = &sync.Mutex{}
		m.locks[pluginID] = lock
	}
	m.mu.Unlock()
	if !lock.TryLock() {
		return nil, fmt.Errorf("%w: plugin %s", ErrBusy, pluginID)
	}
	return lock.Unlock, nil
}

func (m *Manager) persist() error {
	m.mu.Lock()
	state := m.state.Clone()
	m.mu.Unlock()
	return m.store.Save(state)
}

func (m *Manager) updateInstance(pluginID string, mutate func(*Instance)) error {
	m.mu.Lock()
	now := m.now()
	for index := range m.state.Instances {
		if m.state.Instances[index].PluginID != pluginID {
			continue
		}
		mutate(&m.state.Instances[index])
		m.state.Instances[index].UpdatedAt = now
		status := m.state.Instances[index].Status
		if pluginIndex := pluginIndex(m.state.Plugins, pluginID); pluginIndex >= 0 {
			m.state.Plugins[pluginIndex].Status = status
			m.state.Plugins[pluginIndex].UpdatedAt = now
		}
		break
	}
	m.mu.Unlock()
	return m.persist()
}

func (m *Manager) pluginLocked(id string) (Plugin, bool) {
	for _, pluginEntry := range m.state.Plugins {
		if pluginEntry.ID == id {
			return pluginEntry, true
		}
	}
	return Plugin{}, false
}

func (m *Manager) metricsLocked(pluginID string) CallMetrics {
	if metrics, ok := m.metrics[pluginID]; ok {
		return *metrics
	}
	return CallMetrics{}
}

func (m *Manager) addMetric(pluginID string, duration time.Duration, failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metrics, ok := m.metrics[pluginID]
	if !ok {
		metrics = &CallMetrics{}
		m.metrics[pluginID] = metrics
	}
	metrics.Calls++
	metrics.LastDurationMS = duration.Milliseconds()
	metrics.TotalDurationMS += duration.Milliseconds()
	if failed {
		metrics.Errors++
		now := m.now()
		metrics.LastErrorAt = &now
	}
}

func instanceForPlugin(instances []Instance, pluginID string) (Instance, bool) {
	for _, instance := range instances {
		if instance.PluginID == pluginID {
			return instance, true
		}
	}
	return Instance{}, false
}

func imageByID(images []Image, id string) (Image, bool) {
	for _, image := range images {
		if image.ID == id {
			return image, true
		}
	}
	return Image{}, false
}

func latestImageForPlugin(images []Image, pluginID string) (Image, bool) {
	var latest Image
	found := false
	for _, image := range images {
		if image.PluginID != pluginID {
			continue
		}
		if !found || image.CreatedAt.After(latest.CreatedAt) {
			latest = image
			found = true
		}
	}
	return latest, found
}

func imagesForPlugin(images []Image, pluginID string) []Image {
	out := make([]Image, 0)
	for _, image := range images {
		if image.PluginID == pluginID {
			out = append(out, image)
		}
	}
	return out
}

func pluginIndex(plugins []Plugin, id string) int {
	for index := range plugins {
		if plugins[index].ID == id {
			return index
		}
	}
	return -1
}

func friendlyRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrRuntimeUnavailable) {
		return "无法连接 Docker Engine，请确认 Docker 已安装并正在运行"
	}
	return err.Error()
}
