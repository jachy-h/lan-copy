package filestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadResourceConfigPersistsAndValidates(t *testing.T) {
	dir := t.TempDir()
	config, err := LoadResourceConfig(dir)
	if err != nil || config != DefaultResourceConfig {
		t.Fatalf("defaults = %+v, %v", config, err)
	}
	want := ResourceConfig{Concurrent: 2, StagingGiB: 7, IdleSeconds: 45}
	if err := SaveResourceConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	want = ResourceConfig{Concurrent: 4, StagingGiB: 8, IdleSeconds: 30}
	if err := SaveResourceConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadResourceConfig(dir)
	if err != nil || got != want {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ResourceConfigName), []byte(`{"concurrentUploads":0,"stagingGiB":7,"idleTimeoutSeconds":45}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResourceConfig(dir); err == nil {
		t.Fatal("invalid settings accepted")
	}
}

func TestUploadResourceManagerBudgets(t *testing.T) {
	m := NewResources(ResourceConfig{Concurrent: 1, StagingGiB: 1, IdleSeconds: 5})
	if !m.Begin() || m.Begin() {
		t.Fatal("concurrency limit not enforced")
	}
	if !m.Reserve(gib) || m.Reserve(1) {
		t.Fatal("staging limit not enforced")
	}
	m.Release(gib)
	m.End()
	if !m.Begin() {
		t.Fatal("released upload slot was not reusable")
	}
	m.End()
}
