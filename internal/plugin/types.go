// Package plugin owns the plugin registry, runtime abstraction, lifecycle and
// HTTP client. It does not import Docker or the HTTP server layer.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	StateVersion = 1

	// ManifestLabel is the OCI image label that carries the plugin manifest.
	ManifestLabel = "io.lan-copy.plugin.manifest"

	// MaxImageUploadBytes is the default limit for an uploaded image archive.
	MaxImageUploadBytes int64 = 2 << 30 // 2 GiB

	MaxTaskHistory    = 100
	MaxHealthRecords  = 50
	MaxTaskMessage    = 4 << 10
	MaxTaskError      = 4 << 10
	MaxHealthError    = 2 << 10
	DefaultTaskSlots  = 8
	MaxInstallTimeout = 300 * time.Second

	DefaultRequestTimeout = 30 * time.Second
	DefaultResponseBytes  = 8 << 20
	MaxDownloadBytes      = 64 << 20
)

// Status is the persisted lifecycle state of a plugin instance.
type Status string

const (
	StatusNotInstalled Status = "not_installed"
	StatusInstalling   Status = "installing"
	StatusStopped      Status = "stopped"
	StatusStarting     Status = "starting"
	StatusRunning      Status = "running"
	StatusUnhealthy    Status = "unhealthy"
	StatusStopping     Status = "stopping"
	StatusFailed       Status = "failed"
	StatusUninstalling Status = "uninstalling"
)

func (s Status) Valid() bool {
	switch s {
	case StatusNotInstalled, StatusInstalling, StatusStopped, StatusStarting,
		StatusRunning, StatusUnhealthy, StatusStopping, StatusFailed, StatusUninstalling:
		return true
	}
	return false
}

// HealthStatus is the result of the most recent HTTP health check.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthChecking  HealthStatus = "checking"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// Plugin is a logical plugin identified by the manifest ID.
type Plugin struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Category        string    `json:"category,omitempty"`
	ProtocolVersion string    `json:"protocolVersion"`
	CurrentVersion  string    `json:"currentVersion,omitempty"`
	Status          Status    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Image is one imported image version belonging to a plugin.
type Image struct {
	ID             string    `json:"id"`
	PluginID       string    `json:"pluginId"`
	RuntimeImageID string    `json:"runtimeImageId"`
	Reference      string    `json:"reference"`
	ImageName      string    `json:"imageName"`
	ImageTag       string    `json:"imageTag,omitempty"`
	Digest         string    `json:"digest"`
	Registry       string    `json:"registry,omitempty"`
	Architecture   string    `json:"architecture"`
	OS             string    `json:"os"`
	SizeBytes      int64     `json:"sizeBytes"`
	Source         string    `json:"source"`
	ArchiveSHA256  string    `json:"archiveSha256,omitempty"`
	Manifest       Manifest  `json:"manifest"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Instance is one runtime instance of a plugin. Version 1 installs at most one
// per plugin, but the state format is a list so multiple instances stay possible.
type Instance struct {
	ID                string       `json:"id"`
	PluginID          string       `json:"pluginId"`
	ImageID           string       `json:"imageId"`
	RuntimeType       string       `json:"runtimeType"`
	RuntimeInstanceID string       `json:"runtimeInstanceId"`
	ContainerName     string       `json:"containerName"`
	InternalAddress   string       `json:"internalAddress,omitempty"`
	HostPort          int          `json:"hostPort,omitempty"`
	Status            Status       `json:"status"`
	HealthStatus      HealthStatus `json:"healthStatus"`
	StatusReason      string       `json:"statusReason,omitempty"`
	Warnings          []string     `json:"warnings,omitempty"`
	ConfigDirty       bool         `json:"configDirty,omitempty"`
	FatalError        bool         `json:"fatalError,omitempty"`
	RestartCount      int          `json:"restartCount"`
	StartedAt         *time.Time   `json:"startedAt,omitempty"`
	LastHealthAt      *time.Time   `json:"lastHealthAt,omitempty"`
	Config            Config       `json:"config"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}

// EnvVar is one environment variable. Secret values are encrypted at rest and
// masked by the HTTP API.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
	Clear  bool   `json:"clear,omitempty"` // input only: remove a secret value
}

// Mount is a bind mount below an allowed data root.
type Mount struct {
	HostPath      string `json:"hostPath"`
	ContainerPath string `json:"containerPath"`
	ReadOnly      bool   `json:"readOnly,omitempty"`
}

// HealthConfig controls the plugin HTTP health check.
type HealthConfig struct {
	Path                  string `json:"path"`
	IntervalSeconds       int    `json:"intervalSeconds"`
	TimeoutSeconds        int    `json:"timeoutSeconds"`
	Retries               int    `json:"retries"`
	StartupTimeoutSeconds int    `json:"startupTimeoutSeconds"`
}

// Config is the validated runtime configuration of one instance.
type Config struct {
	Env                   []EnvVar     `json:"env"`
	CPULimit              float64      `json:"cpuLimit"`
	MemoryMB              int64        `json:"memoryMB"`
	PIDsLimit             int64        `json:"pidsLimit"`
	RestartPolicy         string       `json:"restartPolicy"`
	Mounts                []Mount      `json:"mounts"`
	HealthCheck           HealthConfig `json:"healthCheck"`
	RequestTimeoutSeconds int          `json:"requestTimeoutSeconds"`
	LogMaxSizeMB          int          `json:"logMaxSizeMB"`
	LogMaxFiles           int          `json:"logMaxFiles"`
}

// HealthRecord is one bounded history entry.
type HealthRecord struct {
	At         time.Time `json:"at"`
	Status     string    `json:"status"`
	StatusCode int       `json:"statusCode,omitempty"`
	LatencyMS  int64     `json:"latencyMs"`
	Error      string    `json:"error,omitempty"`
}

// TaskStatus is the state of an asynchronous operation.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
)

// Task is a bounded asynchronous operation record.
type Task struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	PluginID  string          `json:"pluginId,omitempty"`
	Status    TaskStatus      `json:"status"`
	Progress  int             `json:"progress"` // -1 when indeterminate
	Message   string          `json:"message,omitempty"`
	Error     string          `json:"error,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// TaskFunc is one asynchronous manager operation.
type TaskFunc func(ctx context.Context, progress func(percent int, message string)) (any, error)

// RuntimeInfo describes the selected runtime and its environment.
type RuntimeInfo struct {
	Type         string `json:"type"`
	Available    bool   `json:"available"`
	Version      string `json:"version,omitempty"`
	APIVersion   string `json:"apiVersion,omitempty"`
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	NetworkMode  string `json:"networkMode"`
	Network      string `json:"network"`
	APIBaseURL   string `json:"apiBaseUrl,omitempty"`
	// APIBaseURLReason explains how APIBaseURL was chosen.
	APIBaseURLReason string   `json:"apiBaseUrlReason,omitempty"`
	DataRoots        []string `json:"dataRoots,omitempty"`
	Error            string   `json:"error,omitempty"`
	Orphans          []string `json:"orphans,omitempty"`
	// StartAvailable reports that this host can try to start Podman from the
	// plugin page (used when the runtime is unavailable).
	StartAvailable bool `json:"startAvailable,omitempty"`
}

// ImageInfo is the runtime-neutral result of an image import or inspection.
type ImageInfo struct {
	ID           string            `json:"id"`
	References   []string          `json:"references"`
	Digest       string            `json:"digest"`
	SizeBytes    int64             `json:"sizeBytes"`
	Architecture string            `json:"architecture"`
	OS           string            `json:"os"`
	Labels       map[string]string `json:"labels"`
	User         string            `json:"user"`
	Created      time.Time         `json:"created"`
}

// PullImageRequest is reserved for a future registry pull implementation.
type PullImageRequest struct {
	Reference string
	Username  string
	Password  string
}

// ImportImageRequest carries metadata for an image archive import.
type ImportImageRequest struct {
	Source string
}

// CreateInstanceRequest contains only validated high-level parameters. The
// runtime implementation is responsible for all security-sensitive options.
type CreateInstanceRequest struct {
	Name             string
	ImageRef         string
	Env              []string
	ContainerPort    int
	NetworkName      string
	PublishLoopback  bool
	Mounts           []Mount
	CPULimit         float64
	MemoryMB         int64
	PIDsLimit        int64
	RestartPolicy    string
	LogMaxSizeMB     int
	LogMaxFiles      int
	ExtraHosts       []string
	Labels           map[string]string
	StopTimeoutSecs  int
	ExposedPortProto string
}

// InstanceInfo is the runtime-neutral observed state of a container.
type InstanceInfo struct {
	RuntimeID    string            `json:"runtimeId"`
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Status       string            `json:"status"`
	Running      bool              `json:"running"`
	ExitCode     int               `json:"exitCode"`
	OOMKilled    bool              `json:"oomKilled"`
	RestartCount int               `json:"restartCount"`
	StartedAt    time.Time         `json:"startedAt"`
	FinishedAt   time.Time         `json:"finishedAt"`
	HostPort     int               `json:"hostPort,omitempty"`
	Address      string            `json:"address,omitempty"`
	Error        string            `json:"error,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// LogOptions bounds a log read.
type LogOptions struct {
	Tail       int
	Timestamps bool
	Since      time.Time
	MaxBytes   int64
}

// Stats is a single resource sample.
type Stats struct {
	CPUPercent       float64   `json:"cpuPercent"`
	MemoryBytes      int64     `json:"memoryBytes"`
	MemoryLimitBytes int64     `json:"memoryLimitBytes"`
	PIDs             int64     `json:"pids"`
	SampledAt        time.Time `json:"sampledAt"`
}

// UsageItem is one running plugin's resource sample.
type UsageItem struct {
	PluginID         string  `json:"pluginId"`
	Name             string  `json:"name"`
	CPUPercent       float64 `json:"cpuPercent"`
	MemoryBytes      int64   `json:"memoryBytes"`
	MemoryLimitBytes int64   `json:"memoryLimitBytes"`
	PIDs             int64   `json:"pids"`
	Available        bool    `json:"available"`
	Error            string  `json:"error,omitempty"`
}

// Usage aggregates resource samples for running plugin containers.
type Usage struct {
	Running          int         `json:"running"`
	Unavailable      int         `json:"unavailable"`
	CPUPercent       float64     `json:"cpuPercent"`
	MemoryBytes      int64       `json:"memoryBytes"`
	MemoryLimitBytes int64       `json:"memoryLimitBytes"`
	PIDs             int64       `json:"pids"`
	Items            []UsageItem `json:"items"`
	SampledAt        time.Time   `json:"sampledAt"`
}

// CallMetrics are in-memory per-plugin HTTP client counters.
type CallMetrics struct {
	Calls           uint64     `json:"calls"`
	Errors          uint64     `json:"errors"`
	TotalDurationMS int64      `json:"totalDurationMs"`
	LastDurationMS  int64      `json:"lastDurationMs"`
	LastErrorAt     *time.Time `json:"lastErrorAt,omitempty"`
}

// State is the persisted registry snapshot. It is stored as one atomic file so
// plugin, image, instance, task and health updates cannot be half-applied.
type State struct {
	Version       int                       `json:"version"`
	Plugins       []Plugin                  `json:"plugins"`
	Images        []Image                   `json:"images"`
	Instances     []Instance                `json:"instances"`
	Tasks         []Task                    `json:"tasks"`
	HealthRecords map[string][]HealthRecord `json:"healthRecords"`
}

func NewState() State {
	return State{Version: StateVersion, HealthRecords: map[string][]HealthRecord{}}
}

// Clone returns a deep copy suitable for writing outside the manager lock.
func (s State) Clone() State {
	out := State{
		Version:       s.Version,
		Plugins:       append([]Plugin(nil), s.Plugins...),
		Images:        append([]Image(nil), s.Images...),
		Instances:     make([]Instance, len(s.Instances)),
		Tasks:         append([]Task(nil), s.Tasks...),
		HealthRecords: make(map[string][]HealthRecord, len(s.HealthRecords)),
	}
	for i, instance := range s.Instances {
		out.Instances[i] = instance.Clone()
	}
	for key, records := range s.HealthRecords {
		out.HealthRecords[key] = append([]HealthRecord(nil), records...)
	}
	return out
}

func (i Instance) Clone() Instance {
	out := i
	out.Config = i.Config.Clone()
	out.Warnings = append([]string(nil), i.Warnings...)
	return out
}

func (c Config) Clone() Config {
	out := c
	if c.Env != nil {
		out.Env = append([]EnvVar{}, c.Env...)
	}
	if c.Mounts != nil {
		out.Mounts = append([]Mount{}, c.Mounts...)
	}
	return out
}

// MaskConfig removes secret values for API and task output.
func MaskConfig(config Config) Config {
	out := config.Clone()
	for index := range out.Env {
		if out.Env[index].Secret {
			out.Env[index].Value = ""
		}
	}
	return out
}

// MaskInstance removes secret values from an instance before it is returned
// by the HTTP layer.
func MaskInstance(instance Instance) Instance {
	out := instance.Clone()
	out.Config = MaskConfig(out.Config)
	return out
}

// Views returned by the manager and encoded by the HTTP layer.

type PluginView struct {
	Plugin   Plugin      `json:"plugin"`
	Image    *Image      `json:"image,omitempty"`
	Instance *Instance   `json:"instance,omitempty"`
	Metrics  CallMetrics `json:"metrics"`
}

type DetailView struct {
	Plugin        Plugin         `json:"plugin"`
	Images        []Image        `json:"images"`
	Instance      *Instance      `json:"instance,omitempty"`
	Manifest      Manifest       `json:"manifest"`
	HealthRecords []HealthRecord `json:"healthRecords"`
	Metrics       CallMetrics    `json:"metrics"`
	Runtime       RuntimeInfo    `json:"runtime"`
}

// ProxyTarget is the resolved upstream for one plugin's browser interface.
// It contains no secrets and is only valid while the instance stays running.
type ProxyTarget struct {
	BaseURL string // http://127.0.0.1:<port> or http://<container>:<port>
	UIPath  string // manifest ui.path, normalized to an absolute path
}

type InstallRequest struct {
	ImageID          string `json:"imageId"`
	ExpectedPluginID string `json:"expectedPluginId,omitempty"`
	Config           Config `json:"config"`
	Start            bool   `json:"start"`
}

type InstallResult struct {
	Plugin   Plugin   `json:"plugin"`
	Instance Instance `json:"instance"`
}

type UninstallRequest struct {
	DeleteImages        bool `json:"deleteImages"`
	DeleteData          bool `json:"deleteData"`
	ConfirmDataDeletion bool `json:"confirmDataDeletion,omitempty"`
}

type StatusView struct {
	PluginID      string         `json:"pluginId"`
	InstanceID    string         `json:"instanceId,omitempty"`
	Status        Status         `json:"status"`
	HealthStatus  HealthStatus   `json:"healthStatus"`
	StatusReason  string         `json:"statusReason,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
	Address       string         `json:"address,omitempty"`
	StartedAt     *time.Time     `json:"startedAt,omitempty"`
	LastHealthAt  *time.Time     `json:"lastHealthAt,omitempty"`
	RestartCount  int            `json:"restartCount"`
	HealthRecords []HealthRecord `json:"healthRecords"`
	Stats         *Stats         `json:"stats,omitempty"`
	Metrics       CallMetrics    `json:"metrics"`
}

// Request and response types for the unified plugin HTTP client.

type Request struct {
	Method    string
	Path      string
	Query     map[string][]string
	Header    map[string][]string
	JSON      any
	Multipart *MultipartRequest
	Body      io.Reader
	Timeout   time.Duration
	MaxBytes  int64
}

type MultipartRequest struct {
	Fields []MultipartField
	Files  []MultipartFile
}

type MultipartField struct {
	Name  string
	Value string
}

type MultipartFile struct {
	Field    string
	Filename string
	Reader   io.Reader
	Size     int64
}

type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// Sentinel errors. Callers use errors.Is; details remain wrapped.
var (
	ErrNotFound               = errors.New("not found")
	ErrConflict               = errors.New("conflict")
	ErrBusy                   = errors.New("operation already in progress")
	ErrRuntimeUnavailable     = errors.New("plugin runtime unavailable")
	ErrUnsupported            = errors.New("unsupported operation")
	ErrInvalidManifest        = errors.New("invalid plugin manifest")
	ErrProtocolMismatch       = errors.New("plugin protocol mismatch")
	ErrAlreadyInstalled       = errors.New("plugin already installed")
	ErrImageInUse             = errors.New("image is in use")
	ErrInvalidConfig          = errors.New("invalid plugin configuration")
	ErrTooLarge               = errors.New("upload exceeds limit")
	ErrUnsupportedArchive     = errors.New("unsupported image archive")
	ErrNoPluginManifest       = errors.New("image has no valid plugin manifest")
	ErrPluginNotInstalled     = errors.New("plugin not installed")
	ErrPluginNotRunning       = errors.New("plugin not running")
	ErrPluginUnhealthy        = errors.New("plugin unhealthy")
	ErrPluginNoUI             = errors.New("plugin has no web interface")
	ErrPluginTimeout          = errors.New("plugin request timed out")
	ErrPluginUnavailable      = errors.New("plugin unavailable")
	ErrPluginProtocolMismatch = errors.New("plugin protocol mismatch")
	ErrPluginRequestFailed    = errors.New("plugin request failed")
	ErrPluginResponseTooLarge = errors.New("plugin response too large")
	ErrSecretKeyUnavailable   = errors.New("plugin secret key unavailable")
)
