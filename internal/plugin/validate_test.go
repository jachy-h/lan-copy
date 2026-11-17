package plugin

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultAndConfigValidation(t *testing.T) {
	manifest := testManifest(8080)
	config := DefaultConfig(manifest)
	if err := ValidateConfig(config, manifest); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"cpu low", func(c *Config) { c.CPULimit = 0 }},
		{"cpu high", func(c *Config) { c.CPULimit = 9 }},
		{"memory low", func(c *Config) { c.MemoryMB = 1 }},
		{"pids low", func(c *Config) { c.PIDsLimit = 1 }},
		{"restart always", func(c *Config) { c.RestartPolicy = "always" }},
		{"env name", func(c *Config) { c.Env = []EnvVar{{Name: "1BAD", Value: "x"}} }},
		{"env reserved", func(c *Config) { c.Env = []EnvVar{{Name: "LD_PRELOAD", Value: "x"}} }},
		{"env duplicate", func(c *Config) { c.Env = []EnvVar{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}} }},
		{"health path", func(c *Config) { c.HealthCheck.Path = "health" }},
		{"health interval", func(c *Config) { c.HealthCheck.IntervalSeconds = 1 }},
		{"startup timeout", func(c *Config) { c.HealthCheck.StartupTimeoutSeconds = 1 }},
		{"request timeout", func(c *Config) { c.RequestTimeoutSeconds = 0 }},
		{"log size", func(c *Config) { c.LogMaxSizeMB = 100 }},
		{"mount duplicate", func(c *Config) {
			c.Mounts = []Mount{{HostPath: "/a", ContainerPath: "/data"}, {HostPath: "/b", ContainerPath: "/data"}}
		}},
		{"mount system", func(c *Config) { c.Mounts = []Mount{{HostPath: "/a", ContainerPath: "/etc"}} }},
		{"mount tmp", func(c *Config) { c.Mounts = []Mount{{HostPath: "/a", ContainerPath: "/tmp"}} }},
		{"mount relative", func(c *Config) { c.Mounts = []Mount{{HostPath: "/a", ContainerPath: "data"}} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig(manifest)
			test.mutate(&config)
			if err := ValidateConfig(config, manifest); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestPrepareHostMount(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "demo", "data")
	resolved, err := PrepareHostMount(inside, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved, resolvedRoot) {
		t.Fatalf("resolved path = %s (root %s)", resolved, resolvedRoot)
	}
	outside := filepath.Join(t.TempDir(), "data")
	if _, err := PrepareHostMount(outside, []string{root}); err == nil {
		t.Fatal("path outside allowed roots accepted")
	}
	if _, err := PrepareHostMount(root, []string{root}); err == nil {
		t.Fatal("allowed root itself should not be a mount")
	}
	if _, err := PrepareHostMount(filepath.Join(root, "docker.sock"), []string{root}); err == nil {
		t.Fatal("docker socket accepted")
	}
	linkRoot := t.TempDir()
	if runtime.GOOS != "windows" {
		link := filepath.Join(linkRoot, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareHostMount(filepath.Join(link, "nested"), []string{root}); err == nil {
			t.Fatal("symlink escape accepted")
		}
	}
}

func TestImageFilenameAndArchiveDetection(t *testing.T) {
	for _, name := range []string{"a.tar", "a.tar.gz", "a.tgz", "a.TAR"} {
		if err := ValidateImageFilename(name); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}
	for _, name := range []string{"a.zip", "a.tar.bz2", "a"} {
		if err := ValidateImageFilename(name); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	gzipHeader := []byte{0x1f, 0x8b, 0x08, 0x00}
	if err := DetectImageArchive(bufio.NewReader(bytes.NewReader(gzipHeader))); err != nil {
		t.Fatalf("gzip rejected: %v", err)
	}
	header := make([]byte, 512)
	copy(header[257:], "ustar")
	if err := DetectImageArchive(bufio.NewReader(bytes.NewReader(header))); err != nil {
		t.Fatalf("tar rejected: %v", err)
	}
	if err := DetectImageArchive(bufio.NewReader(strings.NewReader("plain text"))); err == nil {
		t.Fatal("plain text accepted")
	}
}

func TestMergeConfigKeepsSecretOnEmptyUpdate(t *testing.T) {
	base := Config{Env: []EnvVar{{Name: "TOKEN", Value: "old", Secret: true}}}
	merged := MergeConfig(base, Config{Env: []EnvVar{{Name: "TOKEN", Secret: true}}})
	if merged.Env[0].Value != "old" {
		t.Fatalf("secret was cleared: %+v", merged.Env[0])
	}
	merged = MergeConfig(base, Config{Env: []EnvVar{{Name: "TOKEN", Secret: true, Clear: true}}})
	if merged.Env[0].Value != "" {
		t.Fatalf("secret was not cleared: %+v", merged.Env[0])
	}
}
