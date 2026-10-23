//go:build windows

package updater

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func DetachUpdater(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} }
func waitUpdateParent(pid int, timeout time.Duration) error {
	h, err := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if err == syscall.Errno(87) {
		return nil
	} // The parent has already exited.
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	status, err := syscall.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	if status != 0 {
		return errors.New("等待旧程序退出超时")
	}
	return nil
}
