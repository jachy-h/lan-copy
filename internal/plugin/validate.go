package plugin

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MinCPULimit = 0.1
	MaxCPULimit = 8.0
	MinMemoryMB = 64
	MaxMemoryMB = 16384
	MinPIDs     = 16
	MaxPIDs     = 4096

	MinLogSizeMB = 1
	MaxLogSizeMB = 50
	MinLogFiles  = 1
	MaxLogFiles  = 5
	MaxMounts    = 8
	MaxEnvVars   = 128
	MaxEnvValue  = 4096
)

var rejectedEnvNames = map[string]struct{}{
	"DOCKER_HOST":     {},
	"LD_PRELOAD":      {},
	"LD_LIBRARY_PATH": {},
	EventTokenEnv:     {},
	APIBaseURLEnv:     {},
	APITokenEnv:       {},
}

var rejectedContainerPaths = []string{
	"/", "/proc", "/sys", "/dev", "/boot", "/etc", "/usr", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/run", "/var/run", "/var/lib/docker",
}

// DefaultConfig derives safe defaults from a normalized manifest.
func DefaultConfig(m Manifest) Config {
	m = NormalizeManifest(m)
	return Config{
		Env:           []EnvVar{},
		CPULimit:      m.Defaults.CPULimit,
		MemoryMB:      m.Defaults.MemoryMB,
		PIDsLimit:     m.Defaults.PIDsLimit,
		RestartPolicy: "no",
		Mounts:        []Mount{},
		HealthCheck: HealthConfig{
			Path:                  m.Endpoints.Health,
			IntervalSeconds:       15,
			TimeoutSeconds:        3,
			Retries:               3,
			StartupTimeoutSeconds: m.Defaults.StartupTimeoutSeconds,
		},
		RequestTimeoutSeconds: m.Defaults.RequestTimeoutSeconds,
		LogMaxSizeMB:          10,
		LogMaxFiles:           2,
	}
}

// MergeConfig overlays user-provided values on defaults. Zero values in the
// request keep the manifest defaults; env secret values without a replacement
// keep the previous secret.
func MergeConfig(base Config, requested Config) Config {
	out := base
	if requested.CPULimit != 0 {
		out.CPULimit = requested.CPULimit
	}
	if requested.MemoryMB != 0 {
		out.MemoryMB = requested.MemoryMB
	}
	if requested.PIDsLimit != 0 {
		out.PIDsLimit = requested.PIDsLimit
	}
	if requested.RestartPolicy != "" {
		out.RestartPolicy = requested.RestartPolicy
	}
	if requested.RequestTimeoutSeconds != 0 {
		out.RequestTimeoutSeconds = requested.RequestTimeoutSeconds
	}
	if requested.LogMaxSizeMB != 0 {
		out.LogMaxSizeMB = requested.LogMaxSizeMB
	}
	if requested.LogMaxFiles != 0 {
		out.LogMaxFiles = requested.LogMaxFiles
	}
	if requested.HealthCheck.Path != "" {
		out.HealthCheck = requested.HealthCheck
	}
	if requested.Env != nil {
		out.Env = mergeEnv(base.Env, requested.Env)
	}
	if requested.Mounts != nil {
		out.Mounts = append([]Mount(nil), requested.Mounts...)
	}
	return out
}

func mergeEnv(previous, requested []EnvVar) []EnvVar {
	old := make(map[string]EnvVar, len(previous))
	for _, variable := range previous {
		old[variable.Name] = variable
	}
	out := make([]EnvVar, 0, len(requested))
	for _, variable := range requested {
		if variable.Secret && variable.Value == "" && !variable.Clear {
			if existing, ok := old[variable.Name]; ok {
				variable.Value = existing.Value
			}
		}
		variable.Clear = false
		out = append(out, variable)
	}
	return out
}

// ValidateConfig checks shape and ranges. Mount host paths are validated
// separately because they depend on the configured allowed roots.
func ValidateConfig(config Config, manifest Manifest) error {
	if config.CPULimit < MinCPULimit || config.CPULimit > MaxCPULimit {
		return fmt.Errorf("%w: cpuLimit must be %.1f-%.1f", ErrInvalidConfig, MinCPULimit, MaxCPULimit)
	}
	if config.MemoryMB < MinMemoryMB || config.MemoryMB > MaxMemoryMB {
		return fmt.Errorf("%w: memoryMB must be %d-%d", ErrInvalidConfig, MinMemoryMB, MaxMemoryMB)
	}
	if config.PIDsLimit < MinPIDs || config.PIDsLimit > MaxPIDs {
		return fmt.Errorf("%w: pidsLimit must be %d-%d", ErrInvalidConfig, MinPIDs, MaxPIDs)
	}
	if !validRestartPolicy(config.RestartPolicy) {
		return fmt.Errorf("%w: restartPolicy must be no or on-failure[:N]", ErrInvalidConfig)
	}
	if len(config.Env) > MaxEnvVars {
		return fmt.Errorf("%w: too many environment variables", ErrInvalidConfig)
	}
	seen := make(map[string]struct{}, len(config.Env))
	for _, variable := range config.Env {
		if !envNamePattern.MatchString(variable.Name) {
			return fmt.Errorf("%w: invalid environment variable name %q", ErrInvalidConfig, variable.Name)
		}
		if _, rejected := rejectedEnvNames[variable.Name]; rejected {
			return fmt.Errorf("%w: environment variable %s is reserved", ErrInvalidConfig, variable.Name)
		}
		if _, exists := seen[variable.Name]; exists {
			return fmt.Errorf("%w: duplicate environment variable %s", ErrInvalidConfig, variable.Name)
		}
		seen[variable.Name] = struct{}{}
		if len(variable.Value) > MaxEnvValue || strings.ContainsRune(variable.Value, 0) {
			return fmt.Errorf("%w: environment variable %s value is too large or invalid", ErrInvalidConfig, variable.Name)
		}
	}
	if len(config.Mounts) > MaxMounts {
		return fmt.Errorf("%w: too many mounts", ErrInvalidConfig)
	}
	seenMounts := make(map[string]struct{}, len(config.Mounts))
	for _, mount := range config.Mounts {
		if err := ValidateContainerPath(mount.ContainerPath); err != nil {
			return err
		}
		if _, exists := seenMounts[mount.ContainerPath]; exists {
			return fmt.Errorf("%w: duplicate container path %s", ErrInvalidConfig, mount.ContainerPath)
		}
		seenMounts[mount.ContainerPath] = struct{}{}
		if mount.HostPath == "" {
			return fmt.Errorf("%w: host path is required", ErrInvalidConfig)
		}
	}
	health := config.HealthCheck
	if health.Path == "" || health.Path[0] != '/' || strings.ContainsAny(health.Path, "?#") || strings.Contains(health.Path, "..") {
		return fmt.Errorf("%w: invalid health path", ErrInvalidConfig)
	}
	if health.IntervalSeconds < 5 || health.IntervalSeconds > 3600 {
		return fmt.Errorf("%w: health interval must be 5-3600 seconds", ErrInvalidConfig)
	}
	if health.TimeoutSeconds < 1 || health.TimeoutSeconds > 60 {
		return fmt.Errorf("%w: health timeout must be 1-60 seconds", ErrInvalidConfig)
	}
	if health.Retries < 1 || health.Retries > 10 {
		return fmt.Errorf("%w: health retries must be 1-10", ErrInvalidConfig)
	}
	if health.StartupTimeoutSeconds < 10 || health.StartupTimeoutSeconds > 300 {
		return fmt.Errorf("%w: startup timeout must be 10-300 seconds", ErrInvalidConfig)
	}
	if config.RequestTimeoutSeconds < 1 || config.RequestTimeoutSeconds > 300 {
		return fmt.Errorf("%w: request timeout must be 1-300 seconds", ErrInvalidConfig)
	}
	if config.LogMaxSizeMB < MinLogSizeMB || config.LogMaxSizeMB > MaxLogSizeMB {
		return fmt.Errorf("%w: log size must be %d-%d MB", ErrInvalidConfig, MinLogSizeMB, MaxLogSizeMB)
	}
	if config.LogMaxFiles < MinLogFiles || config.LogMaxFiles > MaxLogFiles {
		return fmt.Errorf("%w: log files must be %d-%d", ErrInvalidConfig, MinLogFiles, MaxLogFiles)
	}
	return nil
}

func validRestartPolicy(policy string) bool {
	if policy == "no" || policy == "on-failure" {
		return true
	}
	if strings.HasPrefix(policy, "on-failure:") {
		var count int
		if _, err := fmt.Sscanf(policy, "on-failure:%d", &count); err == nil && count >= 1 && count <= 10 {
			return true
		}
	}
	return false
}

// ValidateContainerPath rejects system paths, traversal and duplicate roots.
func ValidateContainerPath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%w: container path must be absolute", ErrInvalidConfig)
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned != path || strings.Contains(cleaned, "..") {
		return fmt.Errorf("%w: container path must be clean", ErrInvalidConfig)
	}
	if strings.HasPrefix(cleaned, "/tmp") {
		return fmt.Errorf("%w: /tmp is reserved for the container tmpfs", ErrInvalidConfig)
	}
	for _, rejected := range rejectedContainerPaths {
		if cleaned == rejected || strings.HasPrefix(cleaned, rejected+"/") {
			return fmt.Errorf("%w: container path %s is reserved", ErrInvalidConfig, cleaned)
		}
	}
	return nil
}

// isRuntimeSocket rejects Docker and Podman control sockets alike: mounting
// either one hands the container control over the host runtime.
func isRuntimeSocket(path string) bool {
	return strings.HasSuffix(path, "docker.sock") || strings.HasSuffix(path, "podman.sock")
}

// PrepareHostMount creates the directory if needed, resolves symlinks and
// verifies that the final path is a strict child of an allowed root.
func PrepareHostMount(hostPath string, allowedRoots []string) (string, error) {
	if hostPath == "" || strings.ContainsRune(hostPath, 0) || !filepath.IsAbs(hostPath) {
		return "", fmt.Errorf("%w: host path must be absolute", ErrInvalidConfig)
	}
	cleaned := filepath.Clean(hostPath)
	if isRuntimeSocket(cleaned) {
		return "", fmt.Errorf("%w: container runtime socket cannot be mounted", ErrInvalidConfig)
	}
	if err := os.MkdirAll(cleaned, 0o750); err != nil {
		return "", fmt.Errorf("%w: cannot create plugin data directory: %v", ErrInvalidConfig, err)
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve host path: %v", ErrInvalidConfig, err)
	}
	for _, root := range allowedRoots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return resolved, nil
	}
	return "", fmt.Errorf("%w: host path is outside the allowed plugin data roots", ErrInvalidConfig)
}

// ValidateImageFilename accepts only .tar, .tar.gz and .tgz names.
func ValidateImageFilename(name string) error {
	lower := strings.ToLower(filepath.Base(name))
	if strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return nil
	}
	return fmt.Errorf("%w: only .tar, .tar.gz and .tgz are accepted", ErrUnsupportedArchive)
}

// DetectImageArchive sniffs gzip or tar magic without consuming data.
func DetectImageArchive(reader *bufio.Reader) error {
	header, err := reader.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return fmt.Errorf("%w: cannot read archive header", ErrUnsupportedArchive)
	}
	if len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b {
		return nil
	}
	if len(header) >= 262 && string(header[257:262]) == "ustar" {
		return nil
	}
	return fmt.Errorf("%w: file is not a tar or gzip archive", ErrUnsupportedArchive)
}
