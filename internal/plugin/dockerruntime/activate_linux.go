//go:build linux

package dockerruntime

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const podmanActivateTimeout = 3 * time.Second

func init() {
	podmanSocketActivator = activatePodmanSocket
	platformRuntimeStart = func(ctx context.Context, progress func(int, string)) error {
		if progress != nil {
			progress(20, "正在启动 podman.socket")
		}
		return activatePodmanSocket(ctx)
	}
	platformStartAvailable = func() bool {
		return commandExists("podman") && findCommand("systemctl") != ""
	}
}

// activatePodmanSocket starts the Podman Docker-compatible API unit for the
// current identity. It deliberately does not enable the unit: start is a
// runtime action, enable would persist across reboots and change system state
// without the user asking for it.
func activatePodmanSocket(ctx context.Context) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("未找到 systemctl: %w", err)
	}
	args := []string{"--user", "start", "podman.socket"}
	scope := "用户级"
	if os.Geteuid() == 0 {
		args = []string{"start", "podman.socket"}
		scope = "系统级"
	}
	commandCtx, cancel := context.WithTimeout(ctx, podmanActivateTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, "systemctl", args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, detail)
	}
	log.Printf("已执行 Podman API socket 启动命令（%s systemd），正在重新检测", scope)
	return nil
}
