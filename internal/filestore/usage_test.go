package filestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsageGroupsFilesByKind(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()
	files := map[string]string{
		"photo.JPG":          "0123456789",
		"clip.mp4":           string(make([]byte, 100)),
		"notes.txt":          string(make([]byte, 20)),
		"README":             "abc",
		"unknown.xyz":        "12345",
		"nested/archive.zip": string(make([]byte, 50)),
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := New(dir, dataDir)
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bytes != 188 || usage.Files != 6 {
		t.Fatalf("usage = %+v", usage)
	}
	want := []TypeUsage{
		{Kind: "video", Bytes: 100, Files: 1},
		{Kind: "archive", Bytes: 50, Files: 1},
		{Kind: "document", Bytes: 20, Files: 1},
		{Kind: "image", Bytes: 10, Files: 1},
		{Kind: "other", Bytes: 8, Files: 2},
	}
	if len(usage.Types) != len(want) {
		t.Fatalf("types = %+v", usage.Types)
	}
	for index, entry := range want {
		if usage.Types[index] != entry {
			t.Fatalf("types[%d] = %+v, want %+v", index, usage.Types[index], entry)
		}
	}
}

func TestStorageKindIsCaseInsensitive(t *testing.T) {
	cases := map[string]string{
		"a.MP4":   "video",
		"a.Heic":  "image",
		"a.CSV":   "document",
		"a.TS":    "code",
		"a.ZST":   "archive",
		"a.WAV":   "audio",
		"a":       "other",
		"a.weird": "other",
	}
	for name, want := range cases {
		if got := storageKind(name); got != want {
			t.Errorf("storageKind(%q) = %q, want %q", name, got, want)
		}
	}
}
