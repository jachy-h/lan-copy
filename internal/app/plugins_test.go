package app

import "testing"

func TestRuntimeDisplayName(t *testing.T) {
	if got := runtimeDisplayName("podman"); got != "Podman" {
		t.Fatalf("podman name = %q", got)
	}
	if got := runtimeDisplayName("docker"); got != "Docker" {
		t.Fatalf("docker name = %q", got)
	}
	if got := runtimeDisplayName(""); got != "Docker" {
		t.Fatalf("default name = %q", got)
	}
}
