package dockerruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

const (
	detectTotalTimeout = 4 * time.Second
	detectProbeTimeout = 1500 * time.Millisecond

	podmanRetryWindow   = 2 * time.Second
	podmanRetryInterval = 300 * time.Millisecond
)

// DisablePodmanAutostartEnv turns off automatic podman.socket activation.
const DisablePodmanAutostartEnv = "LAN_COPY_DISABLE_PODMAN_AUTOSTART"

// The platform layer installs the runtime starters in init. The remaining
// function variables are seams for tests.
var (
	podmanSocketActivator  func(context.Context) error
	platformRuntimeStart   func(context.Context, func(int, string)) error
	platformStartAvailable = func() bool { return false }
	autoCandidates         = defaultCandidates
	podmanProbeCandidates  = podmanCandidates
	commandExists          = locateCommand
)

// Detected describes the container runtime that answered the probe. Type is
// either "docker" or "podman"; both speak the Docker Engine API.
type Detected struct {
	Type       string
	Host       string
	Version    string
	APIVersion string
	OS         string
	Arch       string
	Source     string
}

// DetectError explains why no runtime could be reached. Its message is shown
// to the user, so it stays short and actionable; Details keeps the raw probe
// errors for logs.
type DetectError struct {
	// EnvSource is DOCKER_HOST or CONTAINER_HOST when the user pinned an
	// endpoint; empty when automatic discovery was used.
	EnvSource      string
	Tried          []string
	Problems       []string
	PodmanFound    bool
	DockerFound    bool
	AutostartTried bool
}

func (e *DetectError) Error() string {
	var builder strings.Builder
	if e.EnvSource != "" {
		builder.WriteString(e.EnvSource)
		builder.WriteString(" 指定的容器运行时端点不可达")
	} else {
		builder.WriteString("未找到可用的 Docker 或 Podman API 端点")
	}
	if len(e.Tried) > 0 {
		tried := e.Tried
		suffix := ""
		if len(tried) > 6 {
			tried = tried[:6]
			suffix = " 等"
		}
		builder.WriteString("（已尝试：")
		builder.WriteString(strings.Join(tried, "、"))
		builder.WriteString(suffix)
		builder.WriteString("）")
	}
	switch {
	case e.EnvSource != "":
		builder.WriteString("。请确认该地址可访问，或删除该环境变量后重启 Lan Copy 重新自动检测")
	case e.PodmanFound && e.AutostartTried:
		builder.WriteString("。检测到已安装 Podman，Lan Copy 已尝试自动启动 podman.socket 但未成功；请手动执行 systemctl --user enable --now podman.socket（rootful：sudo systemctl enable --now podman.socket），完成后重启 Lan Copy")
	case e.PodmanFound && e.DockerFound:
		builder.WriteString("。检测到已安装 Podman 和 Docker：请启动 Docker 服务，或为 Podman 启用 Docker 兼容 API（rootless 运行 systemctl --user enable --now podman.socket，rootful 运行 sudo systemctl enable --now podman.socket）；完成后重启 Lan Copy")
	case e.PodmanFound:
		builder.WriteString("。检测到已安装 Podman，请启用 Docker 兼容 API：rootless 运行 systemctl --user enable --now podman.socket，rootful 运行 sudo systemctl enable --now podman.socket；完成后重启 Lan Copy，也可设置 DOCKER_HOST 指向 podman.sock 后重启")
	case e.DockerFound:
		builder.WriteString("。检测到已安装 Docker，请确认 Docker 服务已启动")
	default:
		builder.WriteString("。请安装并启动 Docker 或 Podman")
	}
	return builder.String()
}

func (e *DetectError) Unwrap() error { return plugin.ErrRuntimeUnavailable }

// Details returns the raw probe errors for logging only.
func (e *DetectError) Details() string { return strings.Join(e.Problems, "; ") }

type candidate struct {
	host     string
	source   string // short label used in logs
	endpoint string // value shown in user-facing diagnostics
}

func (c candidate) String() string {
	if c.endpoint != "" {
		return c.endpoint
	}
	return c.source
}

// Detect finds a reachable Docker or Podman endpoint without running any
// container CLI. An explicit DOCKER_HOST or CONTAINER_HOST is authoritative: if
// it cannot be reached, detection fails instead of silently using another
// engine. Without those variables the well-known Docker and Podman sockets are
// probed in order.
func Detect(ctx context.Context) (Detected, error) {
	candidates := environmentCandidates()
	envSource := ""
	if len(candidates) == 0 {
		candidates = autoCandidates()
	} else {
		envSource = candidates[0].source
	}
	detected, failure := probeCandidatesWithTimeout(ctx, candidates)
	if failure == nil {
		return detected, nil
	}
	failure.EnvSource = envSource
	failure.PodmanFound = commandExists("podman")
	failure.DockerFound = commandExists("docker")
	if shouldActivatePodman(envSource, failure) {
		failure.AutostartTried = true
		if err := podmanSocketActivator(ctx); err != nil {
			failure.Problems = append(failure.Problems, "自动启动 podman.socket 失败: "+err.Error())
		} else if detected, retry := probePodmanAfterActivation(ctx); retry == nil {
			return detected, nil
		} else {
			failure.Problems = append(failure.Problems, "自动启动后仍无法连接: "+retry.Details())
		}
	}
	return Detected{Type: "docker"}, failure
}

// shouldActivatePodman only intervenes for automatic discovery. An explicit
// DOCKER_HOST or CONTAINER_HOST stays authoritative and untouched.
func shouldActivatePodman(envSource string, failure *DetectError) bool {
	return envSource == "" && failure.PodmanFound && !autostartDisabled() && podmanSocketActivator != nil
}

func autostartDisabled() bool {
	value := strings.TrimSpace(os.Getenv(DisablePodmanAutostartEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

func probeCandidatesWithTimeout(ctx context.Context, candidates []candidate) (Detected, *DetectError) {
	probeCtx, cancel := context.WithTimeout(ctx, detectTotalTimeout)
	defer cancel()
	return probeCandidates(probeCtx, candidates)
}

// probePodmanAfterActivation retries the Podman endpoints briefly, because the
// freshly started socket may take a moment to accept connections.
func probePodmanAfterActivation(ctx context.Context) (Detected, *DetectError) {
	deadline := time.Now().Add(podmanRetryWindow)
	var last *DetectError
	for {
		probeCtx, cancel := context.WithTimeout(ctx, detectProbeTimeout)
		detected, failure := probeCandidates(probeCtx, podmanProbeCandidates())
		cancel()
		if failure == nil {
			return detected, nil
		}
		last = failure
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return Detected{}, last
		}
		select {
		case <-ctx.Done():
			return Detected{}, last
		case <-time.After(podmanRetryInterval):
		}
	}
}

// environmentCandidates returns the explicit endpoint override, if any. The
// first variable set wins and is authoritative: a broken DOCKER_HOST must not
// silently fall back to a Podman socket, or operations could hit the wrong
// engine.
func environmentCandidates() []candidate {
	if host := strings.TrimSpace(os.Getenv("DOCKER_HOST")); host != "" {
		return []candidate{{host: host, source: "DOCKER_HOST", endpoint: host}}
	}
	if host := strings.TrimSpace(os.Getenv("CONTAINER_HOST")); host != "" {
		return []candidate{{host: host, source: "CONTAINER_HOST", endpoint: host}}
	}
	return nil
}

func probeCandidates(ctx context.Context, candidates []candidate) (Detected, *DetectError) {
	failure := &DetectError{Tried: make([]string, 0, len(candidates))}
	for _, entry := range candidates {
		if entry.host == "" {
			continue
		}
		failure.Tried = append(failure.Tried, entry.String())
		probeCtx, cancel := context.WithTimeout(ctx, detectProbeTimeout)
		detected, err := probe(probeCtx, entry)
		cancel()
		if err == nil {
			return detected, nil
		}
		failure.Problems = append(failure.Problems, entry.String()+": "+err.Error())
	}
	if len(failure.Tried) == 0 {
		failure.Problems = append(failure.Problems, "没有可用的候选端点")
	}
	return Detected{}, failure
}

func probe(ctx context.Context, entry candidate) (Detected, error) {
	cli, err := client.New(client.WithHost(entry.host), client.WithAPIVersionNegotiation())
	if err != nil {
		return Detected{}, err
	}
	defer cli.Close()
	ping, err := cli.Ping(ctx, client.PingOptions{})
	if err != nil {
		return Detected{}, err
	}
	version, err := cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return Detected{}, err
	}
	apiVersion := version.APIVersion
	if apiVersion == "" {
		apiVersion = ping.APIVersion
	}
	return Detected{
		Type: engineType(version), Host: entry.host, Source: entry.source,
		Version: version.Version, APIVersion: apiVersion,
		OS: version.Os, Arch: version.Arch,
	}, nil
}

// engineType classifies the server behind a Docker-compatible endpoint. Podman's
// compatibility API reports a "Podman Engine" component while Docker reports
// "Engine" and a Docker platform name.
func engineType(version client.ServerVersionResult) string {
	for _, component := range version.Components {
		if strings.Contains(strings.ToLower(component.Name), "podman") {
			return "podman"
		}
	}
	if strings.Contains(strings.ToLower(version.Platform.Name), "podman") {
		return "podman"
	}
	return "docker"
}

func normalizeType(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "podman") {
		return "podman"
	}
	return "docker"
}

// findCommand resolves a binary through PATH and known install locations. It
// never runs the container CLI.
func findCommand(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	for _, dir := range commandDirectories() {
		for _, filename := range commandFileNames(name) {
			candidate := filepath.Join(dir, filename)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func locateCommand(name string) bool {
	return findCommand(name) != ""
}
