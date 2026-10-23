package filestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lan-copy/internal/textstore"
)

func TestListingsDoNotHoldMutationLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	store := New(dir, t.TempDir())
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, err := store.ListFiles(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListDirectory(context.Background(), "", "", 100); err != nil {
		t.Fatal(err)
	}
}

func TestTextStoreDoesNotWaitForFileMutationLock(t *testing.T) {
	store := New(t.TempDir(), t.TempDir())
	texts := textstore.New(t.TempDir())
	store.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := texts.Create("independent"); done <- err }()
	select {
	case err := <-done:
		store.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		store.mu.Unlock()
		t.Fatal("text storage waited for file mutation lock")
	}
}
