package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotUpdateAndCreateFile(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, filepath.Join(dir, ".data"))
	snapshot, err := store.CreateFile("table.csv", []byte("id\nA\n"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != Version([]byte("id\nA\n")) {
		t.Fatalf("version = %q", snapshot.Version)
	}
	if _, err := store.CreateFile("table.csv", []byte("id\nB\n")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("duplicate create error = %v", err)
	}
	updated, err := store.UpdateFile("table.csv", 1024, func(data []byte) ([]byte, error) {
		return append(data, "B\n"...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version == snapshot.Version {
		t.Fatal("version did not change")
	}
	read, err := store.SnapshotFile("table.csv", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Data) != "id\nA\nB\n" {
		t.Fatalf("data = %q", read.Data)
	}
	if _, err := store.UpdateFile("table.csv", 1024, func([]byte) ([]byte, error) {
		return nil, errors.New("cancel")
	}); err == nil {
		t.Fatal("update error was ignored")
	}
	read, err = store.SnapshotFile("table.csv", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Data) != "id\nA\nB\n" {
		t.Fatalf("failed update changed data: %q", read.Data)
	}
	if _, err := store.SnapshotFile("table.csv", 2); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("size limit error = %v", err)
	}
	for _, name := range []string{"../table.csv", "/table.csv", "a\\b.csv"} {
		if _, err := store.CreateFile(name, []byte("x")); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestWriteFileCreatesAndAtomicallyReplaces(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, filepath.Join(dir, ".data"))
	created, err := store.WriteFile("table.csv.hidden.metadata.json", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != Version([]byte("first")) {
		t.Fatalf("version = %q", created.Version)
	}
	updated, err := store.WriteFile("table.csv.hidden.metadata.json", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != Version([]byte("second")) {
		t.Fatalf("version = %q", updated.Version)
	}
	read, err := store.SnapshotFile("table.csv.hidden.metadata.json", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(read.Data) != "second" {
		t.Fatalf("data = %q", read.Data)
	}
	if _, err := store.WriteFile("../escape.json", []byte("x")); err == nil {
		t.Fatal("traversal accepted")
	}
	if err := os.Symlink(filepath.Join(dir, "target.json"), filepath.Join(dir, "link.json")); err == nil {
		if _, err := store.WriteFile("link.json", []byte("x")); err == nil {
			t.Fatal("symlink target accepted")
		}
	}
}
