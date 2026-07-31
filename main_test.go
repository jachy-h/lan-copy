package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileLifecycle(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	upload := uploadRequest(t, map[string]string{"hello.txt": "来自另一台电脑"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, upload)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", recorder.Code)
	}
	var listed struct {
		Files []fileInfo `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Files) != 1 || listed.Files[0].Name != "hello.txt" {
		t.Fatalf("unexpected files: %+v", listed.Files)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/files/hello.txt", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "来自另一台电脑" {
		t.Fatalf("download status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("missing attachment disposition: %q", disposition)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/files/hello.txt", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", recorder.Code)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestDuplicateNamesArePreserved(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, uploadRequest(t, map[string]string{"photo.jpg": "data"}))
		if recorder.Code != http.StatusCreated {
			t.Fatalf("upload %d status = %d", i, recorder.Code)
		}
	}
	for _, name := range []string{"photo.jpg", "photo (2).jpg"} {
		if _, err := os.Stat(filepath.Join(app.dir, name)); err != nil {
			t.Errorf("expected %q: %v", name, err)
		}
	}
}

func TestUploadLimit(t *testing.T) {
	app, err := newServer(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, uploadRequest(t, map[string]string{"large.bin": strings.Repeat("x", 256)}))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestFileEventsEndpoint(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(app.routes())
	defer testServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("event content type = %q", response.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() && scanner.Text() != "" {
	}
	app.notifyFilesChanged()
	if !scanner.Scan() || scanner.Text() != "event: files" {
		t.Fatalf("event line = %q, error = %v", scanner.Text(), scanner.Err())
	}
}

func TestFileChangesNotifySubscribers(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := app.subscribeFileEvents()
	defer unsubscribe()
	handler := app.routes()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, uploadRequest(t, map[string]string{"event.txt": "data"}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("upload did not publish a file update")
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/files/event.txt", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("delete did not publish a file update")
	}
}

func TestReachableLocalURLsFiltersFailedProbes(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	}))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	got := reachableLocalURLs(context.Background(), []string{closedURL, healthy.URL, unhealthy.URL})
	if len(got) != 1 || got[0] != healthy.URL {
		t.Fatalf("reachableLocalURLs() = %v, want [%s]", got, healthy.URL)
	}
}

func TestSortLocalURLsPrefers192Addresses(t *testing.T) {
	urls := []string{
		"http://172.16.0.2:8080",
		"http://192.168.2.8:8080",
		"http://10.0.0.3:8080",
		"http://192.168.1.9:8080",
	}
	sortLocalURLs(urls)
	want := []string{
		"http://192.168.1.9:8080",
		"http://192.168.2.8:8080",
		"http://10.0.0.3:8080",
		"http://172.16.0.2:8080",
	}
	for index := range want {
		if urls[index] != want[index] {
			t.Fatalf("urls[%d] = %q, want %q; all URLs: %v", index, urls[index], want[index], urls)
		}
	}
}

func TestAccessEndpoint(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	probeCalled := false
	app.probeURLs = func(_ context.Context, urls []string) []string {
		probeCalled = true
		return urls
	}
	request := httptest.NewRequest(http.MethodGet, "/api/access", nil)
	request.Host = "localhost:4312"
	request.RemoteAddr = "127.0.0.1:54321"
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("access status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !probeCalled {
		t.Fatal("access endpoint did not probe local URLs")
	}
	var response struct {
		URLs  []string `json:"urls"`
		Local bool     `json:"local"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, address := range response.URLs {
		if !strings.HasSuffix(address, ":4312") {
			t.Errorf("access URL %q does not use request port", address)
		}
	}
	if !response.Local {
		t.Fatal("loopback request was not marked as local")
	}
}

func TestLocalFileActions(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(app.dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var actions []string
	app.openPath = func(gotPath, action string) error {
		if gotPath != filePath {
			t.Errorf("open path = %q, want %q", gotPath, filePath)
		}
		actions = append(actions, action)
		return nil
	}
	handler := app.routes()
	for _, action := range []string{"open", "folder"} {
		request := httptest.NewRequest(http.MethodPost, "/api/files/hello.txt/"+action, nil)
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("X-Lan-Copy-Action", action)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s status = %d, body = %s", action, recorder.Code, recorder.Body.String())
		}
	}
	if strings.Join(actions, ",") != "open,folder" {
		t.Fatalf("local actions = %v", actions)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/files/hello.txt/open", nil)
	request.RemoteAddr = "203.0.113.8:54321"
	request.Header.Set("X-Lan-Copy-Action", "open")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("remote action status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if len(actions) != 2 {
		t.Fatal("remote request executed a local file action")
	}
}

func TestShutdownEndpoint(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/shutdown", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/shutdown", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	select {
	case <-app.shutdownRequested:
		t.Fatal("unauthorized request triggered shutdown")
	default:
	}

	request := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("shutdown status = %d, want %d; body = %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	select {
	case <-app.shutdownRequested:
	default:
		t.Fatal("shutdown request did not signal the server")
	}

	request = httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("repeated shutdown status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
}

func TestSafeName(t *testing.T) {
	tests := map[string]string{
		"normal.txt":          "normal.txt",
		"folder/document.pdf": "document.pdf",
		`C:\\fakepath\\a.png`: "a.png",
	}
	for input, want := range tests {
		got, err := safeName(input)
		if err != nil || got != want {
			t.Errorf("safeName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", ".", "..", ".upload-reserved", "bad\x00name"} {
		if _, err := safeName(input); err == nil {
			t.Errorf("safeName(%q) unexpectedly succeeded", input)
		}
	}
}

func uploadRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, contents := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
