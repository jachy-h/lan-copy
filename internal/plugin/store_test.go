package plugin

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	box, err := LoadSecretBox(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir, box)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreEncryptsSecretsAndRoundTrips(t *testing.T) {
	store := testStore(t)
	state := NewState()
	state.Plugins = []Plugin{{ID: "demo", Name: "Demo", Status: StatusStopped}}
	state.Instances = []Instance{{
		ID: "inst-1", PluginID: "demo",
		Config: Config{Env: []EnvVar{
			{Name: "TOKEN", Value: "super-secret-value", Secret: true},
			{Name: "PLAIN", Value: "visible-value"},
		}},
	}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(store.Dir(), stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-value") {
		t.Fatal("secret value was written in plain text")
	}
	if !strings.Contains(string(raw), "enc:v1:") {
		t.Fatal("secret value was not encrypted")
	}
	if !strings.Contains(string(raw), "visible-value") {
		t.Fatal("non-secret value should be readable")
	}
	info, err := os.Stat(filepath.Join(store.Dir(), stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %o", info.Mode().Perm())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Instances[0].Config.Env[0].Value != "super-secret-value" {
		t.Fatalf("secret did not decrypt: %+v", loaded.Instances[0].Config.Env)
	}
}

func TestStoreFallsBackToBackup(t *testing.T) {
	store := testStore(t)
	first := NewState()
	first.Plugins = []Plugin{{ID: "first", Name: "First", Status: StatusNotInstalled}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	second := NewState()
	second.Plugins = []Plugin{{ID: "second", Name: "Second", Status: StatusNotInstalled}}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir(), stateFileName)
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Plugins) != 1 || loaded.Plugins[0].ID != "first" {
		t.Fatalf("backup was not used: %+v", loaded.Plugins)
	}
}

func TestStoreTrimsBoundedCollections(t *testing.T) {
	store := testStore(t)
	state := NewState()
	for index := 0; index < MaxTaskHistory+10; index++ {
		state.Tasks = append(state.Tasks, Task{ID: randomTestID(index), Status: TaskSucceeded, CreatedAt: time.Now()})
	}
	state.HealthRecords["inst"] = make([]HealthRecord, MaxHealthRecords+10)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tasks) != MaxTaskHistory || len(loaded.HealthRecords["inst"]) != MaxHealthRecords {
		t.Fatalf("bounds = %d tasks, %d records", len(loaded.Tasks), len(loaded.HealthRecords["inst"]))
	}
}

func TestRecoverTasksMarksInterrupted(t *testing.T) {
	state := NewState()
	state.Tasks = []Task{
		{ID: "a", Status: TaskRunning},
		{ID: "b", Status: TaskSucceeded},
	}
	if !RecoverTasks(&state) {
		t.Fatal("expected a change")
	}
	if state.Tasks[0].Status != TaskFailed || state.Tasks[0].Error == "" {
		t.Fatalf("task = %+v", state.Tasks[0])
	}
	if state.Tasks[1].Status != TaskSucceeded {
		t.Fatal("completed task was modified")
	}
}

func TestSecretBoxRequiresValidEnvironmentKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSecretBox(dir, "not-base64"); err == nil {
		t.Fatal("invalid environment key accepted")
	}
	if _, err := LoadSecretBox(dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", secretKeyFile)); err != nil {
		t.Fatal("key file was not created")
	}
}

func randomTestID(index int) string {
	return "task-" + strconv.Itoa(index)
}
