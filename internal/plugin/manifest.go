package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	pluginIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionPattern    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	capabilityPattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)+$`)
	protocolPattern   = regexp.MustCompile(`^([0-9]+)\.([0-9]+)$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Manifest is the frozen plugin manifest carried by an image label. Unknown
// fields are rejected so an image cannot smuggle runtime options through it.
type Manifest struct {
	SchemaVersion   int               `json:"schemaVersion"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description,omitempty"`
	ProtocolVersion string            `json:"protocolVersion"`
	Runtime         string            `json:"runtime"`
	Category        string            `json:"category,omitempty"`
	ContainerPort   int               `json:"containerPort"`
	Endpoints       ManifestEndpoints `json:"endpoints"`
	Capabilities    []string          `json:"capabilities"`
	Defaults        ManifestDefaults  `json:"defaults,omitempty"`
}

type ManifestEndpoints struct {
	Health       string `json:"health"`
	Manifest     string `json:"manifest"`
	Version      string `json:"version"`
	Capabilities string `json:"capabilities"`
}

type ManifestDefaults struct {
	CPULimit              float64 `json:"cpuLimit,omitempty"`
	MemoryMB              int64   `json:"memoryMB,omitempty"`
	PIDsLimit             int64   `json:"pidsLimit,omitempty"`
	RequestTimeoutSeconds int     `json:"requestTimeoutSeconds,omitempty"`
	StartupTimeoutSeconds int     `json:"startupTimeoutSeconds,omitempty"`
}

const SupportedProtocolMajor = 1

// ParseManifest decodes and validates an image label value.
func ParseManifest(raw string) (Manifest, error) {
	var manifest Manifest
	if strings.TrimSpace(raw) == "" {
		return manifest, fmt.Errorf("%w: manifest label is empty", ErrInvalidManifest)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest, fmt.Errorf("%w: trailing data", ErrInvalidManifest)
	}
	if err := ValidateManifest(manifest); err != nil {
		return manifest, err
	}
	return NormalizeManifest(manifest), nil
}

func LoadManifestFromLabels(labels map[string]string) (Manifest, error) {
	raw, ok := labels[ManifestLabel]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: missing %s label", ErrNoPluginManifest, ManifestLabel)
	}
	return ParseManifest(raw)
}

// ValidateManifest checks every field and rejects values that would require
// host privileges or escape the fixed container security profile.
func ValidateManifest(m Manifest) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schemaVersion %d", ErrInvalidManifest, m.SchemaVersion)
	}
	if !pluginIDPattern.MatchString(m.ID) {
		return fmt.Errorf("%w: id must match %s", ErrInvalidManifest, pluginIDPattern)
	}
	if err := validateText("name", m.Name, 1, 100); err != nil {
		return err
	}
	if m.Category != "" && !pluginIDPattern.MatchString(m.Category) {
		return fmt.Errorf("%w: category must be lowercase letters, digits and hyphens", ErrInvalidManifest)
	}
	if err := validateText("description", m.Description, 0, 2000); err != nil {
		return err
	}
	if !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("%w: invalid version", ErrInvalidManifest)
	}
	if !ProtocolCompatible(m.ProtocolVersion) {
		return fmt.Errorf("%w: unsupported protocolVersion %q", ErrProtocolMismatch, m.ProtocolVersion)
	}
	if m.Runtime != "http" {
		return fmt.Errorf("%w: runtime must be http", ErrInvalidManifest)
	}
	if m.ContainerPort < 1024 || m.ContainerPort > 65535 {
		return fmt.Errorf("%w: containerPort must be 1024-65535", ErrInvalidManifest)
	}
	for name, endpoint := range map[string]string{
		"health": m.Endpoints.Health, "manifest": m.Endpoints.Manifest,
		"version": m.Endpoints.Version, "capabilities": m.Endpoints.Capabilities,
	} {
		if err := validateEndpoint(name, endpoint); err != nil {
			return err
		}
	}
	if len(m.Capabilities) > 64 {
		return fmt.Errorf("%w: too many capabilities", ErrInvalidManifest)
	}
	seen := make(map[string]struct{}, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		if !capabilityPattern.MatchString(capability) || len(capability) > 128 {
			return fmt.Errorf("%w: invalid capability %q", ErrInvalidManifest, capability)
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("%w: duplicate capability %q", ErrInvalidManifest, capability)
		}
		seen[capability] = struct{}{}
	}
	return validateManifestDefaults(m.Defaults)
}

func validateManifestDefaults(d ManifestDefaults) error {
	if d.CPULimit != 0 && (d.CPULimit < MinCPULimit || d.CPULimit > MaxCPULimit) {
		return fmt.Errorf("%w: default cpu out of range", ErrInvalidManifest)
	}
	if d.MemoryMB != 0 && (d.MemoryMB < MinMemoryMB || d.MemoryMB > MaxMemoryMB) {
		return fmt.Errorf("%w: default memory out of range", ErrInvalidManifest)
	}
	if d.PIDsLimit != 0 && (d.PIDsLimit < MinPIDs || d.PIDsLimit > MaxPIDs) {
		return fmt.Errorf("%w: default pids out of range", ErrInvalidManifest)
	}
	if d.RequestTimeoutSeconds != 0 && (d.RequestTimeoutSeconds < 1 || d.RequestTimeoutSeconds > 300) {
		return fmt.Errorf("%w: default request timeout out of range", ErrInvalidManifest)
	}
	if d.StartupTimeoutSeconds != 0 && (d.StartupTimeoutSeconds < 10 || d.StartupTimeoutSeconds > 300) {
		return fmt.Errorf("%w: default startup timeout out of range", ErrInvalidManifest)
	}
	return nil
}

func NormalizeManifest(m Manifest) Manifest {
	if m.Capabilities == nil {
		m.Capabilities = []string{}
	}
	if m.Defaults.CPULimit == 0 {
		m.Defaults.CPULimit = 0.5
	}
	if m.Defaults.MemoryMB == 0 {
		m.Defaults.MemoryMB = 512
	}
	if m.Defaults.PIDsLimit == 0 {
		m.Defaults.PIDsLimit = 128
	}
	if m.Defaults.RequestTimeoutSeconds == 0 {
		m.Defaults.RequestTimeoutSeconds = 30
	}
	if m.Defaults.StartupTimeoutSeconds == 0 {
		m.Defaults.StartupTimeoutSeconds = 60
	}
	return m
}

// ProtocolCompatible accepts protocol 1.x. A different major version requires
// an explicit manager upgrade.
func ProtocolCompatible(version string) bool {
	match := protocolPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return false
	}
	major, err := strconv.Atoi(match[1])
	return err == nil && major == SupportedProtocolMajor
}

func validateText(field, value string, min, max int) error {
	if len([]rune(value)) < min || len([]rune(value)) > max {
		return fmt.Errorf("%w: %s length out of range", ErrInvalidManifest, field)
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("%w: %s contains control characters", ErrInvalidManifest, field)
		}
	}
	return nil
}

func validateEndpoint(field, endpoint string) error {
	if endpoint == "" || len(endpoint) > 256 || endpoint[0] != '/' {
		return fmt.Errorf("%w: %s endpoint must be an absolute path", ErrInvalidManifest, field)
	}
	if strings.ContainsAny(endpoint, "?#") || strings.Contains(endpoint, "..") {
		return fmt.Errorf("%w: %s endpoint is invalid", ErrInvalidManifest, field)
	}
	for _, r := range endpoint {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%w: %s endpoint contains invalid characters", ErrInvalidManifest, field)
		}
	}
	return nil
}
