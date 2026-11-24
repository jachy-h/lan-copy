//go:build !windows

package dockerruntime

import (
	"fmt"
	"strings"
	"testing"
)

func TestPodmanCandidatesCoverCommonSockets(t *testing.T) {
	joined := fmt.Sprint(podmanCandidates())
	for _, expected := range []string{
		"/run/podman/podman.sock",
		"/var/run/podman/podman.sock",
		"/run/user/",
		"machine/podman.sock",
		"podman/podman.sock",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("podman candidates missing %q: %s", expected, joined)
		}
	}
}
