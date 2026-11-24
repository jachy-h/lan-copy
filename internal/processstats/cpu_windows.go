//go:build windows

package processstats

import (
	"syscall"
	"time"
)

func processCPU() (time.Duration, error) {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return time.Duration(kernel.Nanoseconds() + user.Nanoseconds()), nil
}
