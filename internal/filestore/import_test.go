package filestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveFileUsesSharedPathPolicyAndAvoidsOverwrite(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "相册"), 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := store.SaveFile("相册", "截图.png", []byte("first"))
	if err != nil || name != "相册/截图.png" {
		t.Fatalf("save = %q, %v", name, err)
	}
	name, err = store.SaveFile("相册", "截图.png", []byte("second"))
	if err != nil || name != "相册/截图 (2).png" {
		t.Fatalf("duplicate save = %q, %v", name, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "相册", "截图.png")); err != nil || string(data) != "first" {
		t.Fatalf("original overwritten: %q, %v", data, err)
	}
	if _, err := store.SaveFile("../outside", "bad.txt", []byte("x")); err == nil {
		t.Fatal("expected escaping destination to fail")
	}
	if _, err := store.SaveFile("相册", "../bad.txt", []byte("x")); err == nil {
		t.Fatal("expected escaping name to fail")
	}
	if _, err := store.SaveFile("missing", "file.txt", []byte("x")); !os.IsNotExist(err) {
		t.Fatalf("missing destination error = %v", err)
	}
}
