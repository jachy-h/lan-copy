package dockerruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

const (
	fakeDockerVersion = `{"Platform":{"Name":"Docker Engine - Community"},"Components":[{"Name":"Engine","Version":"29.8.2"}],"Version":"29.8.2","ApiVersion":"1.52","MinAPIVersion":"1.24","Os":"linux","Arch":"arm64"}`
	fakePodmanVersion = `{"Platform":{"Name":"linux/arm64"},"Components":[{"Name":"Podman Engine","Version":"5.2.2"}],"Version":"5.2.2","ApiVersion":"1.41","MinAPIVersion":"1.24","Os":"linux","Arch":"arm64"}`
)

func fakeEngine(t *testing.T, version string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.41")
			w.Header().Set("OSType", "linux")
			_, _ = io.WriteString(w, "OK")
		case strings.HasSuffix(r.URL.Path, "/version"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, version)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func engineHost(t *testing.T, server *httptest.Server) string {
	t.Helper()
	return strings.Replace(server.URL, "http://", "tcp://", 1)
}

func closedPortHost(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return fmt.Sprintf("tcp://127.0.0.1:%d", port)
}

func TestEngineTypeClassifiesDockerAndPodman(t *testing.T) {
	tests := []struct {
		name    string
		version client.ServerVersionResult
		want    string
	}{
		{
			name: "docker platform name",
			version: client.ServerVersionResult{
				Platform: client.PlatformInfo{Name: "Docker Engine - Community"},
			},
			want: "docker",
		},
		{
			name:    "unknown platform",
			version: client.ServerVersionResult{},
			want:    "docker",
		},
		{
			name: "podman component",
			version: client.ServerVersionResult{
				Components: []system.ComponentVersion{{Name: "Podman Engine", Version: "5.2.2"}},
			},
			want: "podman",
		},
		{
			name: "podman platform name",
			version: client.ServerVersionResult{
				Platform: client.PlatformInfo{Name: "Podman"},
			},
			want: "podman",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := engineType(test.version); got != test.want {
				t.Fatalf("engineType = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDetectIdentifiesDockerAndPodman(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantType string
	}{
		{"docker", fakeDockerVersion, "docker"},
		{"podman", fakePodmanVersion, "podman"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := fakeEngine(t, test.version)
			t.Setenv("CONTAINER_HOST", engineHost(t, server))
			t.Setenv("DOCKER_HOST", "")
			detected, err := Detect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if detected.Type != test.wantType {
				t.Fatalf("type = %q, want %q", detected.Type, test.wantType)
			}
			if detected.Host != engineHost(t, server) || detected.Source != "CONTAINER_HOST" {
				t.Fatalf("detected = %+v", detected)
			}
			if detected.APIVersion == "" || detected.Version == "" {
				t.Fatalf("missing version info: %+v", detected)
			}
		})
	}
}

func TestDetectPrefersDockerHostAndDoesNotFallBack(t *testing.T) {
	server := fakeEngine(t, fakePodmanVersion)
	t.Setenv("DOCKER_HOST", closedPortHost(t))
	t.Setenv("CONTAINER_HOST", engineHost(t, server))
	detected, err := Detect(context.Background())
	if err == nil {
		t.Fatalf("expected failure, got %+v", detected)
	}
	if !errors.Is(err, plugin.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v, want ErrRuntimeUnavailable", err)
	}
}

func TestEnvironmentCandidatesUseFirstSetVariable(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	t.Setenv("CONTAINER_HOST", "unix:///tmp/podman.sock")
	candidates := environmentCandidates()
	if len(candidates) != 1 || candidates[0].source != "DOCKER_HOST" {
		t.Fatalf("candidates = %+v", candidates)
	}
	t.Setenv("DOCKER_HOST", "")
	candidates = environmentCandidates()
	if len(candidates) != 1 || candidates[0].source != "CONTAINER_HOST" {
		t.Fatalf("candidates = %+v", candidates)
	}
	t.Setenv("CONTAINER_HOST", "")
	if candidates := environmentCandidates(); len(candidates) != 0 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestDefaultCandidatesStartWithDocker(t *testing.T) {
	candidates := defaultCandidates()
	if len(candidates) == 0 || !strings.Contains(strings.ToLower(candidates[0].source), "docker") {
		t.Fatalf("candidates = %+v", candidates)
	}
	joined := strings.ToLower(fmt.Sprint(candidates))
	if !strings.Contains(joined, "podman") {
		t.Fatalf("podman candidates missing: %+v", candidates)
	}
}

func TestNormalizeType(t *testing.T) {
	if got := normalizeType("Podman"); got != "podman" {
		t.Fatalf("normalizeType = %q", got)
	}
	if got := normalizeType(""); got != "docker" {
		t.Fatalf("normalizeType = %q", got)
	}
}

func TestDetectErrorIsActionable(t *testing.T) {
	auto := &DetectError{
		Tried:       []string{"/var/run/docker.sock", "/run/user/1000/podman/podman.sock"},
		Problems:    []string{"/var/run/docker.sock: no such file"},
		PodmanFound: true,
	}
	message := auto.Error()
	for _, expected := range []string{"Podman", "systemctl --user enable --now podman.socket", "/run/user/1000/podman/podman.sock"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message %q missing %q", message, expected)
		}
	}
	if !errors.Is(auto, plugin.ErrRuntimeUnavailable) {
		t.Fatal("detect error does not unwrap to ErrRuntimeUnavailable")
	}
	if details := auto.Details(); !strings.Contains(details, "no such file") {
		t.Fatalf("details = %q", details)
	}

	explicit := &DetectError{EnvSource: "DOCKER_HOST", Tried: []string{"tcp://127.0.0.1:1"}}
	message = explicit.Error()
	for _, expected := range []string{"DOCKER_HOST", "tcp://127.0.0.1:1", "删除该环境变量"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message %q missing %q", message, expected)
		}
	}

	both := &DetectError{PodmanFound: true, DockerFound: true}
	if message := both.Error(); !strings.Contains(message, "Podman 和 Docker") {
		t.Fatalf("message = %q", message)
	}
}

func TestRuntimeInfoSurfacesDetectionError(t *testing.T) {
	failure := &DetectError{
		Tried:       []string{"/run/podman/podman.sock"},
		PodmanFound: true,
	}
	runtime, err := New(Options{
		Host:        closedPortHost(t),
		Type:        "docker",
		DetectError: failure,
		Settings:    plugin.RuntimeSettings{NetworkMode: "loopback", Network: "plugin-network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Info(context.Background())
	if err == nil {
		t.Fatal("expected unavailable error")
	}
	if !errors.Is(err, plugin.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "Podman") {
		t.Fatalf("error lost detection detail: %v", err)
	}
}

func replaceDetectionSeams(t *testing.T, auto, podman func() []candidate, exists func(string) bool) {
	t.Helper()
	previousAuto, previousPodman, previousExists := autoCandidates, podmanProbeCandidates, commandExists
	autoCandidates, podmanProbeCandidates, commandExists = auto, podman, exists
	t.Cleanup(func() {
		autoCandidates, podmanProbeCandidates, commandExists = previousAuto, previousPodman, previousExists
	})
}

func withPodmanActivator(t *testing.T, activate func(context.Context) error) *int {
	t.Helper()
	count := 0
	previous := podmanSocketActivator
	podmanSocketActivator = func(ctx context.Context) error {
		count++
		return activate(ctx)
	}
	t.Cleanup(func() { podmanSocketActivator = previous })
	return &count
}

func TestDetectActivatesPodmanSocketAndRetries(t *testing.T) {
	server := fakeEngine(t, fakePodmanVersion)
	host := engineHost(t, server)
	replaceDetectionSeams(t,
		func() []candidate { return []candidate{{host: closedPortHost(t), source: "closed"}} },
		func() []candidate { return []candidate{{host: host, source: "fake-podman"}} },
		func(name string) bool { return name == "podman" },
	)
	activations := withPodmanActivator(t, func(context.Context) error { return nil })
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv(DisablePodmanAutostartEnv, "")

	detected, err := Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if detected.Type != "podman" {
		t.Fatalf("type = %q", detected.Type)
	}
	if *activations != 1 {
		t.Fatalf("activations = %d", *activations)
	}
}

func TestDetectAutostartFailureFallsBackWithHint(t *testing.T) {
	replaceDetectionSeams(t,
		func() []candidate { return []candidate{{host: closedPortHost(t), source: "closed"}} },
		func() []candidate { return nil },
		func(name string) bool { return name == "podman" },
	)
	activations := withPodmanActivator(t, func(context.Context) error {
		return fmt.Errorf("unit podman.socket not found")
	})
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv(DisablePodmanAutostartEnv, "")

	_, err := Detect(context.Background())
	if err == nil {
		t.Fatal("expected detection failure")
	}
	if !errors.Is(err, plugin.ErrRuntimeUnavailable) {
		t.Fatalf("error = %v", err)
	}
	var failure *DetectError
	if !errors.As(err, &failure) || !failure.AutostartTried {
		t.Fatalf("failure = %+v", failure)
	}
	if !strings.Contains(err.Error(), "已尝试自动启动 podman.socket 但未成功") {
		t.Fatalf("message = %q", err.Error())
	}
	if !strings.Contains(failure.Details(), "unit podman.socket not found") {
		t.Fatalf("details = %q", failure.Details())
	}
	if *activations != 1 {
		t.Fatalf("activations = %d", *activations)
	}
}

func TestDetectSkipsAutostartForExplicitEndpoint(t *testing.T) {
	activations := withPodmanActivator(t, func(context.Context) error { return nil })
	replaceDetectionSeams(t,
		func() []candidate { return nil },
		func() []candidate { return nil },
		func(name string) bool { return name == "podman" },
	)
	t.Setenv("DOCKER_HOST", closedPortHost(t))
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv(DisablePodmanAutostartEnv, "")

	if _, err := Detect(context.Background()); err == nil {
		t.Fatal("expected detection failure")
	}
	if *activations != 0 {
		t.Fatalf("activations = %d", *activations)
	}
}

func TestDetectSkipsAutostartWhenDisabled(t *testing.T) {
	activations := withPodmanActivator(t, func(context.Context) error { return nil })
	replaceDetectionSeams(t,
		func() []candidate { return []candidate{{host: closedPortHost(t), source: "closed"}} },
		func() []candidate { return nil },
		func(name string) bool { return name == "podman" },
	)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv(DisablePodmanAutostartEnv, "1")

	_, err := Detect(context.Background())
	if err == nil {
		t.Fatal("expected detection failure")
	}
	if *activations != 0 {
		t.Fatalf("activations = %d", *activations)
	}
	var failure *DetectError
	if !errors.As(err, &failure) || failure.AutostartTried {
		t.Fatalf("failure = %+v", failure)
	}
}
