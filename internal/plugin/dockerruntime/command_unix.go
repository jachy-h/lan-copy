//go:build !windows

package dockerruntime

import (
	"os"
	"path/filepath"
)

// commandDirectories lists locations that GUI-launched processes commonly miss
// on PATH.
func commandDirectories() []string {
	directories := []string{"/usr/bin", "/usr/local/bin", "/opt/homebrew/bin", "/opt/podman/bin", "/usr/libexec/podman"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		directories = append(directories, filepath.Join(home, ".local", "bin"))
	}
	return directories
}

func commandFileNames(name string) []string {
	return []string{name}
}
