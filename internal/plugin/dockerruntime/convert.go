package dockerruntime

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"lan-copy/internal/plugin"
)

const (
	tmpfsSize = "128m"
)

// buildCreateConfigs is the single place that converts validated plugin
// settings into Docker options. It intentionally has no pass-through fields:
// callers cannot request privileged mode, host namespaces, devices, extra
// capabilities or arbitrary port publishing.
func buildCreateConfigs(req plugin.CreateInstanceRequest) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	if req.Name == "" || req.ImageRef == "" {
		return nil, nil, nil, fmt.Errorf("container name and image are required")
	}
	if req.ContainerPort < 1024 || req.ContainerPort > 65535 {
		return nil, nil, nil, fmt.Errorf("invalid container port")
	}
	if req.NetworkName == "" {
		return nil, nil, nil, fmt.Errorf("plugin network is required")
	}
	if req.CPULimit <= 0 || req.CPULimit > 8 {
		return nil, nil, nil, fmt.Errorf("invalid CPU limit")
	}
	if req.MemoryMB < 64 || req.MemoryMB > 16384 {
		return nil, nil, nil, fmt.Errorf("invalid memory limit")
	}
	if req.PIDsLimit < 16 || req.PIDsLimit > 4096 {
		return nil, nil, nil, fmt.Errorf("invalid PID limit")
	}
	for _, value := range req.Env {
		if strings.ContainsRune(value, 0) {
			return nil, nil, nil, fmt.Errorf("invalid environment variable")
		}
	}
	binds := make([]string, 0, len(req.Mounts))
	for _, mount := range req.Mounts {
		if mount.HostPath == "" || mount.ContainerPath == "" || !filepath.IsAbs(mount.HostPath) {
			return nil, nil, nil, fmt.Errorf("invalid mount")
		}
		if strings.HasSuffix(filepath.Clean(mount.HostPath), "docker.sock") ||
			strings.HasSuffix(filepath.Clean(mount.HostPath), "podman.sock") {
			return nil, nil, nil, fmt.Errorf("container runtime socket cannot be mounted")
		}
		bind := mount.HostPath + ":" + mount.ContainerPath
		if mount.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}
	restartPolicy, err := parseRestartPolicy(req.RestartPolicy)
	if err != nil {
		return nil, nil, nil, err
	}

	port, err := network.ParsePort(fmt.Sprintf("%d/tcp", req.ContainerPort))
	if err != nil {
		return nil, nil, nil, err
	}
	exposedPorts := network.PortSet{port: struct{}{}}
	portBindings := network.PortMap{}
	if req.PublishLoopback {
		portBindings[port] = []network.PortBinding{{
			HostIP: netip.MustParseAddr("127.0.0.1"),
			// Empty host port lets Docker allocate an unused port.
			HostPort: "",
		}}
	}
	labels := map[string]string{plugin.LabelManaged: "true"}
	for name, value := range req.Labels {
		labels[name] = value
	}
	initProcess := true
	pidsLimit := req.PIDsLimit
	memoryBytes := req.MemoryMB << 20
	stopTimeout := req.StopTimeoutSecs
	if stopTimeout <= 0 {
		stopTimeout = 10
	}
	logSize := req.LogMaxSizeMB
	if logSize <= 0 || logSize > 50 {
		logSize = 10
	}
	logFiles := req.LogMaxFiles
	if logFiles <= 0 || logFiles > 5 {
		logFiles = 2
	}

	config := &container.Config{
		Image:        req.ImageRef,
		Env:          append([]string(nil), req.Env...),
		ExposedPorts: exposedPorts,
		Labels:       labels,
		Tty:          false,
		OpenStdin:    false,
		AttachStdin:  false, AttachStdout: false, AttachStderr: false,
		StopTimeout: &stopTimeout,
	}
	extraHosts := make([]string, 0, len(req.ExtraHosts))
	for _, host := range req.ExtraHosts {
		switch host {
		case "host.docker.internal:host-gateway", "host.containers.internal:host-gateway":
			extraHosts = append(extraHosts, host)
		default:
			return nil, nil, nil, fmt.Errorf("%w: unsupported extra host %q", plugin.ErrInvalidConfig, host)
		}
	}
	hostConfig := &container.HostConfig{
		Binds:           binds,
		NetworkMode:     container.NetworkMode(req.NetworkName),
		PortBindings:    portBindings,
		RestartPolicy:   restartPolicy,
		AutoRemove:      false,
		Privileged:      false,
		PublishAllPorts: false,
		ReadonlyRootfs:  true,
		CapAdd:          nil,
		CapDrop:         []string{"ALL"},
		SecurityOpt:     []string{"no-new-privileges:true"},
		Tmpfs:           map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=" + tmpfsSize},
		PidMode:         "",
		IpcMode:         container.IpcMode("private"),
		UTSMode:         "",
		UsernsMode:      "",
		CgroupnsMode:    container.CgroupnsMode("private"),
		VolumesFrom:     nil,
		Links:           nil,
		ExtraHosts:      extraHosts,
		DNS:             nil,
		DNSOptions:      nil,
		DNSSearch:       nil,
		GroupAdd:        nil,
		Sysctls:         nil,
		StorageOpt:      nil,
		Runtime:         "",
		Isolation:       "",
		Init:            &initProcess,
		LogConfig: container.LogConfig{
			Type: "json-file",
			Config: map[string]string{
				"max-size": fmt.Sprintf("%dm", logSize),
				"max-file": fmt.Sprintf("%d", logFiles),
			},
		},
		Resources: container.Resources{
			Memory:     memoryBytes,
			MemorySwap: memoryBytes,
			NanoCPUs:   int64(req.CPULimit * 1e9),
			PidsLimit:  &pidsLimit,
			CPUShares:  0,
		},
	}
	networking := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			req.NetworkName: {},
		},
	}
	return config, hostConfig, networking, nil
}

func parseRestartPolicy(policy string) (container.RestartPolicy, error) {
	switch {
	case policy == "" || policy == "no":
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}, nil
	case policy == "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure}, nil
	case strings.HasPrefix(policy, "on-failure:"):
		var count int
		if _, err := fmt.Sscanf(policy, "on-failure:%d", &count); err != nil || count < 1 || count > 10 {
			return container.RestartPolicy{}, fmt.Errorf("invalid restart policy")
		}
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: count}, nil
	default:
		return container.RestartPolicy{}, fmt.Errorf("unsupported restart policy %q", policy)
	}
}
