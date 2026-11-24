package app

import (
	"context"
	"fmt"
	"log"
	"os"

	"lan-copy/internal/plugin"
	"lan-copy/internal/plugin/dockerruntime"
)

// preparePlugins creates the optional plugin manager after detecting a local
// Docker or Podman endpoint. A missing runtime is not an application startup
// error: the HTTP API reports it and the rest of Lan Copy keeps working.
func preparePlugins(dataDir, port, networkMode, network, apiBaseURL string) (*plugin.Manager, error) {
	if networkMode != "loopback" && networkMode != "container" {
		return nil, fmt.Errorf("插件网络模式必须为 loopback 或 container")
	}
	detected, detectErr := dockerruntime.Detect(context.Background())
	if detectErr != nil {
		log.Printf("未检测到可用的容器运行时: %v", detectErr)
		if detailed, ok := detectErr.(interface{ Details() string }); ok {
			if details := detailed.Details(); details != "" {
				log.Printf("运行时探测详情: %s", details)
			}
		}
	}
	runtimeType := detected.Type
	if runtimeType == "" {
		runtimeType = "docker"
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
		Host: detected.Host, Type: runtimeType, DetectError: detectErr,
		Settings: plugin.RuntimeSettings{
			Type: runtimeType, NetworkMode: networkMode, Network: network,
		},
	})
	if err != nil {
		return nil, err
	}
	if detectErr == nil {
		log.Printf("插件运行时: %s %s（%s）", runtimeDisplayName(runtimeType), detected.Version, detected.Source)
	}
	return plugin.NewManager(store, runtime, plugin.Options{
		Runtime: plugin.RuntimeSettings{
			Type: runtimeType, NetworkMode: networkMode, Network: network,
		},
		StopOnExit: true,
		APIBaseURL: apiBaseURL,
		Port:       port,
	})
}

func runtimeDisplayName(runtimeType string) string {
	if runtimeType == "podman" {
		return "Podman"
	}
	return "Docker"
}
