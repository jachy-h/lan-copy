//go:build windows || darwin

package dockerruntime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"lan-copy/internal/plugin"
)

const podmanMachineStartTimeout = 90 * time.Second

func init() {
	platformRuntimeStart = startPodmanMachine
	platformStartAvailable = func() bool {
		return findCommand("podman") != ""
	}
}

// startPodmanMachine brings up the Podman machine that provides the Docker
// compatible API on Windows and macOS.
func startPodmanMachine(ctx context.Context, progress func(int, string)) error {
	podman := findCommand("podman")
	if podman == "" {
		return fmt.Errorf("%w: 未找到 podman 可执行文件", plugin.ErrUnsupported)
	}
	if progress != nil {
		progress(20, "正在启动 Podman machine")
	}
	commandCtx, cancel := context.WithTimeout(ctx, podmanMachineStartTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, podman, "machine", "start").CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return fmt.Errorf("podman machine start 失败: %w", err)
		}
		return fmt.Errorf("podman machine start 失败: %v: %s", err, truncateCommandOutput(detail))
	}
	return nil
}

func truncateCommandOutput(value string) string {
	const max = 2000
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
