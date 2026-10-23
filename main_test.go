package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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

func TestAvailableNameReturnsFilesystemErrors(t *testing.T) {
	want := os.ErrPermission
	_, err := availableNameWithLstat(t.TempDir(), "file.txt", func(string) (os.FileInfo, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("availableName error = %v, want %v", err, want)
	}
}

func TestAvailableNameStopsAfterBoundedConflicts(t *testing.T) {
	info, err := os.Stat(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = availableNameWithLstat(t.TempDir(), "file.txt", func(string) (os.FileInfo, error) {
		calls++
		return info, nil
	})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("availableName error = %v, want %v", err, fs.ErrExist)
	}
	if calls != maxAvailableNameAttempts {
		t.Fatalf("lstat calls = %d, want %d", calls, maxAvailableNameAttempts)
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
	if !scanner.Scan() || scanner.Text() != "event: change" {
		t.Fatalf("event line = %q, error = %v", scanner.Text(), scanner.Err())
	}
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"files":true`) {
		t.Fatalf("event payload = %q", scanner.Text())
	}
}

func TestFileChangesNotifySubscribers(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := app.subscribeChanges()
	defer unsubscribe()
	handler := app.routes()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, uploadRequest(t, map[string]string{"event.txt": "data"}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case update := <-updates:
		if !update.Files || update.Texts {
			t.Fatalf("unexpected upload event: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("upload did not publish a file update")
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/files/event.txt", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case update := <-updates:
		if !update.Files || update.Texts {
			t.Fatalf("unexpected delete event: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("delete did not publish a file update")
	}
}

func TestChangeEventsCoalesceResourceKinds(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := app.subscribeChanges()
	defer unsubscribe()
	app.notifyFilesChanged()
	app.notifyTextsChanged()
	update := <-updates
	if !update.Files || !update.Texts || update.Version != 2 {
		t.Fatalf("coalesced change = %+v", update)
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
	unsigned := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	unsigned.RemoteAddr = "127.0.0.1:12345"
	unsigned.Host = "localhost"
	handler.ServeHTTP(recorder, unsigned)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	select {
	case <-app.shutdownRequested:
		t.Fatal("unauthorized request triggered shutdown")
	default:
	}

	remote := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	remote.RemoteAddr = "192.0.2.42:12345"
	remote.Header.Set("X-Lan-Copy-Action", "shutdown")
	remote.Header.Set("X-Forwarded-For", "127.0.0.1")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, remote)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("remote shutdown status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	select {
	case <-app.shutdownRequested:
		t.Fatal("remote request triggered shutdown")
	default:
	}

	request := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost"
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
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("repeated shutdown status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
}

func TestShutdownWarnsAndRequiresTypedConfirmationForTransfers(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !app.transfers.begin(transferUpload) || !app.transfers.begin(transferDownload) {
		t.Fatal("failed to register active transfers")
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/shutdown/status", nil)
	statusRequest.RemoteAddr = "127.0.0.1:12345"
	statusRequest.Host = "localhost"
	statusResult := httptest.NewRecorder()
	app.routes().ServeHTTP(statusResult, statusRequest)
	var status transferCounts
	if statusResult.Code != http.StatusOK || json.Unmarshal(statusResult.Body.Bytes(), &status) != nil || status != (transferCounts{Uploads: 1, Downloads: 1}) {
		t.Fatalf("shutdown status = %d %+v: %s", statusResult.Code, status, statusResult.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != http.StatusConflict {
		t.Fatalf("unconfirmed shutdown status = %d, want %d: %s", result.Code, http.StatusConflict, result.Body.String())
	}
	var warning struct {
		Uploads              int  `json:"uploads"`
		Downloads            int  `json:"downloads"`
		RequiresConfirmation bool `json:"requiresConfirmation"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &warning); err != nil {
		t.Fatal(err)
	}
	if warning.Uploads != 1 || warning.Downloads != 1 || !warning.RequiresConfirmation {
		t.Fatalf("unexpected transfer warning: %+v", warning)
	}
	select {
	case <-app.shutdownRequested:
		t.Fatal("unconfirmed shutdown was accepted")
	default:
	}

	app.transfers.end(transferUpload)
	request = httptest.NewRequest(http.MethodPost, "/api/shutdown", strings.NewReader(`{"confirmation":"强制关闭","uploads":1,"downloads":1}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	result = httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != http.StatusConflict {
		t.Fatalf("stale-count shutdown status = %d, want %d: %s", result.Code, http.StatusConflict, result.Body.String())
	}
	select {
	case <-app.shutdownRequested:
		t.Fatal("stale transfer count triggered shutdown")
	default:
	}
	request = httptest.NewRequest(http.MethodPost, "/api/shutdown", strings.NewReader(`{"confirmation":"强制关闭","uploads":0,"downloads":1}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "localhost"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	result = httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != http.StatusAccepted {
		t.Fatalf("confirmed shutdown status = %d: %s", result.Code, result.Body.String())
	}
	if app.transfers.begin(transferDownload) {
		t.Fatal("new transfer started after shutdown admission closed")
	}
	select {
	case <-app.shutdownRequested:
	default:
		t.Fatal("confirmed shutdown did not signal the server")
	}
	app.transfers.end(transferDownload)
}

func TestShutdownRequiresLoopbackAddress(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.RemoteAddr = "192.0.2.42:12345"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("non-loopback shutdown status = %d, want %d", result.Code, http.StatusForbidden)
	}
}

func TestShutdownRejectsNonLocalhostHostHeader(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "lan-copy.example:8080"
	request.Header.Set("X-Lan-Copy-Action", "shutdown")
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("non-localhost host status = %d, want %d", result.Code, http.StatusForbidden)
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
	for _, input := range []string{"", ".", "..", ".upload-reserved", ".UPLOAD-reserved", ".LAN-COPY-private", "CON.txt", "lpt9.log", "bad\x00name", "trailing."} {
		if _, err := safeName(input); err == nil {
			t.Errorf("safeName(%q) unexpectedly succeeded", input)
		}
	}
}

func uploadRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]uploadTreeEntry, len(names))
	parts := make([]treeTestPart, len(names))
	for i, name := range names {
		entries[i] = uploadTreeEntry{Path: name}
		parts[i] = treeTestPart{field: fmt.Sprintf("file-%d", i), body: files[name]}
	}
	return treeRequest(t, entries, parts)
}
