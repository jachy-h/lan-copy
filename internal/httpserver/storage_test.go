package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folder", "two.txt"), []byte("world!"), 0o644); err != nil {
		t.Fatal(err)
	}
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var usage struct {
		Bytes int64 `json:"bytes"`
		Files int   `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Bytes != 11 || usage.Files != 2 {
		t.Fatalf("usage = %+v, want 11 bytes and 2 files", usage)
	}
}
