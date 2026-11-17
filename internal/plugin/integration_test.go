package plugin_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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
