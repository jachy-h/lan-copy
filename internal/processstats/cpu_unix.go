//go:build unix

package processstats

import (
	"syscall"
	"time"
)

func processCPU() (time.Duration, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return time.Duration(usage.Utime.Sec)*time.Second +
		time.Duration(usage.Utime.Usec)*time.Microsecond +
		time.Duration(usage.Stime.Sec)*time.Second +
		time.Duration(usage.Stime.Usec)*time.Microsecond, nil
}
