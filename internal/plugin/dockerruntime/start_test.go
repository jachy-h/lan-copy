package dockerruntime

import (
	"context"
	"errors"
	"testing"

	"lan-copy/internal/plugin"
)

func withRuntimeStart(t *testing.T, start func(context.Context, func(int, string)) error, available func() bool) {
	t.Helper()
	previousStart, previousAvailable := platformRuntimeStart, platformStartAvailable
	platformRuntimeStart, platformStartAvailable = start, available
	t.Cleanup(func() {
		platformRuntimeStart, platformStartAvailable = previousStart, previousAvailable
	})
}

func TestStartRuntimeBringsPodmanUp(t *testing.T) {
	server := fakeEngine(t, fakePodmanVersion)
	host := engineHost(t, server)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	previousCandidates := podmanProbeCandidates
	podmanProbeCandidates = func() []candidate {
		return []candidate{{host: host, source: "fake-podman"}}
	}
	t.Cleanup(func() { podmanProbeCandidates = previousCandidates })

	started := 0
	withRuntimeStart(t, func(context.Context, func(int, string)) error {
		started++
		return nil
	}, func() bool { return true })

	runtime, err := New(Options{
		Host: closedPortHost(t), Type: "docker",
		Settings: plugin.RuntimeSettings{NetworkMode: "loopback", Network: "plugin-network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := runtime.Info(context.Background())
	if err == nil || !info.StartAvailable {
		t.Fatalf("before start: info=%+v err=%v", info, err)
	}
	if err := runtime.StartRuntime(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("start calls = %d", started)
	}
	info, err = runtime.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Type != "podman" {
		t.Fatalf("after start: %+v", info)
	}
	if info.StartAvailable {
		t.Fatal("start should not be offered when the runtime is available")
	}
}

func TestStartRuntimeRejectsAvailableRuntime(t *testing.T) {
	server := fakeEngine(t, fakeDockerVersion)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	withRuntimeStart(t, func(context.Context, func(int, string)) error {
		t.Fatal("start must not run while the runtime is available")
		return nil
	}, func() bool { return true })

	runtime, err := New(Options{
		Host: engineHost(t, server), Type: "docker",
		Settings: plugin.RuntimeSettings{NetworkMode: "loopback", Network: "plugin-network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartRuntime(context.Background(), nil); !errors.Is(err, plugin.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestStartRuntimeUnsupportedWithoutPodman(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	withRuntimeStart(t, nil, func() bool { return false })

	runtime, err := New(Options{
		Host: closedPortHost(t), Type: "docker",
		Settings: plugin.RuntimeSettings{NetworkMode: "loopback", Network: "plugin-network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartRuntime(context.Background(), nil); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestStartAvailableHiddenForExplicitEndpoint(t *testing.T) {
	t.Setenv("DOCKER_HOST", closedPortHost(t))
	withRuntimeStart(t, nil, func() bool { return true })

	runtime, err := New(Options{
		Host: closedPortHost(t), Type: "docker",
		Settings: plugin.RuntimeSettings{NetworkMode: "loopback", Network: "plugin-network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := runtime.Info(context.Background())
	if err == nil {
		t.Fatal("expected unavailable")
	}
	if info.StartAvailable {
		t.Fatal("explicit endpoint must not offer a start button")
	}
}
