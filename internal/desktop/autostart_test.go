package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeBackend struct {
	enabled           bool
	calls             int
	readErr, writeErr error
}

func (f *fakeBackend) Status() (bool, string, error) { return f.enabled, "", f.readErr }
func (f *fakeBackend) Set(v bool) error {
	f.calls++
	if f.writeErr != nil {
		return f.writeErr
	}
	f.enabled = v
	return nil
}

func TestAutostartUnavailable(t *testing.T) {
	for _, a := range []*Autostart{nil, {disabled: "unsupported"}, {backend: &fakeBackend{readErr: errors.New("denied")}}} {
		state := a.State()
		if state.Available || state.Enabled || state.Reason == "" {
			t.Fatalf("unexpected state: %+v", state)
		}
	}
}

func TestStartupSavedArguments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	path, err := startupConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	expected := []string{"--autostart", "--working-dir", filepath.Join(root, "工作 & 目录"), "-dir", filepath.Join(root, "shared files"), "-listen", "127.0.0.1:9876", "-max-gb", "3"}
	data, _ := json.Marshal(StartupSpec{Args: expected})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := StartupLaunchArgs()
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("args=%q err=%v", actual, err)
	}
	for _, data := range []string{`{`, `{}`, `{"Args":["--internal-autostart"]}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := StartupLaunchArgs(); err == nil {
			t.Fatal("invalid startup config accepted")
		}
	}
}
