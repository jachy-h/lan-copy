package plugin_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
	"lan-copy/internal/plugin/dockerruntime"
)

// Run with a real Docker daemon:
//
//	LAN_COPY_DOCKER_TEST=1 \
//	LAN_COPY_PLUGIN_TEST_IMAGE=/tmp/lan-copy-demo-plugin.tar.gz \
//	go test ./internal/plugin -run TestDockerIntegration -v
func TestDockerIntegration(t *testing.T) {
	if os.Getenv("LAN_COPY_DOCKER_TEST") != "1" {
		t.Skip("set LAN_COPY_DOCKER_TEST=1 to run Docker integration tests")
	}
	archive := os.Getenv("LAN_COPY_PLUGIN_TEST_IMAGE")
	if archive == "" {
		t.Skip("set LAN_COPY_PLUGIN_TEST_IMAGE to a .tar or .tar.gz plugin image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	dataDir := t.TempDir()
	network := fmt.Sprintf("lan-copy-it-%d", time.Now().UnixNano())
	box, err := plugin.LoadSecretBox(dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := plugin.OpenStore(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := dockerruntime.New(dockerruntime.Options{
		Settings: plugin.RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: network},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := plugin.NewManager(store, runtime, plugin.Options{
		Runtime:    plugin.RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: network},
		StopOnExit: true, Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		manager.Shutdown(cleanupCtx)
		cli, clientErr := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
		if clientErr == nil {
			_, _ = cli.NetworkRemove(cleanupCtx, network, client.NetworkRemoveOptions{})
		}
	})

	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := manager.StageImage(filepath.Base(archive), file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	image, err := manager.ImportStaged(ctx, staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Install(ctx, plugin.InstallRequest{
		ImageID: image.ID,
		Config:  plugin.Config{Env: []plugin.EnvVar{{Name: "DEMO_SECRET", Value: "integration-secret", Secret: true}}},
		Start:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Instance.Status != plugin.StatusRunning {
		t.Fatalf("status = %s", result.Instance.Status)
	}
	var response struct {
		Message string `json:"message"`
		Secret  bool   `json:"secret"`
	}
	if _, err := manager.Client().Do(ctx, image.PluginID, plugin.Request{
		Method: "POST", Path: "/echo", JSON: map[string]string{"hello": "world"},
	}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "pong" || !response.Secret {
		t.Fatalf("client response = %+v", response)
	}
	if err := manager.Stop(ctx, image.PluginID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(ctx, image.PluginID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(ctx, image.PluginID, plugin.UninstallRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Plugins()) != 1 || len(manager.Images()) != 1 {
		t.Fatal("uninstall did not preserve the plugin metadata and image record")
	}
	if err := manager.RemoveImage(ctx, image.ID); err != nil {
		t.Fatal(err)
	}
	if len(manager.Plugins()) != 0 || len(manager.Images()) != 0 {
		t.Fatal("removing the last image should remove the plugin metadata")
	}
}

// TestDockerPluginAPIAccess verifies that a real plugin container can reach
// the injected LAN_COPY_API_URL. Build the image from scripts/plugin-example
// (it must include GET /api-check):
//
//	LAN_COPY_DOCKER_TEST=1 \
//	LAN_COPY_PLUGIN_TEST_IMAGE=/tmp/lan-copy-demo-plugin.tar.gz \
//	go test ./internal/plugin -run TestDockerPluginAPIAccess -v
func TestDockerPluginAPIAccess(t *testing.T) {
	if os.Getenv("LAN_COPY_DOCKER_TEST") != "1" {
		t.Skip("set LAN_COPY_DOCKER_TEST=1 to run Docker integration tests")
	}
	archive := os.Getenv("LAN_COPY_PLUGIN_TEST_IMAGE")
	if archive == "" {
		t.Skip("set LAN_COPY_PLUGIN_TEST_IMAGE to a .tar or .tar.gz plugin image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Bind the fake host API on all IPv4 interfaces so host.docker.internal and
	// a Podman machine LAN address can both reach it.
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	apiServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = apiServer.Serve(listener)
	}()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = apiServer.Shutdown(shutdownCtx)
		<-serverDone
	})
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	dataDir := t.TempDir()
	network := fmt.Sprintf("lan-copy-it-api-%d", time.Now().UnixNano())
	box, err := plugin.LoadSecretBox(dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := plugin.OpenStore(dataDir, box)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := dockerruntime.New(dockerruntime.Options{
		Settings: plugin.RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: network},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := plugin.NewManager(store, runtime, plugin.Options{
		Runtime:    plugin.RuntimeSettings{Type: "docker", NetworkMode: "loopback", Network: network},
		StopOnExit: true, Logger: log.New(io.Discard, "", 0), Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		manager.Shutdown(cleanupCtx)
		cli, clientErr := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
		if clientErr == nil {
			_, _ = cli.NetworkRemove(cleanupCtx, network, client.NetworkRemoveOptions{})
		}
	})

	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := manager.StageImage(filepath.Base(archive), file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	image, err := manager.ImportStaged(ctx, staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(ctx, plugin.InstallRequest{ImageID: image.ID, Start: true}); err != nil {
		t.Fatal(err)
	}
	var check struct {
		URL       string `json:"url"`
		Reachable bool   `json:"reachable"`
		Status    int    `json:"status"`
		Error     string `json:"error"`
	}
	if _, err := manager.Client().Do(ctx, image.PluginID, plugin.Request{
		Method: http.MethodGet, Path: "/api-check",
	}, &check); err != nil {
		t.Fatalf("plugin /api-check failed (rebuild the test image from scripts/plugin-example): %v", err)
	}
	if check.URL == "" {
		t.Fatal("LAN_COPY_API_URL was not injected")
	}
	if !check.Reachable {
		t.Fatalf("plugin could not reach injected API %s: status=%d error=%s", check.URL, check.Status, check.Error)
	}
	if err := manager.Uninstall(ctx, image.PluginID, plugin.UninstallRequest{}); err != nil {
		t.Fatal(err)
	}
}
