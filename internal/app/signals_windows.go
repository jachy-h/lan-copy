//go:build windows

package app

import "os"

func ShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
