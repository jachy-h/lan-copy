package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lan-copy/internal/filestore"

	"lan-copy/internal/textstore"
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
	var created textstore.Snippet
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
		Texts []textstore.Snippet `json:"texts"`
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

func TestTextChangesPublishUnifiedEvents(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := app.subscribeChanges()
	defer unsubscribe()
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, textRequest(http.MethodPost, "/api/texts", "event"))
	if result.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", result.Code, result.Body.String())
	}
	created := <-updates
	if created.Files || !created.Texts {
		t.Fatalf("create event=%+v", created)
	}
	var item textstore.Snippet
	if err := json.Unmarshal(result.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	result = httptest.NewRecorder()
	app.routes().ServeHTTP(result, httptest.NewRequest(http.MethodDelete, "/api/texts/"+item.ID, nil))
	if result.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", result.Code)
	}
	deleted := <-updates
	if deleted.Files || !deleted.Texts || deleted.Version <= created.Version {
		t.Fatalf("delete event=%+v after %+v", deleted, created)
	}
}

func TestTextStoreRecoversBackupAtStartup(t *testing.T) {
	dir := t.TempDir()
	want := []byte(`[{"id":"0011223344556677","content":"recover me","created":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(filepath.Join(dir, textstore.BackupFileName), want, 0o600); err != nil {
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
	if _, err := os.Stat(filepath.Join(dir, textstore.BackupFileName)); !os.IsNotExist(err) {
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
	items := make([]textstore.Snippet, textstore.MaxCount)
	for i := range items {
		items[i] = textstore.Snippet{ID: strings.Repeat("a", 16), Content: "x"}
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dataDir, textstore.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", "new item"))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create status = %d, want %d: %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestInterruptedUploadReceiveIsDiscardedOnRestart(t *testing.T) {
	dir := t.TempDir()
	transaction := filepath.Join(dir, filestore.TransactionPrefix+"example-id")
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
	if err := filestore.RecoverTransactions(dir, dir); err != nil {
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
		{name: "too large", content: strings.Repeat("x", textstore.MaxTextBytes+1), status: http.StatusRequestEntityTooLarge},
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
