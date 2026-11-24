//go:build !darwin

package desktop

import (
	"errors"
	"io"
)

// IsBackgroundChild reports whether this process was started as a detached
// background child. Other platforms never do that, so the flag is not removed.
func IsBackgroundChild() bool { return false }

// ShouldStartBackground reports whether a bare launch should move itself to
// the background. Only macOS provides that behavior.
func ShouldStartBackground() bool { return false }

// StartBackground is only implemented on macOS.
func StartBackground() error { return errors.New("background launch is only supported on macOS") }

// ReadyWriter returns the pipe used by the background parent. Other platforms
// never create one.
func ReadyWriter() io.WriteCloser { return nil }
