package tablestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lan-copy/internal/filestore"
)

func marksStoreForTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".app-data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := New(filestore.New(dir, dataDir))
	if err := store.LoadMarks(dataDir); err != nil {
		t.Fatal(err)
	}
	return store, dir
}

func writeMarkTestCSV(t *testing.T, dir, name string, contents string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMarksPersistAndToggle(t *testing.T) {
	store, dir := marksStoreForTest(t)
	writeMarkTestCSV(t, dir, "data/orders.csv", "id,status\nA,new\n")
	if store.IsMarked("data/orders.csv") {
		t.Fatal("new store already marks a file")
	}
	if err := store.SetMark(context.Background(), "data/orders.csv", true); err != nil {
		t.Fatal(err)
	}
	if !store.IsMarked("data/orders.csv") {
		t.Fatal("mark not set")
	}
	reloaded := New(filestore.New(dir, filepath.Join(dir, ".app-data")))
	if err := reloaded.LoadMarks(filepath.Join(dir, ".app-data")); err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsMarked("data/orders.csv") {
		t.Fatal("mark did not persist")
	}
	if err := reloaded.SetMark(context.Background(), "data/orders.csv", false); err != nil {
		t.Fatal(err)
	}
	again := New(filestore.New(dir, filepath.Join(dir, ".app-data")))
	if err := again.LoadMarks(filepath.Join(dir, ".app-data")); err != nil {
		t.Fatal(err)
	}
	if again.IsMarked("data/orders.csv") {
		t.Fatal("mark did not persist removal")
	}
}

func TestMarksRejectNonTables(t *testing.T) {
	store, dir := marksStoreForTest(t)
	writeMarkTestCSV(t, dir, "notes.txt", "hello")
	writeMarkTestCSV(t, dir, "broken.csv", "id,name\n1\n")
	for _, name := range []string{"data/missing.csv", "notes.txt", "broken.csv", "../outside.csv"} {
		if err := store.SetMark(context.Background(), name, true); err == nil {
			t.Fatalf("SetMark(%q) accepted an invalid table", name)
		}
		if store.IsMarked(name) {
			t.Fatalf("SetMark(%q) left a mark", name)
		}
	}
}

func TestMarksFollowRenameMoveAndDelete(t *testing.T) {
	store, dir := marksStoreForTest(t)
	writeMarkTestCSV(t, dir, "data/orders.csv", "id,status\nA,new\n")
	writeMarkTestCSV(t, dir, "data/inbox.csv", "id,status\nB,new\n")
	writeMarkTestCSV(t, dir, "data/archive/old.csv", "id,status\nC,new\n")
	for _, name := range []string{"data/orders.csv", "data/inbox.csv", "data/archive/old.csv"} {
		if err := store.SetMark(context.Background(), name, true); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.RelocateMarks("data/orders.csv", "data/sales.csv", false); err != nil {
		t.Fatal(err)
	}
	if store.IsMarked("data/orders.csv") || !store.IsMarked("data/sales.csv") {
		t.Fatal("file mark did not follow rename")
	}
	if err := store.RelocateMarks("data/archive", "history", true); err != nil {
		t.Fatal(err)
	}
	if store.IsMarked("data/archive/old.csv") || !store.IsMarked("history/old.csv") {
		t.Fatal("folder mark did not follow move")
	}
	if err := store.RemoveMarks("history", true); err != nil {
		t.Fatal(err)
	}
	if store.IsMarked("history/old.csv") {
		t.Fatal("folder delete left a mark")
	}
	if err := store.RelocateMarks("data/inbox.csv", "data/inbox.txt", false); err != nil {
		t.Fatal(err)
	}
	if store.IsMarked("data/inbox.csv") || store.IsMarked("data/inbox.txt") {
		t.Fatal("non-CSV relocation kept a mark")
	}
}

func TestLoadMarksRejectsCorruptState(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".app-data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"malformed":   `{"paths":`,
		"unknown":     `{"paths":[],"extra":true}`,
		"invalid ext": `{"paths":["notes.txt"]}`,
		"escaping":    `{"paths":["../outside.csv"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dataDir, MarksFileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			store := New(filestore.New(dir, dataDir))
			if err := store.LoadMarks(dataDir); err == nil {
				t.Fatal("corrupt marks accepted")
			}
		})
	}
}

func TestSetMarkRequiresLoadedState(t *testing.T) {
	dir := t.TempDir()
	store := New(filestore.New(dir, filepath.Join(dir, ".app-data")))
	err := store.SetMark(context.Background(), "data/orders.csv", true)
	if err == nil {
		t.Fatal("SetMark accepted an unloaded store")
	}
}

func TestMarkDoesNotChangeFileVersion(t *testing.T) {
	store, dir := marksStoreForTest(t)
	writeMarkTestCSV(t, dir, "data/orders.csv", "id,status\nA,new\n")
	before, err := store.CurrentVersion("data/orders.csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMark(context.Background(), "data/orders.csv", true); err != nil {
		t.Fatal(err)
	}
	after, err := store.CurrentVersion("data/orders.csv")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("mark changed version: %s -> %s", before, after)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data", "orders.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "id,status\nA,new\n" {
		t.Fatalf("mark changed file contents: %q", data)
	}
}

func TestLoadMarksMissingFileIsEmpty(t *testing.T) {
	store, _ := marksStoreForTest(t)
	if store.marksPath == "" || len(store.marks) != 0 {
		t.Fatalf("empty marks state = path %q, %d entries", store.marksPath, len(store.marks))
	}
	if err := store.SetMark(context.Background(), "", true); !errors.Is(err, ErrNotCSV) {
		t.Fatalf("empty path error = %v", err)
	}
}
