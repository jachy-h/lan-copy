//go:build windows

package dockerruntime

import (
	"os"
	"path/filepath"
)

// commandDirectories lists common Podman/Docker install locations. A GUI or
// service process often has a PATH that predates the installer.
func commandDirectories() []string {
	directories := make([]string, 0, 8)
	add := func(value string) {
		if value != "" {
			directories = append(directories, value)
		}
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		add(filepath.Join(programFiles, "RedHat", "Podman"))
		add(filepath.Join(programFiles, "Red Hat", "Podman"))
		add(filepath.Join(programFiles, "Podman"))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		add(filepath.Join(localAppData, "Programs", "Podman"))
		add(filepath.Join(localAppData, "Programs", "Red Hat", "Podman"))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		add(filepath.Join(programData, "chocolatey", "bin"))
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		add(filepath.Join(profile, "scoop", "shims"))
	}
	return directories
}

func commandFileNames(name string) []string {
	return []string{name + ".exe", name + ".cmd", name + ".bat"}
}
