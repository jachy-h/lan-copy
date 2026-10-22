//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func detachUpdater(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func waitUpdateParent(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("等待旧程序退出超时")
}
