//go:build !unix && !windows

package processstats

import (
	"errors"
	"time"
)

var errUnsupported = errors.New("process CPU usage is unavailable on this platform")

func processCPU() (time.Duration, error) { return 0, errUnsupported }
