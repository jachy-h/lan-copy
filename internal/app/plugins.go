package app

import (
	"fmt"
	"os"

	"lan-copy/internal/plugin"
	"lan-copy/internal/plugin/dockerruntime"
)

// preparePlugins creates the optional plugin manager. Docker being unavailable
// is not an application startup error: the HTTP API reports it and the rest of
// Lan Copy keeps working.
func preparePlugins(dataDir, networkMode, network string) (*plugin.Manager, error) {
	if networkMode != "loopback" && networkMode != "container" {
		return nil, fmt.Errorf("插件网络模式必须为 loopback 或 container")
	}
	box, err := plugin.LoadSecretBox(dataDir, os.Getenv("LAN_COPY_PLUGIN_SECRET_KEY"))
	if err != nil {
		return nil, err
	}
	store, err := plugin.OpenStore(dataDir, box)
	if err != nil {
		return nil, err
	}
	runtime, err := dockerruntime.New(dockerruntime.Options{
		Settings: plugin.RuntimeSettings{
			Type: "docker", NetworkMode: networkMode, Network: network,
		},
	})
	if err != nil {
		return nil, err
	}
	return plugin.NewManager(store, runtime, plugin.Options{
		Runtime: plugin.RuntimeSettings{
			Type: "docker", NetworkMode: networkMode, Network: network,
		},
		StopOnExit: true,
	})
}
