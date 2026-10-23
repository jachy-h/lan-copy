package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalizedStorageName(t *testing.T) {
	for _, language := range []string{"zh", "zh-CN", "zh-Hans-CN", "zh_TW.UTF-8", "ZH_hk"} {
		if got := DefaultStorageName(language); got != "共享文件夹-lancopy" {
			t.Errorf("%s: %s", language, got)
		}
	}
	for _, language := range []string{"en-US", "en_US.UTF-8", "C", "ja-JP", ""} {
		if got := DefaultStorageName(language); got != "shared-folder-lancopy" {
			t.Errorf("%s: %s", language, got)
		}
	}
}

func TestDefaultStorageMigrationPreservesFiles(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "lan-copy-data")
	destination := filepath.Join(root, "共享文件夹-lancopy")
	if err := os.MkdirAll(filepath.Join(legacy, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "nested", "hello.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateStorageDirectory(legacy, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "nested", "hello.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("migration lost file: %q %v", data, err)
	}
	if err := migrateStorageDirectory(legacy, destination); err != nil {
		t.Fatal(err)
	}
	// If both directories exist, never overwrite or merge existing content.
	if err := os.Mkdir(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "second.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrateStorageDirectory(legacy, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "second.txt")); err != nil {
		t.Fatal("existing legacy data was changed")
	}
}
