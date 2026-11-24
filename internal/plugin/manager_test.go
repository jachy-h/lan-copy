package plugin

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) (*Manager, *fakeRuntime, *Store) {
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
	manager, err := NewManager(store, runtime, Options{
		Runtime: RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: "plugin-network"},
		Logger:  log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	})
	return manager, runtime, store
}

func testManifest(port int) Manifest {
	return NormalizeManifest(Manifest{
		SchemaVersion: 1, ID: "demo", Name: "Demo", Version: "1.0.0",
		ProtocolVersion: "1.0", Runtime: "http", ContainerPort: port,
		Endpoints:    ManifestEndpoints{Health: "/health", Manifest: "/manifest", Version: "/version", Capabilities: "/capabilities"},
		Capabilities: []string{"demo.echo"},
	})
}

func manifestLabel(t *testing.T, manifest Manifest) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func testImageInfo(t *testing.T, manifest Manifest) ImageInfo {
	return testImageInfoFor(t, manifest, "sha256:image1", "lan-copy/demo:1.0")
}

func testImageInfoFor(t *testing.T, manifest Manifest, id, reference string) ImageInfo {
	return ImageInfo{
		ID: id, References: []string{reference},
		Digest: "sha256:digest-" + manifest.Version, SizeBytes: 1234,
		Architecture: "arm64", OS: "linux", User: "1000:1000",
		Labels:  map[string]string{ManifestLabel: manifestLabel(t, manifest)},
		Created: time.Now().UTC(),
	}
}

func makeTar(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	data := []byte("not a real docker image, only archive sniffing is exercised")
	if err := writer.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func importTestImage(t *testing.T, manager *Manager, runtime *fakeRuntime) Image {
	t.Helper()
	manifest := testManifest(8080)
	runtime.imported = []ImageInfo{testImageInfo(t, manifest)}
	staged, err := manager.StageImage("demo.tar", bytes.NewReader(makeTar(t)))
	if err != nil {
		t.Fatal(err)
	}
	image, err := manager.ImportStaged(context.Background(), staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func newPluginServer(t *testing.T, manifest Manifest, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok","version":"1.0.0"}`)
		case "/manifest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestJSON)
		case "/version":
			_, _ = io.WriteString(w, `{"version":"1.0.0"}`)
		default:
			if handler != nil {
				handler(w, r)
			} else {
				http.NotFound(w, r)
			}
		}
	}))
}

func TestManifestValidation(t *testing.T) {
	valid := testManifest(8080)
	if err := ValidateManifest(valid); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"schema", func(m *Manifest) { m.SchemaVersion = 2 }},
		{"id", func(m *Manifest) { m.ID = "Demo Plugin" }},
		{"version", func(m *Manifest) { m.Version = "" }},
		{"protocol", func(m *Manifest) { m.ProtocolVersion = "2.0" }},
		{"runtime", func(m *Manifest) { m.Runtime = "process" }},
		{"port", func(m *Manifest) { m.ContainerPort = 80 }},
		{"endpoint", func(m *Manifest) { m.Endpoints.Health = "health" }},
		{"endpoint traversal", func(m *Manifest) { m.Endpoints.Version = "/../etc" }},
		{"capability", func(m *Manifest) { m.Capabilities = []string{"BAD"} }},
		{"duplicate capability", func(m *Manifest) { m.Capabilities = []string{"a.b", "a.b"} }},
		{"default cpu", func(m *Manifest) { m.Defaults.CPULimit = 100 }},
		{"default memory", func(m *Manifest) { m.Defaults.MemoryMB = 1 }},
		{"category", func(m *Manifest) { m.Category = "Not Valid" }},
		{"ui path relative", func(m *Manifest) { m.UI = &ManifestUI{Path: "ui"} }},
		{"ui path traversal", func(m *Manifest) { m.UI = &ManifestUI{Path: "/../etc"} }},
		{"ui path query", func(m *Manifest) { m.UI = &ManifestUI{Path: "/ui?x=1"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := valid
			test.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	if _, err := ParseManifest(`{"schemaVersion":1,"id":"demo","name":"Demo","version":"1","protocolVersion":"1.0","runtime":"http","containerPort":8080,"endpoints":{"health":"/health","manifest":"/manifest","version":"/version","capabilities":"/capabilities"},"capabilities":["a.b"],"privileged":true}`); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	if _, err := ParseManifest("not json"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if !ProtocolCompatible("1.9") || ProtocolCompatible("2.0") {
		t.Fatal("protocol compatibility is wrong")
	}
}

func TestManifestUIPath(t *testing.T) {
	manifest := testManifest(8080)
	manifest.UI = &ManifestUI{}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseManifest(string(raw))
	if err != nil {
		t.Fatalf("manifest with empty ui rejected: %v", err)
	}
	if parsed.UI == nil || parsed.UI.Path != "/" {
		t.Fatalf("ui = %+v, want default /", parsed.UI)
	}
	if manifest.UI.Path != "" {
		t.Fatal("normalization mutated the input manifest")
	}

	manifest.UI = &ManifestUI{Path: "/console"}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParseManifest(string(raw))
	if err != nil {
		t.Fatalf("manifest with ui path rejected: %v", err)
	}
	if parsed.UI == nil || parsed.UI.Path != "/console" {
		t.Fatalf("ui = %+v, want /console", parsed.UI)
	}

	withoutUI := testManifest(8080)
	raw, err = json.Marshal(withoutUI)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParseManifest(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.UI != nil {
		t.Fatalf("ui = %+v, want nil", parsed.UI)
	}
}

func TestProxyTarget(t *testing.T) {
	manager, _, _ := testManager(t)
	manifest := testManifest(8080)
	manifest.UI = &ManifestUI{Path: "/console"}
	set := func(instance Instance, ui *ManifestUI) {
		imageManifest := manifest
		imageManifest.UI = ui
		manager.mu.Lock()
		manager.state.Plugins = []Plugin{{ID: "demo"}}
		manager.state.Images = []Image{{ID: "img-1", PluginID: "demo", Manifest: imageManifest}}
		manager.state.Instances = []Instance{instance}
		manager.mu.Unlock()
	}

	running := Instance{
		ID: "inst-1", PluginID: "demo", ImageID: "img-1",
		Status: StatusRunning, HealthStatus: HealthHealthy,
		InternalAddress: "http://127.0.0.1:4567",
	}
	set(running, manifest.UI)
	target, err := manager.ProxyTarget("demo")
	if err != nil {
		t.Fatalf("proxy target: %v", err)
	}
	if target.BaseURL != "http://127.0.0.1:4567" || target.UIPath != "/console" {
		t.Fatalf("target = %+v", target)
	}

	tests := []struct {
		name     string
		instance Instance
		ui       *ManifestUI
		want     error
	}{
		{"no ui", running, nil, ErrPluginNoUI},
		{"stopped", func() Instance { value := running; value.Status = StatusStopped; return value }(), manifest.UI, ErrPluginNotRunning},
		{"no address", func() Instance { value := running; value.InternalAddress = ""; return value }(), manifest.UI, ErrPluginNotRunning},
		{"unhealthy", func() Instance { value := running; value.HealthStatus = HealthUnhealthy; return value }(), manifest.UI, ErrPluginUnhealthy},
		{"status unhealthy", func() Instance {
			value := running
			value.Status = StatusUnhealthy
			value.HealthStatus = HealthUnhealthy
			return value
		}(), manifest.UI, ErrPluginUnhealthy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set(test.instance, test.ui)
			if _, err := manager.ProxyTarget("demo"); !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}

	set(running, manifest.UI)
	if _, err := manager.ProxyTarget("missing"); !errors.Is(err, ErrPluginNotInstalled) {
		t.Fatalf("missing plugin err = %v", err)
	}
}

func TestStageAndImportImage(t *testing.T) {
	manager, runtime, store := testManager(t)
	manifest := testManifest(8080)
	runtime.imported = []ImageInfo{testImageInfo(t, manifest)}
	tarBytes := makeTar(t)
	staged, err := manager.StageImage("demo.tar", bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatal(err)
	}
	if staged.Size != int64(len(tarBytes)) || len(staged.SHA256) != 64 {
		t.Fatalf("unexpected staged metadata: %+v", staged)
	}
	image, err := manager.ImportStaged(context.Background(), staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	if image.PluginID != "demo" || image.Manifest.ContainerPort != 8080 {
		t.Fatalf("unexpected image: %+v", image)
	}
	if _, err := os.Stat(staged.Path); !os.IsNotExist(err) {
		t.Fatal("staged file was not removed")
	}
	plugins := manager.Plugins()
	if len(plugins) != 1 || plugins[0].Plugin.Status != StatusNotInstalled {
		t.Fatalf("unexpected plugins: %+v", plugins)
	}
	if len(manager.Images()) != 1 {
		t.Fatalf("images = %d", len(manager.Images()))
	}
	// Importing the same image again reuses the record.
	staged, err = manager.StageImage("demo.tar", bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.ImportStaged(context.Background(), staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != image.ID || len(manager.Images()) != 1 {
		t.Fatal("duplicate import created a new record")
	}
	if _, err := os.Stat(filepath.Join(store.TmpDir())); err != nil {
		t.Fatal(err)
	}
}

func TestStageRejectsInvalidArchives(t *testing.T) {
	manager, runtime, _ := testManager(t)
	runtime.imported = []ImageInfo{testImageInfo(t, testManifest(8080))}
	if _, err := manager.StageImage("demo.zip", bytes.NewReader(makeTar(t))); !errors.Is(err, ErrUnsupportedArchive) {
		t.Fatalf("extension error = %v", err)
	}
	if _, err := manager.StageImage("demo.tar", strings.NewReader("not a tar")); !errors.Is(err, ErrUnsupportedArchive) {
		t.Fatalf("magic error = %v", err)
	}
	manager.maxImageBytes = 10
	if _, err := manager.StageImage("demo.tar", bytes.NewReader(makeTar(t))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size error = %v", err)
	}
}

func TestImportRejectsRootAndArchitecture(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifest(8080)
	info := testImageInfo(t, manifest)
	info.User = "root"
	runtime.imported = []ImageInfo{info}
	staged, err := manager.StageImage("demo.tar", bytes.NewReader(makeTar(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportStaged(context.Background(), staged, nil); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("root image error = %v", err)
	}
	info.User = "1000"
	info.Architecture = "amd64"
	runtime.imported = []ImageInfo{info}
	staged, err = manager.StageImage("demo.tar", bytes.NewReader(makeTar(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportStaged(context.Background(), staged, nil); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("arch error = %v", err)
	}
	info.Architecture = "arm64"
	info.Labels = map[string]string{}
	runtime.imported = []ImageInfo{info}
	staged, err = manager.StageImage("demo.tar", bytes.NewReader(makeTar(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportStaged(context.Background(), staged, nil); !errors.Is(err, ErrNoPluginManifest) {
		t.Fatalf("manifest error = %v", err)
	}
}

func TestInstallStartStopAndUninstall(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifest(8080)
	server := newPluginServer(t, manifest, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/echo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"message":"pong"}`)
			return
		}
		http.NotFound(w, r)
	})
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)

	result, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Instance.Status != StatusStopped || result.Instance.RuntimeInstanceID == "" {
		t.Fatalf("unexpected instance: %+v", result.Instance)
	}
	if result.Instance.Config.Env == nil {
		t.Fatal("env should be initialized")
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusRunning || status.HealthStatus != HealthHealthy || status.Stats == nil {
		t.Fatalf("unexpected status: %+v", status)
	}
	var output struct{ Message string }
	if _, err := manager.Client().Do(context.Background(), "demo", Request{Method: http.MethodPost, Path: "/echo", JSON: map[string]string{"ping": "x"}}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Message != "pong" {
		t.Fatalf("client response = %+v", output)
	}
	status, _ = manager.Status(context.Background(), "demo")
	if status.Metrics.Calls != 1 {
		t.Fatalf("metrics = %+v", status.Metrics)
	}
	if err := manager.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	status, _ = manager.Status(context.Background(), "demo")
	if status.Status != StatusStopped {
		t.Fatalf("stop status = %s", status.Status)
	}
	if err := manager.Restart(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	status, _ = manager.Status(context.Background(), "demo")
	if status.Status != StatusRunning {
		t.Fatalf("restart status = %s", status.Status)
	}
	if err := manager.Uninstall(context.Background(), "demo", UninstallRequest{}); err != nil {
		t.Fatal(err)
	}
	plugins := manager.Plugins()
	if len(plugins) != 1 || plugins[0].Plugin.Status != StatusNotInstalled || plugins[0].Instance != nil {
		t.Fatalf("uninstall should keep plugin metadata for the retained image: %+v", plugins)
	}
	if len(manager.Images()) != 1 {
		t.Fatal("uninstall should keep the image record")
	}
	if len(runtime.containers) != 0 {
		t.Fatal("uninstall did not remove the container")
	}
}

func TestInstallStartFailsOnManifestMismatch(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifest(8080)
	served := manifest
	served.ID = "other"
	server := newPluginServer(t, served, nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Start: true}); !errors.Is(err, ErrPluginProtocolMismatch) {
		t.Fatalf("start error = %v", err)
	}
	status, _ := manager.Status(context.Background(), "demo")
	if status.Status != StatusFailed {
		t.Fatalf("status = %s", status.Status)
	}
}

func TestInstallRejectsSecondInstanceAndImageRemovalInUse(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("second install error = %v", err)
	}
	if err := manager.RemoveImage(context.Background(), image.ID); !errors.Is(err, ErrImageInUse) {
		t.Fatalf("remove in-use image error = %v", err)
	}
}

func TestViewsMaskSecretEnvironment(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	result, err := manager.Install(context.Background(), InstallRequest{
		ImageID: image.ID,
		Config:  Config{Env: []EnvVar{{Name: "TOKEN", Value: "do-not-leak", Secret: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Instance.Config.Env[0].Value != "" {
		t.Fatal("install result leaked a secret")
	}
	views := manager.Plugins()
	if views[0].Instance == nil || views[0].Instance.Config.Env[0].Value != "" {
		t.Fatal("plugin list leaked a secret")
	}
	detail, err := manager.Detail(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Instance.Config.Env[0].Value != "" {
		t.Fatal("plugin detail leaked a secret")
	}
	config, err := manager.Config("demo")
	if err != nil {
		t.Fatal(err)
	}
	if config.Env[0].Value != "" {
		t.Fatal("config endpoint leaked a secret")
	}
	containerRequest := runtime.created[0]
	found := false
	for _, variable := range containerRequest.Env {
		if variable == "TOKEN=do-not-leak" {
			found = true
		}
	}
	if !found {
		t.Fatal("container did not receive the secret environment variable")
	}
}

func TestUninstallDeleteDataRequiresExplicitRequest(t *testing.T) {
	manager, runtime, store := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	dataDir := filepath.Join(store.DataDir(), "demo")
	config := Config{Mounts: []Mount{{HostPath: dataDir, ContainerPath: "/data"}}}
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Config: config}); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dataDir, "keep.txt")
	if err := os.WriteFile(payload, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), "demo", UninstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatal("data should be kept by default")
	}
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Config: config}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), "demo", UninstallRequest{DeleteData: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(payload); !os.IsNotExist(err) {
		t.Fatal("data should be deleted when requested")
	}
}

func TestReconcileUsesActualContainerState(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	result, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID})
	if err != nil {
		t.Fatal(err)
	}
	runtime.setContainerRunning(result.Instance.RuntimeInstanceID, true, 0)
	manager.reconcile(context.Background())
	plugins := manager.Plugins()
	if plugins[0].Instance.Status != StatusRunning || plugins[0].Plugin.Status != StatusRunning {
		t.Fatalf("status = %s / plugin %s", plugins[0].Instance.Status, plugins[0].Plugin.Status)
	}
	runtime.setContainerRunning(result.Instance.RuntimeInstanceID, false, 7)
	manager.reconcile(context.Background())
	plugins = manager.Plugins()
	if plugins[0].Instance.Status != StatusFailed || !strings.Contains(plugins[0].Instance.StatusReason, "7") {
		t.Fatalf("crash status = %+v", plugins[0].Instance)
	}
	if plugins[0].Plugin.Status != StatusFailed {
		t.Fatalf("plugin status = %s", plugins[0].Plugin.Status)
	}
	runtime.removeContainer(result.Instance.RuntimeInstanceID)
	manager.reconcile(context.Background())
	plugins = manager.Plugins()
	if plugins[0].Instance.Status != StatusFailed || !strings.Contains(plugins[0].Instance.StatusReason, "容器不存在") {
		t.Fatalf("missing status = %+v", plugins[0].Instance)
	}
}

func TestStatusSyncAtStartupAdoptsRunningContainer(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	result, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: the persisted state says stopped, the container runs.
	runtime.setContainerRunning(result.Instance.RuntimeInstanceID, true, 0)
	manager.reconcile(context.Background())
	if manager.Plugins()[0].Instance.Status != StatusRunning {
		t.Fatal("running container was not adopted")
	}
}

func TestStatusReflectsRuntimeImmediately(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	result, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID})
	if err != nil {
		t.Fatal(err)
	}
	runtime.setContainerRunning(result.Instance.RuntimeInstanceID, true, 0)
	status, err := manager.Status(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusRunning {
		t.Fatalf("externally started container status = %s", status.Status)
	}
	runtime.setContainerRunning(result.Instance.RuntimeInstanceID, false, 9)
	status, err = manager.Status(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusFailed || !strings.Contains(status.StatusReason, "9") {
		t.Fatalf("crash status = %+v", status)
	}
}

func TestImportNewImageVersionKeepsInstalledVersion(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	first := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: first.ID}); err != nil {
		t.Fatal(err)
	}

	versionTwo := testManifest(8080)
	versionTwo.Version = "2.0.0"
	runtime.imported = []ImageInfo{testImageInfoFor(t, versionTwo, "sha256:image2", "lan-copy/demo:2.0")}
	staged, err := manager.StageImage("demo-v2.tar", bytes.NewReader(makeTar(t)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.ImportStaged(context.Background(), staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || len(manager.Images()) != 2 {
		t.Fatal("a new image version should create a new image record")
	}
	plugins := manager.Plugins()
	if plugins[0].Plugin.CurrentVersion != "1.0.0" {
		t.Fatalf("installed version changed to %s", plugins[0].Plugin.CurrentVersion)
	}
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: second.ID}); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("second install error = %v", err)
	}
	if err := manager.Uninstall(context.Background(), "demo", UninstallRequest{}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Install(context.Background(), InstallRequest{ImageID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Plugin.CurrentVersion != "2.0.0" {
		t.Fatalf("installed version = %s", result.Plugin.CurrentVersion)
	}
}

func TestConfigUpdatesOnlyWhileStopped(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Start: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateConfig("demo", Config{MemoryMB: 1024}); !errors.Is(err, ErrConflict) {
		t.Fatalf("running config update error = %v", err)
	}
	if err := manager.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.UpdateConfig("demo", Config{MemoryMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MemoryMB != 1024 || updated.CPULimit != 0.5 {
		t.Fatalf("merged config = %+v", updated)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) != 2 {
		t.Fatalf("container was not recreated, creates = %d", len(runtime.created))
	}
	if runtime.created[len(runtime.created)-1].MemoryMB != 1024 {
		t.Fatalf("new container memory = %d", runtime.created[len(runtime.created)-1].MemoryMB)
	}
	status, _ := manager.Status(context.Background(), "demo")
	if status.Status != StatusRunning {
		t.Fatalf("status after config apply = %s", status.Status)
	}
}

func TestManagerBusyGuard(t *testing.T) {
	manager, _, _ := testManager(t)
	unlock, err := manager.lockInstance("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), "demo"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy error = %v", err)
	}
	unlock()
}

func TestClientErrorsAndLimits(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/slow":
			time.Sleep(200 * time.Millisecond)
			_, _ = io.WriteString(w, `{}`)
		case "/big":
			_, _ = w.Write(bytes.Repeat([]byte("x"), DefaultResponseBytes+10))
		}
	})
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Start: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Client().Do(context.Background(), "demo", Request{Path: "/fail"}, nil); !errors.Is(err, ErrPluginRequestFailed) {
		t.Fatalf("failure error = %v", err)
	}
	if _, err := manager.Client().Do(context.Background(), "demo", Request{Path: "/slow", Timeout: 50 * time.Millisecond}, nil); !errors.Is(err, ErrPluginTimeout) {
		t.Fatalf("timeout error = %v", err)
	}
	if _, err := manager.Client().Do(context.Background(), "demo", Request{Path: "/big"}, nil); !errors.Is(err, ErrPluginResponseTooLarge) {
		t.Fatalf("size error = %v", err)
	}
	if err := manager.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Client().Do(context.Background(), "demo", Request{Path: "/fail"}, nil); !errors.Is(err, ErrPluginNotRunning) {
		t.Fatalf("not running error = %v", err)
	}
}

func TestUninstallWithImageDeletion(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), nil)
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background(), "demo", UninstallRequest{DeleteImages: true}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Images()) != 0 || len(manager.Plugins()) != 0 {
		t.Fatal("image and plugin records should be removed")
	}
}

func TestRunTaskHistoryAndRecovery(t *testing.T) {
	manager, _, store := testManager(t)
	task, err := manager.RunTask("test", "demo", func(context.Context, func(int, string)) (any, error) {
		return map[string]string{"ok": "yes"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := manager.Task(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == TaskSucceeded {
			if string(current.Result) != `{"ok":"yes"}` {
				t.Fatalf("result = %s", current.Result)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if current, _ := manager.Task(task.ID); current.Status != TaskSucceeded {
		t.Fatalf("task status = %s", current.Status)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.Tasks = append(state.Tasks, Task{ID: "task-interrupted", Kind: "start", Status: TaskRunning})
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	RecoverTasks(&loaded)
	for _, item := range loaded.Tasks {
		if item.ID == "task-interrupted" && item.Status != TaskFailed {
			t.Fatal("interrupted task was not failed")
		}
	}
}

func TestClientMultipartUploadAndDownload(t *testing.T) {
	manager, runtime, _ := testManager(t)
	server := newPluginServer(t, testManifest(8080), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			_, _ = fmt.Fprintf(w, `{"name":%q,"size":%d}`, header.Filename, len(data))
		case "/download":
			_, _ = io.WriteString(w, "file-content")
		}
	})
	defer server.Close()
	runtime.address = server.URL
	image := importTestImage(t, manager, runtime)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID, Start: true}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}
	_, err := manager.Client().Do(context.Background(), "demo", Request{
		Method: http.MethodPost, Path: "/upload",
		Multipart: &MultipartRequest{
			Fields: []MultipartField{{Name: "kind", Value: "test"}},
			Files:  []MultipartFile{{Field: "file", Filename: "a.txt", Reader: strings.NewReader("hello")}},
		},
	}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "a.txt" || result.Size != 5 {
		t.Fatalf("upload result = %+v", result)
	}
	body, err := manager.Client().Download(context.Background(), "demo", "/download")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "file-content" {
		t.Fatalf("download = %q, %v", data, err)
	}
}

func TestFriendlyRuntimeErrorShowsDiagnosticDetail(t *testing.T) {
	detail := fmt.Errorf("%w: 未找到可用的 Docker 或 Podman API 端点（已尝试：/run/podman/podman.sock）", ErrRuntimeUnavailable)
	if got := friendlyRuntimeError(detail); !strings.Contains(got, "未找到可用的") {
		t.Fatalf("friendly error = %q", got)
	}
	if got := runtimeErrorDetail(ErrRuntimeUnavailable); got != "" {
		t.Fatalf("sentinel detail = %q", got)
	}
	generic := friendlyRuntimeError(ErrRuntimeUnavailable)
	if !strings.Contains(generic, "Podman") || !strings.Contains(generic, "podman.socket") {
		t.Fatalf("generic error = %q", generic)
	}
}
