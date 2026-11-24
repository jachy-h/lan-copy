package filestore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestIsHiddenMetadata(t *testing.T) {
	for name, want := range map[string]bool{
		"orders.csv.hidden.metadata.json":      true,
		"data/orders.csv.hidden.metadata.json": true,
		"data/ORDERS.CSV.HIDDEN.METADATA.JSON": true,
		"orders.csv":                           false,
		"hidden.metadata.json":                 false,
		"orders.hidden.metadata.json.bak":      false,
		"notes.txt":                            false,
	} {
		if got := IsHiddenMetadata(name); got != want {
			t.Errorf("IsHiddenMetadata(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestListsOmitHiddenMetadata(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, filepath.Join(dir, ".data"))
	for _, name := range []string{"orders.csv", "orders.csv.hidden.metadata.json", "nested/other.csv", "nested/other.csv.hidden.metadata.json"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("id\nA\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.ListDirectory(context.Background(), "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "nested" || entries[1].Name != "orders.csv" {
		t.Fatalf("entries = %+v", entries)
	}
	files, err := store.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	for _, file := range files {
		if IsHiddenMetadata(file.Name) {
			t.Fatalf("hidden metadata listed: %s", file.Name)
		}
	}
}

func TestUserOperationsRejectHiddenMetadataNames(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, filepath.Join(dir, ".data"))
	if _, _, err := ValidateUploadTree([]TreeEntry{{Path: "orders.csv.hidden.metadata.json"}}); err == nil {
		t.Fatal("upload tree accepted reserved name")
	}
	if _, err := store.SaveFile("", "orders.csv.hidden.metadata.json", []byte("x")); err == nil {
		t.Fatal("SaveFile accepted reserved name")
	}
	if _, err := store.MutateDirectory("mkdir", Mutation{Name: "orders.csv.hidden.metadata.json"}, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("mkdir error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orders.csv.hidden.metadata.json"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateDirectory("delete", Mutation{Path: "orders.csv.hidden.metadata.json"}, nil); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("delete error = %v", err)
	}
}
