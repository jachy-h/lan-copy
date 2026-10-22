package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemLifecycle(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	request := func(method, target, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(method, target, strings.NewReader(body)))
		if result.Code != status {
			t.Fatalf("%s %s: status %d, want %d: %s", method, target, result.Code, status, result.Body.String())
		}
		return result
	}
	changes, unsubscribe := app.subscribeFileEvents()
	defer unsubscribe()
	request("POST", "/api/filesystem/mkdir", `{"path":"","name":"资料"}`, 204)
	select {
	case <-changes:
	default:
		t.Fatal("mkdir did not notify clients")
	}
	request("POST", "/api/filesystem/mkdir", `{"path":"资料","name":"设计"}`, 204)
	upload := httptest.NewRecorder()
	handler.ServeHTTP(upload, uploadRequest(t, map[string]string{"hello.txt": "shared content"}))
	if upload.Code != 201 {
		t.Fatal(upload.Body.String())
	}
	request("POST", "/api/filesystem/move", `{"path":"hello.txt","destination":"资料/设计"}`, 204)
	response := request("GET", "/api/filesystem?path="+url.QueryEscape("资料/设计"), "", 200)
	var directory struct {
		Entries []directoryEntry `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	if len(directory.Entries) != 1 || directory.Entries[0].Path != "资料/设计/hello.txt" {
		t.Fatalf("entries: %+v", directory.Entries)
	}
	// Original quick-share listing and download URLs continue working after moves.
	response = request("GET", "/api/files", "", 200)
	var listed struct {
		Files []fileInfo `json:"files"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Files) != 1 || listed.Files[0].Name != "资料/设计/hello.txt" {
		t.Fatalf("files: %+v", listed.Files)
	}
	response = request("GET", "/files/"+url.PathEscape(listed.Files[0].Name), "", 200)
	if response.Body.String() != "shared content" {
		t.Fatal(response.Body.String())
	}
	request("POST", "/api/filesystem/rename", `{"path":"资料/设计","destination":"资料","name":"文档"}`, 204)
	request("POST", "/api/filesystem/delete", `{"path":"资料"}`, 409)
	request("DELETE", "/api/files/"+url.PathEscape("资料/文档/hello.txt"), "", 204)
	request("POST", "/api/filesystem/delete", `{"path":"资料/文档"}`, 204)
	request("POST", "/api/filesystem/delete", `{"path":"资料"}`, 204)
}

func TestFilesystemRejectsUnsafeOperations(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "folder", "child"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt", textStore} {
		if err := os.WriteFile(filepath.Join(app.dir, name), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		action, body string
		status       int
	}{
		{"mkdir", `{"path":"","name":"folder"}`, 409},
		{"mkdir", `{"path":"","name":"../escape"}`, 400},
		{"mkdir", `{"path":"","name":".lan-copy-private"}`, 400},
		{"mkdir", `{"path":"../","name":"escape"}`, 400},
		{"mkdir", `{"path":"one.txt","name":"child"}`, 400},
		{"move", `{"path":"folder","destination":"folder/child"}`, 400},
		{"move", `{"path":"one.txt","destination":""}`, 400},
		{"rename", `{"path":"one.txt","destination":"","name":"two.txt"}`, 409},
		{"move", `{"path":"","destination":"folder"}`, 400},
		{"delete", `{"path":""}`, 400},
		{"delete", `{"path":".lan-copy-texts.json"}`, 400},
		{"delete", `{"path":"missing"}`, 404},
		{"mkdir", `{"path":"","name":"new"} {}`, 400},
	} {
		t.Run(test.action+test.body, func(t *testing.T) {
			result := httptest.NewRecorder()
			app.routes().ServeHTTP(result, httptest.NewRequest("POST", "/api/filesystem/"+test.action, strings.NewReader(test.body)))
			if result.Code != test.status {
				t.Fatalf("status=%d want=%d: %s", result.Code, test.status, result.Body.String())
			}
		})
	}
	data, err := os.ReadFile(filepath.Join(app.dir, "two.txt"))
	if err != nil || string(data) != "original" {
		t.Fatal("existing destination was modified")
	}
}

func TestFilesystemHidesSymlinksAndInternalFiles(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.dir, "link")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, textStore), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	for _, target := range []string{"/api/filesystem?path=link", "/files/link%2Fsecret.txt", "/api/filesystem?path=.."} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest("GET", target, nil))
		if result.Code != 400 {
			t.Fatalf("%s: %d %s", target, result.Code, result.Body.String())
		}
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest("GET", "/api/filesystem", nil))
	var listing struct {
		Entries []directoryEntry `json:"entries"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 0 {
		t.Fatalf("leaked internal entries: %+v", listing.Entries)
	}
}

func TestSharedSpacePages(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	for _, test := range []struct{ path, want, absent string }{
		{"/", "共享空间", "共享文本"},
		{"/transfer.html", "共享文本", "id=\"explorerTitle\""},
	} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, test.path, nil))
		body := result.Body.String()
		if result.Code != http.StatusOK || !strings.Contains(body, test.want) || strings.Contains(body, test.absent) {
			t.Fatalf("%s: status %d, unexpected page content", test.path, result.Code)
		}
		for _, link := range []string{`href="/"`, `href="/transfer.html"`} {
			if !strings.Contains(body, link) {
				t.Errorf("%s: missing navigation %s", test.path, link)
			}
		}
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/filesystem.html?from=bookmark", nil))
	if result.Code != http.StatusTemporaryRedirect || result.Header().Get("Location") != "/?from=bookmark" {
		t.Fatalf("legacy link: status %d, location %q", result.Code, result.Header().Get("Location"))
	}
}

func TestRecursiveSearchAndScopedUploads(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reports/deep", "other"} {
		if err := os.MkdirAll(filepath.Join(app.dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"reports/deep/Report.TXT", "other/report.txt", "reports/.lan-copy-private"} {
		if err := os.WriteFile(filepath.Join(app.dir, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := app.routes()
	for _, test := range []struct {
		query string
		count int
	}{
		{"path=reports", 1},
		{"path=reports&q=REPORT", 1},
		{"q=report", 3}, // matching folder plus two nested files
		{"path=reports&q=private", 0},
		{"path=other&q=deep", 0},
	} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest("GET", "/api/filesystem?"+test.query, nil))
		var listing struct {
			Entries []directoryEntry `json:"entries"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		if result.Code != 200 || len(listing.Entries) != test.count {
			t.Fatalf("%s: %d %+v", test.query, result.Code, listing.Entries)
		}
	}
	for i := 0; i < 2; i++ {
		request := uploadRequest(t, map[string]string{"uploaded.txt": "nested upload"})
		request.URL.RawQuery = "path=reports%2Fdeep"
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != 201 {
			t.Fatal(result.Body.String())
		}
	}
	for _, name := range []string{"uploaded.txt", "uploaded (2).txt"} {
		data, err := os.ReadFile(filepath.Join(app.dir, "reports", "deep", name))
		if err != nil || string(data) != "nested upload" {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
	for _, target := range []string{"../escape", "reports/deep/Report.TXT", "missing"} {
		request := uploadRequest(t, map[string]string{"bad.txt": "must not save"})
		request.URL.RawQuery = "path=" + url.QueryEscape(target)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != 400 && result.Code != 404 {
			t.Fatalf("unsafe destination %s: %d", target, result.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(app.dir, "bad.txt")); !os.IsNotExist(err) {
		t.Fatal("invalid upload wrote a file")
	}
}
