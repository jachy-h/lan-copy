package desktop

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsStartupCommand(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	spec := StartupSpec{`C:\Program Files\Lan Copy\lan-copy.exe`, `C:\工作目录`, []string{"--autostart", "--working-dir", `C:\工作目录`, "-dir", `C:\` + strings.Repeat("共享目录", 100)}}
	backend, err := newStartupBackend(spec)
	if err != nil {
		t.Fatal(err)
	}
	windows := backend.(*windowsStartup)
	if windows.command != syscall.EscapeArg(spec.Executable)+" --internal-autostart" {
		t.Fatalf("unsafe command: %s", windows.command)
	}
	if strings.Contains(windows.command, "共享目录") {
		t.Fatal("long arguments must not be stored in Run command")
	}
	if !filepath.IsAbs(windows.configPath) {
		t.Fatal("startup config must use absolute path")
	}
}
