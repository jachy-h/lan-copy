//go:build !windows

package dockerruntime

import (
	"os"
	"path/filepath"
	"strconv"
)

// defaultCandidates lists local endpoints in preference order. Docker comes
// first so existing installations keep their behavior when both engines run.
func defaultCandidates() []candidate {
	candidates := []candidate{
		{host: "unix:///var/run/docker.sock", source: "/var/run/docker.sock"},
		{host: "unix:///run/docker.sock", source: "/run/docker.sock"},
	}
	return append(candidates, podmanCandidates()...)
}

func podmanCandidates() []candidate {
	candidates := make([]candidate, 0, 10)
	seen := make(map[string]struct{}, 10)
	add := func(path string) {
		if path == "" {
			return
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		candidates = append(candidates, candidate{host: "unix://" + cleaned, source: cleaned})
	}
	addGlob := func(pattern string) {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			add(match)
		}
	}
	// Rootful Podman; /var/run/podman is a legacy alias kept by some distros.
	add("/run/podman/podman.sock")
	add("/var/run/podman/podman.sock")
	// Rootless Podman for the current user.
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		add(filepath.Join(runtimeDir, "podman", "podman.sock"))
	}
	add(filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "podman", "podman.sock"))
	// When Lan Copy runs as root, rootless Podman sockets of login users are
	// still reachable. Without this a system service would never find them.
	if os.Geteuid() == 0 {
		addGlob("/run/user/*/podman/podman.sock")
	}
	// Podman machine: the API socket is usually forwarded under the machine
	// directory, but some versions expose it through TMPDIR instead.
	if tempDir := os.TempDir(); tempDir != "" {
		add(filepath.Join(tempDir, "podman", "podman.sock"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		machineDir := filepath.Join(home, ".local", "share", "containers", "podman", "machine")
		add(filepath.Join(machineDir, "podman.sock"))
		addGlob(filepath.Join(machineDir, "*", "podman.sock"))
	}
	return candidates
}
