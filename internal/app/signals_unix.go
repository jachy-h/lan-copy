//go:build !windows

package app

import (
	"os"
	"syscall"
)

func ShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
