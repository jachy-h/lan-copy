package dockerruntime

import (
	"os"
	"path/filepath"
	"testing"

	"lan-copy/internal/plugin"
)

func testMountHostPath() string {
	return filepath.Join(os.TempDir(), "lan-copy-plugin-test-data")
}

func validCreateRequest() plugin.CreateInstanceRequest {
	return plugin.CreateInstanceRequest{
		Name: "lan-copy-plugin-demo-12345678", ImageRef: "sha256:image",
		Env: []string{"A=1"}, ContainerPort: 8080, NetworkName: "plugin-network",
		PublishLoopback: true,
		Mounts:          []plugin.Mount{{HostPath: testMountHostPath(), ContainerPath: "/data", ReadOnly: true}},
		CPULimit:        0.5, MemoryMB: 512, PIDsLimit: 128,
		RestartPolicy: "on-failure:2", LogMaxSizeMB: 10, LogMaxFiles: 2,
		Labels: map[string]string{plugin.LabelPluginID: "demo"},
	}
}

func TestBuildCreateConfigsSecurityInvariants(t *testing.T) {
	request := validCreateRequest()
	config, host, networking, err := buildCreateConfigs(request)
	if err != nil {
		t.Fatal(err)
	}
	if config.Image != "sha256:image" || len(config.Env) != 1 || config.Tty {
		t.Fatalf("config = %+v", config)
	}
	if config.User != "" {
		t.Fatal("runtime must not override the image user")
	}
	if host.Privileged {
		t.Fatal("privileged container requested")
	}
	if !host.ReadonlyRootfs {
		t.Fatal("root filesystem must be read-only")
	}
	if len(host.CapDrop) != 1 || host.CapDrop[0] != "ALL" || len(host.CapAdd) != 0 {
		t.Fatalf("capabilities = %+v / %+v", host.CapAdd, host.CapDrop)
	}
	if len(host.SecurityOpt) != 1 || host.SecurityOpt[0] != "no-new-privileges:true" {
		t.Fatalf("security opts = %+v", host.SecurityOpt)
	}
	if host.NetworkMode != "plugin-network" || host.PidMode != "" || host.UTSMode != "" || host.UsernsMode != "" {
		t.Fatalf("namespace settings = %+v", host)
	}
	if host.IpcMode != "private" || host.CgroupnsMode != "private" {
		t.Fatalf("ipc/cgroup settings = %q / %q", host.IpcMode, host.CgroupnsMode)
	}
	if host.Init == nil || !*host.Init {
		t.Fatal("init process not enabled")
	}
	if host.Resources.Memory != 512<<20 || host.Resources.MemorySwap != 512<<20 {
		t.Fatalf("memory = %+v", host.Resources)
	}
	if host.Resources.NanoCPUs != 500000000 || host.Resources.PidsLimit == nil || *host.Resources.PidsLimit != 128 {
		t.Fatalf("resources = %+v", host.Resources)
	}
	if host.RestartPolicy.Name != "on-failure" || host.RestartPolicy.MaximumRetryCount != 2 {
		t.Fatalf("restart = %+v", host.RestartPolicy)
	}
	if host.LogConfig.Type != "json-file" || host.LogConfig.Config["max-size"] != "10m" {
		t.Fatalf("log config = %+v", host.LogConfig)
	}
	if host.Tmpfs["/tmp"] == "" {
		t.Fatal("tmpfs not configured")
	}
	if len(host.Binds) != 1 || host.Binds[0] != testMountHostPath()+":/data:ro" {
		t.Fatalf("binds = %+v", host.Binds)
	}
	if len(host.PortBindings) != 1 {
		t.Fatalf("port bindings = %+v", host.PortBindings)
	}
	for port, bindings := range host.PortBindings {
		if port.Num() != 8080 || len(bindings) != 1 || bindings[0].HostIP.String() != "127.0.0.1" || bindings[0].HostPort != "" {
			t.Fatalf("port binding = %v %+v", port, bindings)
		}
	}
	if len(host.Devices) != 0 || len(host.DeviceRequests) != 0 || len(host.VolumesFrom) != 0 || len(host.ExtraHosts) != 0 || len(host.DNS) != 0 {
		t.Fatal("forbidden host options were populated")
	}
	if networking.EndpointsConfig["plugin-network"] == nil {
		t.Fatal("plugin network missing")
	}
	if config.Labels[plugin.LabelManaged] != "true" || config.Labels[plugin.LabelPluginID] != "demo" {
		t.Fatalf("labels = %+v", config.Labels)
	}
}

func TestBuildCreateConfigsContainerModeHasNoPublishedPort(t *testing.T) {
	request := validCreateRequest()
	request.PublishLoopback = false
	_, host, _, err := buildCreateConfigs(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.PortBindings) != 0 {
		t.Fatalf("port bindings = %+v", host.PortBindings)
	}
}

func TestBuildCreateConfigsRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*plugin.CreateInstanceRequest)
	}{
		{"no image", func(r *plugin.CreateInstanceRequest) { r.ImageRef = "" }},
		{"bad port", func(r *plugin.CreateInstanceRequest) { r.ContainerPort = 80 }},
		{"no network", func(r *plugin.CreateInstanceRequest) { r.NetworkName = "" }},
		{"bad cpu", func(r *plugin.CreateInstanceRequest) { r.CPULimit = 0 }},
		{"bad memory", func(r *plugin.CreateInstanceRequest) { r.MemoryMB = 1 }},
		{"bad pids", func(r *plugin.CreateInstanceRequest) { r.PIDsLimit = 1 }},
		{"restart always", func(r *plugin.CreateInstanceRequest) { r.RestartPolicy = "always" }},
		{"relative mount", func(r *plugin.CreateInstanceRequest) {
			r.Mounts = []plugin.Mount{{HostPath: "relative", ContainerPath: "/data"}}
		}},
		{"docker socket", func(r *plugin.CreateInstanceRequest) {
			r.Mounts = []plugin.Mount{{HostPath: "/var/run/docker.sock", ContainerPath: "/sock"}}
		}},
		{"podman socket", func(r *plugin.CreateInstanceRequest) {
			r.Mounts = []plugin.Mount{{HostPath: "/run/podman/podman.sock", ContainerPath: "/sock"}}
		}},
		{"nul env", func(r *plugin.CreateInstanceRequest) { r.Env = []string{"A=x\x00y"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCreateRequest()
			test.mutate(&request)
			if _, _, _, err := buildCreateConfigs(request); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
}

func TestExtraHostsOnlyAllowsFixedHostGateway(t *testing.T) {
	for _, allowed := range []string{"host.docker.internal:host-gateway", "host.containers.internal:host-gateway"} {
		request := validCreateRequest()
		request.ExtraHosts = []string{allowed}
		_, host, _, err := buildCreateConfigs(request)
		if err != nil {
			t.Fatalf("%s: %v", allowed, err)
		}
		if len(host.ExtraHosts) != 1 || host.ExtraHosts[0] != allowed {
			t.Fatalf("extra hosts = %+v", host.ExtraHosts)
		}
	}
	request := validCreateRequest()
	request.ExtraHosts = []string{"evil.example:1.2.3.4"}
	if _, _, _, err := buildCreateConfigs(request); err == nil {
		t.Fatal("arbitrary extra host accepted")
	}
}
