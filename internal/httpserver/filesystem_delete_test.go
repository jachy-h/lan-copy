package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNonEmptyFolderDeletionRequiresConfirmation(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "folder", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(app.dir, "folder", "nested", "file.txt")
	if err := os.WriteFile(file, []byte("keep until confirmed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "sibling.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	events, cancel := app.subscribeChanges()
	defer cancel()
	request := func(body string) *httptest.ResponseRecorder {
		result := httptest.NewRecorder()
		app.routes().ServeHTTP(result, httptest.NewRequest("POST", "/api/filesystem/delete", strings.NewReader(body)))
		return result
	}
	result := request(`{"path":"folder"}`)
	var response struct {
		RequiresConfirmation bool `json:"requiresConfirmation"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if result.Code != 409 || !response.RequiresConfirmation {
		t.Fatalf("missing confirmation: %d %s", result.Code, result.Body.String())
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep until confirmed" {
		t.Fatal("unconfirmed deletion changed contents")
	}
	select {
	case <-events:
		t.Fatal("unconfirmed deletion notified a change")
	default:
	}
	result = request(`{"path":"folder","recursive":true}`)
	if result.Code != 204 {
		t.Fatalf("confirmed deletion: %d %s", result.Code, result.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.dir, "folder")); !os.IsNotExist(err) {
		t.Fatal("folder remains after recursive delete")
	}
	if data, err := os.ReadFile(filepath.Join(app.dir, "sibling.txt")); err != nil || string(data) != "keep" {
		t.Fatal("sibling modified")
	}
	select {
	case <-events:
	default:
		t.Fatal("confirmed deletion did not notify browsers")
	}
	for _, body := range []string{`{"path":"","recursive":true}`, `{"path":"../outside","recursive":true}`, `{"path":".lan-copy-texts.json","recursive":true}`} {
		result = request(body)
		if result.Code != 400 {
			t.Fatalf("unsafe recursive delete accepted: %s, %d", body, result.Code)
		}
	}
}

func TestRecursiveDeleteDoesNotFollowSymlinks(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	outside := t.TempDir()
	secret := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(app.dir, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.dir, "folder", "link")); err != nil {
		t.Skip(err)
	}
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, httptest.NewRequest("POST", "/api/filesystem/delete", strings.NewReader(`{"path":"folder/link","recursive":true}`)))
	if result.Code != 400 {
		t.Fatalf("direct symlink deletion accepted: %d", result.Code)
	}
	result = httptest.NewRecorder()
	app.routes().ServeHTTP(result, httptest.NewRequest("POST", "/api/filesystem/delete", strings.NewReader(`{"path":"folder","recursive":true}`)))
	if result.Code != 204 {
		t.Fatalf("recursive removal failed: %d %s", result.Code, result.Body.String())
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "private" {
		t.Fatal("recursive removal followed a symlink outside the shared folder")
	}
}
