package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTextLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", "电脑上的一段文字\nhttps://example.com"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created textSnippet
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !validTextID(created.ID) || created.Content != "电脑上的一段文字\nhttps://example.com" {
		t.Fatalf("unexpected text: %+v", created)
	}

	restarted, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts", nil))
	var listed struct {
		Texts []textSnippet `json:"texts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Texts) != 1 || listed.Texts[0].ID != created.ID {
		t.Fatalf("unexpected texts after restart: %+v", listed.Texts)
	}

	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var files struct {
		Files []fileInfo `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 0 {
		t.Fatalf("text storage leaked into files: %+v", files.Files)
	}

	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/"+created.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestTextStorageDoesNotWaitForFileOperationLock(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	app.fileMu.Lock()
	result := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		app.routes().ServeHTTP(result, textRequest(http.MethodPost, "/api/texts", "independent"))
		close(done)
	}()
	select {
	case <-done:
		app.fileMu.Unlock()
	case <-time.After(time.Second):
		app.fileMu.Unlock()
		t.Fatal("text storage waited for the file operation lock")
	}
	if result.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d: %s", result.Code, http.StatusCreated, result.Body.String())
	}
}

func TestTextStoreRecoversBackupAtStartup(t *testing.T) {
	dir := t.TempDir()
	want := []byte(`[{"id":"0011223344556677","content":"recover me","created":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(filepath.Join(dir, textStoreBackup), want, 0o600); err != nil {
		t.Fatal(err)
	}
	staleTemp := filepath.Join(dir, ".lan-copy-texts-interrupted")
	if err := os.WriteFile(staleTemp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "recover me") {
		t.Fatalf("recovered texts response = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, textStoreBackup)); !os.IsNotExist(err) {
		t.Fatalf("backup remains after recovery: %v", err)
	}
	if _, err := os.Stat(staleTemp); !os.IsNotExist(err) {
		t.Fatalf("interrupted text temp remains after recovery: %v", err)
	}
}

func TestTextStoreCountLimit(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]textSnippet, maxTextCount)
	for i := range items {
		items[i] = textSnippet{ID: strings.Repeat("a", 16), Content: "x"}
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dataDir, textStore), data, 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", "new item"))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create status = %d, want %d: %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestTextStoreWriteFailuresPreserveReadableData(t *testing.T) {
	want := []byte(`[{"id":"0011223344556677","content":"old value","created":"2026-01-01T00:00:00Z"}]`)
	for _, failure := range []string{"write", "file-sync", "rename-old", "rename-new", "directory-sync-old", "directory-sync-new"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, textStore)
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
			if err := writeTextsWithOps(dir, []textSnippet{{ID: "8899aabbccddeeff", Content: "new value"}}, ops); err == nil {
				t.Fatal("expected injected persistence failure")
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatalf("old store unavailable: %v", err)
			}
			var items []textSnippet
			if err := json.Unmarshal(got, &items); err != nil || len(items) != 1 || items[0].Content != "old value" {
				t.Fatalf("old store not restored: %s (%v)", got, err)
			}
		})
	}
}

func TestInterruptedUploadReceiveIsDiscardedOnRestart(t *testing.T) {
	dir := t.TempDir()
	transaction := filepath.Join(dir, uploadTransactionPrefix+"example-id")
	stage := filepath.Join(transaction, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transaction, "receive.json"), []byte(`{"id":"example-id","destination":"","state":"receiving"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "partial.txt"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recoverUploadTransactions(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transaction); !os.IsNotExist(err) {
		t.Fatalf("interrupted transaction remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "partial.txt")); !os.IsNotExist(err) {
		t.Fatalf("partial upload became visible: %v", err)
	}
}

func TestTextValidation(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	tests := []struct {
		name    string
		content string
		status  int
	}{
		{name: "empty", content: "  \n", status: http.StatusBadRequest},
		{name: "too large", content: strings.Repeat("x", maxTextBytes+1), status: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", test.content))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/not-an-id", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d", recorder.Code)
	}
}

func textRequest(method, target, content string) *http.Request {
	body, _ := json.Marshal(map[string]string{"content": content})
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
