//go:build darwin

package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackgroundEligible(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		terminal bool
		childEnv string
		want     bool
	}{
		{"homebrew command in terminal", []string{"/opt/homebrew/bin/lancopy"}, true, "", true},
		{"bare command from PATH", []string{"lancopy"}, true, "", true},
		{"release binary keeps foreground", []string{"/tmp/lan-copy"}, true, "", false},
		{"go run temp binary keeps foreground", []string{"/var/folders/x/exe/lan-copy"}, true, "", false},
		{"explicit flags keep foreground", []string{"lancopy", "-dir", "share"}, true, "", false},
		{"not a terminal", []string{"lancopy"}, false, "", false},
		{"background child does not detach again", []string{"lancopy"}, true, "3", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := backgroundEligible(testCase.args, testCase.terminal, testCase.childEnv); got != testCase.want {
				t.Fatalf("backgroundEligible(%v, %v, %q) = %v, want %v",
					testCase.args, testCase.terminal, testCase.childEnv, got, testCase.want)
			}
		})
	}
}

func TestBackgroundLogPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(backgroundLogEnv, "")

	got, err := BackgroundLogPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "Logs", "lancopy", "lancopy.log")
	if got != want {
		t.Fatalf("BackgroundLogPath() = %q, want %q", got, want)
	}

	override := filepath.Join(t.TempDir(), "custom.log")
	t.Setenv(backgroundLogEnv, override)
	got, err = BackgroundLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Fatalf("BackgroundLogPath() = %q, want override %q", got, override)
	}
}

func TestRotateLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lancopy.log")

	if err := rotateLog(path, 4); err != nil {
		t.Fatalf("rotateLog missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rotateLog(path, 10); err != nil {
		t.Fatalf("rotateLog below limit: %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("log rotated below the limit: %v", err)
	}
	if err := rotateLog(path, 4); err != nil {
		t.Fatalf("rotateLog at limit: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("log was not rotated: %v", err)
	}
	data, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1234" {
		t.Fatalf("rotated content = %q", data)
	}
}

func TestStartBackgroundStreamsChildOutput(t *testing.T) {
	dir := t.TempDir()
	script := writeChildScript(t, dir, "printf 'line one\\n' >&3\nprintf 'line two\\n' >&3\nexec 3>&-\nexec sleep 30\n")
	logPath := filepath.Join(dir, "lancopy.log")

	command, lines, err := startBackground(script, logPath, 5*time.Second)
	if err != nil {
		t.Fatalf("startBackground() error = %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	if want := []string{"line one", "line two"}; !slices.Equal(lines, want) {
		t.Fatalf("startup lines = %v, want %v", lines, want)
	}
	if err := command.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("child exited after startup: %v", err)
	}
}

func TestStartBackgroundReportsChildFailure(t *testing.T) {
	dir := t.TempDir()
	script := writeChildScript(t, dir, "echo 'bind: address already in use' >&2\nexit 1\n")
	logPath := filepath.Join(dir, "lancopy.log")

	command, lines, err := startBackground(script, logPath, 5*time.Second)
	if err == nil {
		t.Fatal("startBackground() succeeded, want failure")
	}
	if lines != nil {
		t.Fatalf("startup lines = %v, want none", lines)
	}
	if !strings.Contains(err.Error(), "bind: address already in use") {
		t.Fatalf("error %q does not contain the child log tail", err)
	}
	if command == nil || command.ProcessState == nil || !command.ProcessState.Exited() {
		t.Fatalf("failed child was not reaped: %+v", command)
	}
}

func TestStartBackgroundTimesOutWithoutReadyPipe(t *testing.T) {
	dir := t.TempDir()
	script := writeChildScript(t, dir, "exec sleep 30\n")
	logPath := filepath.Join(dir, "lancopy.log")

	started := time.Now()
	command, lines, err := startBackground(script, logPath, 300*time.Millisecond)
	if err == nil {
		t.Fatal("startBackground() succeeded, want timeout")
	}
	if lines != nil {
		t.Fatalf("startup lines = %v, want none", lines)
	}
	if !strings.Contains(err.Error(), "未在") {
		t.Fatalf("error %q does not describe the timeout", err)
	}
	if command == nil || command.ProcessState == nil {
		t.Fatalf("timed out child was not terminated: %+v", command)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestSplitReadyLines(t *testing.T) {
	got := splitReadyLines("first\r\n\nsecond\n")
	want := []string{"first", "second"}
	if !slices.Equal(got, want) {
		t.Fatalf("splitReadyLines() = %v, want %v", got, want)
	}
}

func TestReadyWriterForRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "abc", "0", "2", "-1"} {
		if writer := readyWriterFor(value); writer != nil {
			writer.Close()
			t.Fatalf("readyWriterFor(%q) returned a writer", value)
		}
	}
}

func writeChildScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "child.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
