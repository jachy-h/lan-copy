package updater

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCleanupHistoryRetentionAndProtection(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "lan-copy")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	buildTestUpdater(t, executable)
	updaterName := "updater"
	if runtime.GOOS == "windows" {
		updaterName += ".exe"
	}

	create := func(name, version, id string, completed time.Time, extras ...string) string {
		t.Helper()
		dir := filepath.Join(root, ".lan-copy-update-"+name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := copyFile(executable, filepath.Join(dir, updaterName)); err != nil {
			t.Fatal(err)
		}
		if version != "" {
			record, _ := json.Marshal(historyRecord{Target: executable, ID: id, Version: version, Completed: completed})
			if err := os.WriteFile(filepath.Join(dir, historyFile), record, 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, extra := range extras {
			if err := os.WriteFile(filepath.Join(dir, extra), []byte("active"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	old := create("old", "1.0", "old", time.Now().Add(-4*time.Hour))
	middle := create("middle", "2.0", "mid", time.Now().Add(-3*time.Hour))
	newest := create("new", "3.0", "new", time.Now().Add(-time.Hour))
	duplicate := create("duplicate", "2.0", "mid-newer", time.Now().Add(-2*time.Hour))
	active := create("active", "", "", time.Time{}, "plan.json")
	legacy := filepath.Join(root, ".lan-copy-update-legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(executable, filepath.Join(legacy, updaterName)); err != nil {
		t.Fatal(err)
	}

	var reports []string
	if err := CleanupHistory(executable, func(message string) { reports = append(reports, message) }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{old, middle, legacy} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected directory removed: %s (err %v)", path, err)
		}
	}
	for _, path := range []string{duplicate, newest, active} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected directory kept: %s (err %v)", path, err)
		}
	}
	if len(reports) == 0 {
		t.Fatal("expected active directory to be reported")
	}
}

func TestCleanupHistoryIgnoresUntrustedDirectories(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "lan-copy")
	if err := os.WriteFile(executable, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".lan-copy-update-untrusted")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "updater"), []byte("not a program"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupHistory(executable, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("untrusted directory was removed: %v", err)
	}
}

func buildTestUpdater(t *testing.T, destination string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate updater test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("go", "build", "-o", destination, ".")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build updater test binary: %v\n%s", err, output)
	}
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if runtime.GOOS != "windows" {
		mode = 0700
	}
	return os.WriteFile(destination, data, mode)
}
