//go:build windows

package dockerruntime

// defaultCandidates lists local named pipes in preference order. Docker comes
// first so existing installations keep their behavior when both engines run.
func defaultCandidates() []candidate {
	return append([]candidate{
		{host: "npipe:////./pipe/docker_engine", source: `\\.\pipe\docker_engine`},
	}, podmanCandidates()...)
}

func podmanCandidates() []candidate {
	return []candidate{
		{host: "npipe:////./pipe/podman-machine-default", source: `\\.\pipe\podman-machine-default`},
		{host: "npipe:////./pipe/podman-machine-default-root", source: `\\.\pipe\podman-machine-default-root`},
	}
}
