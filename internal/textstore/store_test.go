package textstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTextStoreWriteFailuresPreserveReadableData(t *testing.T) {
	want := []byte(`[{"id":"0011223344556677","content":"old value","created":"2026-01-01T00:00:00Z"}]`)
	for _, failure := range []string{"write", "file-sync", "rename-old", "rename-new", "directory-sync-old", "directory-sync-new"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, FileName)
			if err := os.WriteFile(destination, want, 0o600); err != nil {
				t.Fatal(err)
			}
			ops := defaultTextStoreOps()
			switch failure {
			case "write":
				ops.write = func(*os.File, []byte) (int, error) { return 0, os.ErrPermission }
			case "file-sync":
				ops.syncFile = func(*os.File) error { return os.ErrPermission }
			case "rename-old", "rename-new":
				base := ops.rename
				calls := 0
				ops.rename = func(old, new string) error {
					calls++
					failAt := 1
					if failure == "rename-new" {
						failAt = 2
					}
					if calls == failAt {
						return os.ErrPermission
					}
					return base(old, new)
				}
			case "directory-sync-old", "directory-sync-new":
				base := ops.syncDir
				calls := 0
				ops.syncDir = func(path string) error {
					calls++
					failAt := 1
					if failure == "directory-sync-new" {
						failAt = 2
					}
					if calls == failAt {
						return os.ErrPermission
					}
					return base(path)
				}
			}
			if err := writeTextsWithOps(dir, []Snippet{{ID: "8899aabbccddeeff", Content: "new value"}}, ops); err == nil {
				t.Fatal("expected injected persistence failure")
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatalf("old store unavailable: %v", err)
			}
			var items []Snippet
			if err := json.Unmarshal(got, &items); err != nil || len(items) != 1 || items[0].Content != "old value" {
				t.Fatalf("old store not restored: %s (%v)", got, err)
			}
		})
	}
}
