package plugin

import (
	"context"
	"io"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestPluginExtraHostsFollowRuntimeType(t *testing.T) {
	tests := []struct {
		networkMode string
		runtimeType string
		hostOS      string
		want        []string
	}{
		{"loopback", "docker", "linux", []string{"host.docker.internal:host-gateway"}},
		{"loopback", "docker", "windows", []string{"host.docker.internal:host-gateway"}},
		{"loopback", "podman", "linux", []string{"host.containers.internal:host-gateway"}},
		{"loopback", "podman", "windows", nil},
		{"loopback", "podman", "darwin", nil},
		{"container", "docker", "linux", nil},
		{"container", "podman", "linux", nil},
	}
	for _, test := range tests {
		got := pluginExtraHosts(test.networkMode, test.runtimeType, test.hostOS)
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s/%s/%s: hosts = %+v, want %+v", test.networkMode, test.runtimeType, test.hostOS, got, test.want)
		}
	}
}

func TestDefaultAPIBaseURL(t *testing.T) {
	lan := []HostAddress{{Interface: "Wi-Fi", IP: "192.168.1.20"}}
	mixed := []HostAddress{
		{Interface: "docker0", IP: "172.17.0.1"},
		{Interface: "Ethernet", IP: "10.0.0.9"},
		{Interface: "en0", IP: "192.168.31.7"},
		{Interface: "loopback", IP: "127.0.0.1"},
		{Interface: "Ethernet", IP: "169.254.10.20"},
	}
	tests := []struct {
		name        string
		networkMode string
		port        string
		runtimeType string
		hostOS      string
		addresses   []HostAddress
		wantURL     string
		wantReason  string
	}{
		{
			name: "docker linux", networkMode: "loopback", port: "8080", runtimeType: "docker", hostOS: "linux",
			wantURL: "http://host.docker.internal:8080", wantReason: "host.docker.internal",
		},
		{
			name: "docker darwin", networkMode: "loopback", port: "8080", runtimeType: "docker", hostOS: "darwin",
			wantURL: "http://host.docker.internal:8080", wantReason: "host.docker.internal",
		},
		{
			name: "native podman linux", networkMode: "loopback", port: "8080", runtimeType: "podman", hostOS: "linux", addresses: lan,
			wantURL: "http://host.containers.internal:8080", wantReason: "原生 Podman",
		},
		{
			name: "podman machine windows", networkMode: "loopback", port: "8080", runtimeType: "podman", hostOS: "windows", addresses: lan,
			wantURL: "http://192.168.1.20:8080", wantReason: "Podman machine（windows）",
		},
		{
			name: "podman machine darwin", networkMode: "loopback", port: "8080", runtimeType: "podman", hostOS: "darwin", addresses: lan,
			wantURL: "http://192.168.1.20:8080", wantReason: "Podman machine（darwin）",
		},
		{
			name: "podman machine prefers 192.168", networkMode: "loopback", port: "8080", runtimeType: "podman", hostOS: "windows", addresses: mixed,
			wantURL: "http://192.168.31.7:8080", wantReason: "en0",
		},
		{
			name: "podman machine without LAN IPv4 does not inject", networkMode: "loopback", port: "8080", runtimeType: "podman", hostOS: "windows",
			addresses: []HostAddress{
				{Interface: "vEthernet (WSL)", IP: "172.20.0.1"},
				{Interface: "Loopback Pseudo-Interface 1", IP: "127.0.0.1"},
				{Interface: "Ethernet", IP: "fe80::1"},
			},
			wantReason: "未注入",
		},
		{
			name: "container mode ignores host OS", networkMode: "container", port: "8080", runtimeType: "podman", hostOS: "windows", addresses: lan,
			wantURL: "http://lan-copy:8080", wantReason: "lan-copy",
		},
		{
			name: "empty port", networkMode: "loopback", port: "", runtimeType: "podman", hostOS: "windows", addresses: lan,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DefaultAPIBaseURL(test.networkMode, test.port, test.runtimeType, test.hostOS, test.addresses)
			if got.URL != test.wantURL {
				t.Fatalf("URL = %q, want %q", got.URL, test.wantURL)
			}
			if test.wantURL == "" {
				if test.wantReason == "" {
					return
				}
				if !strings.Contains(got.Reason, test.wantReason) {
					t.Fatalf("reason = %q, want it to contain %q", got.Reason, test.wantReason)
				}
				return
			}
			if got.URL == "" {
				t.Fatalf("URL is empty, want %q", test.wantURL)
			}
			if !strings.Contains(got.Reason, test.wantReason) {
				t.Fatalf("reason = %q, want it to contain %q", got.Reason, test.wantReason)
			}
		})
	}
}

func TestSelectHostAddressIsDeterministic(t *testing.T) {
	addresses := []HostAddress{
		{Interface: "vEthernet (WSL)", IP: "172.20.0.1"},
		{Interface: "docker0", IP: "172.17.0.1"},
		{Interface: "Ethernet", IP: "10.0.0.9"},
		{Interface: "Wi-Fi", IP: "192.168.1.30"},
		{Interface: "en0", IP: "192.168.1.9"},
		{Interface: "en0", IP: "fe80::1"},
		{Interface: "Ethernet", IP: "169.254.10.20"},
		{Interface: "Ethernet", IP: "100.64.0.2"},
	}
	got, ok := selectHostAddress(addresses)
	if !ok {
		t.Fatal("no host address selected")
	}
	if got.Interface != "en0" || got.IP != "192.168.1.9" {
		t.Fatalf("selected = %+v", got)
	}

	sameRank := []HostAddress{
		{Interface: "Wi-Fi", IP: "10.0.0.20"},
		{Interface: "Ethernet", IP: "10.0.0.3"},
	}
	got, ok = selectHostAddress(sameRank)
	if !ok || got.IP != "10.0.0.3" {
		t.Fatalf("same-rank selection = %+v, %v", got, ok)
	}
	if _, ok := selectHostAddress(nil); ok {
		t.Fatal("empty address list selected an address")
	}
}

func TestIsVirtualInterfaceName(t *testing.T) {
	virtualNames := []string{
		"docker0", "veth123", "br-abc", "br0", "virbr0", "vmnet8", "vboxnet0",
		"vEthernet (WSL)", "Hyper-V Virtual Ethernet Adapter", "utun3",
		"tun0", "tap0", "tailscale0", "wg0", "ztabc", "bridge0", "awdl0", "llw0", "lo0",
	}
	for _, name := range virtualNames {
		if !isVirtualInterfaceName(name) {
			t.Fatalf("%q should be treated as virtual", name)
		}
	}
	for _, name := range []string{"en0", "Ethernet", "Wi-Fi", "以太网", "WLAN"} {
		if isVirtualInterfaceName(name) {
			t.Fatalf("%q should not be treated as virtual", name)
		}
	}
}

func TestPluginAPITokenInjectionAndAuthentication(t *testing.T) {
	dir := t.TempDir()
	box, err := LoadSecretBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newFakeRuntime()
	manager, err := NewManager(store, runtime, Options{
		Runtime:    RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: "plugin-network"},
		Logger:     log.New(io.Discard, "", 0),
		APIBaseURL: "http://host.docker.internal:18080",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(context.Background()) })
	manifest := testManifest(8080)
	server, _ := eventServer(t, manifest, new(string), 200)
	defer server.Close()
	runtime.address = server.URL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) == 0 {
		t.Fatal("container was not created")
	}
	env := runtime.created[0].Env
	if value := envValue(env, APIBaseURLEnv); value != "http://host.docker.internal:18080" {
		t.Fatalf("API URL = %q", value)
	}
	if hosts := runtime.created[0].ExtraHosts; len(hosts) != 1 || hosts[0] != "host.docker.internal:host-gateway" {
		t.Fatalf("extra hosts = %+v", hosts)
	}
	token := envValue(env, APITokenEnv)
	if token == "" {
		t.Fatal("API token was not injected")
	}
	pluginID, ok := manager.AuthenticateAPIToken(token)
	if !ok || pluginID != "demo" {
		t.Fatalf("authenticate = %q %v", pluginID, ok)
	}
	if _, ok := manager.AuthenticateAPIToken("wrong"); ok {
		t.Fatal("wrong token accepted")
	}
	if err := manager.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.AuthenticateAPIToken(token); ok {
		t.Fatal("stopped instance token accepted")
	}
	if got := envValue(runtime.created[0].Env, APITokenEnv); got == "" || strings.Contains(got, " ") {
		t.Fatalf("unexpected token %q", got)
	}
}

func TestPodmanMachineInjectionUsesLANAddress(t *testing.T) {
	manager, runtime := newManagerForAPIAddressTest(t, Options{
		Runtime:       RuntimeSettings{Type: "podman", NetworkMode: "loopback", Network: "plugin-network"},
		Port:          "8080",
		HostOS:        "windows",
		HostAddresses: func() []HostAddress { return []HostAddress{{Interface: "Wi-Fi", IP: "192.168.1.20"}} },
	})
	manifest := testManifest(8080)
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) == 0 {
		t.Fatal("container was not created")
	}
	created := runtime.created[0]
	if got := envValue(created.Env, APIBaseURLEnv); got != "http://192.168.1.20:8080" {
		t.Fatalf("API URL = %q", got)
	}
	if len(created.ExtraHosts) != 0 {
		t.Fatalf("extra hosts = %+v, want none", created.ExtraHosts)
	}
	info := manager.RuntimeInfo(context.Background())
	if info.APIBaseURL != "http://192.168.1.20:8080" {
		t.Fatalf("displayed API URL = %q", info.APIBaseURL)
	}
	if !strings.Contains(info.APIBaseURLReason, "Podman machine（windows）") {
		t.Fatalf("displayed reason = %q", info.APIBaseURLReason)
	}
}

func TestExplicitAPIURLOverridesPodmanMachineInference(t *testing.T) {
	manager, runtime := newManagerForAPIAddressTest(t, Options{
		Runtime:       RuntimeSettings{Type: "podman", NetworkMode: "loopback", Network: "plugin-network"},
		Port:          "8080",
		HostOS:        "darwin",
		HostAddresses: func() []HostAddress { return []HostAddress{{Interface: "en0", IP: "192.168.1.20"}} },
		APIBaseURL:    "http://10.1.2.3:9000/",
	})
	manifest := testManifest(8080)
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if got := envValue(runtime.created[0].Env, APIBaseURLEnv); got != "http://10.1.2.3:9000" {
		t.Fatalf("API URL = %q", got)
	}
	info := manager.RuntimeInfo(context.Background())
	if info.APIBaseURL != "http://10.1.2.3:9000" || !strings.Contains(info.APIBaseURLReason, "-plugin-api-url") {
		t.Fatalf("runtime info = %+v", info)
	}
}

func TestRestartReinjectsAPIAddress(t *testing.T) {
	manager, runtime := newManagerForAPIAddressTest(t, Options{
		Runtime:    RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: "plugin-network"},
		APIBaseURL: "http://one.example:8080",
	})
	manifest := testManifest(8080)
	server := newPluginServer(t, manifest, nil)
	defer server.Close()
	runtime.address = server.URL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if got := envValue(runtime.created[0].Env, APIBaseURLEnv); got != "http://one.example:8080" {
		t.Fatalf("initial API URL = %q", got)
	}
	manager.apiBaseURL = "http://two.example:8080"
	if err := manager.Restart(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) != 2 {
		t.Fatalf("containers created = %d, want 2 (restart must recreate)", len(runtime.created))
	}
	if got := envValue(runtime.created[1].Env, APIBaseURLEnv); got != "http://two.example:8080" {
		t.Fatalf("reinjected API URL = %q", got)
	}
	if len(runtime.containers) != 1 {
		t.Fatalf("containers = %d, want one after recreate", len(runtime.containers))
	}
}

func newManagerForAPIAddressTest(t *testing.T, options Options) (*Manager, *fakeRuntime) {
	t.Helper()
	dir := t.TempDir()
	box, err := LoadSecretBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newFakeRuntime()
	runtime.info.Type = options.Runtime.Type
	options.Logger = log.New(io.Discard, "", 0)
	manager, err := NewManager(store, runtime, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(context.Background()) })
	return manager, runtime
}

func TestStartRuntimeUpdatesTypeAndInjection(t *testing.T) {
	dir := t.TempDir()
	box, err := LoadSecretBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newFakeRuntime()
	runtime.info = RuntimeInfo{
		Type: "docker", Available: false, StartAvailable: true,
		NetworkMode: "loopback", Network: "plugin-network",
	}
	manager, err := NewManager(store, runtime, Options{
		Runtime: RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: "plugin-network"},
		Logger:  log.New(io.Discard, "", 0),
		Port:    "18080",
		HostOS:  "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(context.Background()) })

	if err := manager.StartRuntime(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := manager.currentRuntimeType(); got != "podman" {
		t.Fatalf("runtime type = %q", got)
	}
	manifest := testManifest(8080)
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) == 0 {
		t.Fatal("container was not created")
	}
	created := runtime.created[0]
	if got := envValue(created.Env, APIBaseURLEnv); got != "http://host.containers.internal:18080" {
		t.Fatalf("API URL = %q", got)
	}
	if len(created.ExtraHosts) != 1 || created.ExtraHosts[0] != "host.containers.internal:host-gateway" {
		t.Fatalf("extra hosts = %+v", created.ExtraHosts)
	}
}
