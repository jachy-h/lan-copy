package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lan-copy/internal/filestore"
)

func TestUploadIdleTimeoutClosesStalledBody(t *testing.T) {
	reader, writer := io.Pipe()
	body := newIdleTimeoutBody(reader, 20*time.Millisecond)
	defer body.Close()
	buffer := make([]byte, 1)
	_, err := body.Read(buffer)
	if !errors.Is(err, errUploadIdleTimeout) {
		t.Fatalf("read error = %v", err)
	}
	_ = writer.Close()
}

func TestUploadResourceSettingsLocalWrite(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir, uploadResources: filestore.NewResources(filestore.DefaultResourceConfig)}
	request := httptest.NewRequest("PUT", "http://localhost:8080/api/settings/upload-resources", strings.NewReader(`{"concurrentUploads":4,"stagingGiB":12,"idleTimeoutSeconds":90}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Lan-Copy-Action", "upload-resources")
	request.Header.Set("Origin", "http://localhost:8080")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	s.uploadResourceSettings(response, request)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Config   filestore.ResourceConfig `json:"config"`
		Editable bool                     `json:"editable"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config != (filestore.ResourceConfig{Concurrent: 4, StagingGiB: 12, IdleSeconds: 90}) || !result.Editable {
		t.Fatalf("result=%+v", result)
	}
	request = httptest.NewRequest("PUT", "http://localhost:8080/api/settings/upload-resources", strings.NewReader(`{"concurrentUploads":4,"stagingGiB":12,"idleTimeoutSeconds":90}`))
	request.RemoteAddr = "192.0.2.3:5"
	request.Header.Set("X-Lan-Copy-Action", "upload-resources")
	response = httptest.NewRecorder()
	s.uploadResourceSettings(response, request)
	if response.Code != 403 {
		t.Fatalf("remote write status=%d", response.Code)
	}
}

func TestUploadPreflightSessionBindsTargetAndManifest(t *testing.T) {
	dir := t.TempDir()
	app, err := newServer(dir, defaultMaxUpload)
	if err != nil {
		t.Fatal(err)
	}
	defer app.cancelApp()
	entries := []filestore.TreeEntry{{Path: "folder", Directory: true}, {Path: "folder/a.txt"}}
	request := httptest.NewRequest("POST", "http://localhost:8080/api/upload-tree/preflight", strings.NewReader(`{"entries":[{"path":"folder","directory":true},{"path":"folder/a.txt","directory":false}]}`))
	response := httptest.NewRecorder()
	app.preflightUploadTree(response, request)
	if response.Code != 200 {
		t.Fatalf("preflight status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Session == "" {
		t.Fatalf("session=%q err=%v", result.Session, err)
	}
	if !app.uploadPreflight.Consume(result.Session, "", entries) {
		t.Fatal("valid preflight session rejected")
	}
	if app.uploadPreflight.Consume(result.Session, "", entries) {
		t.Fatal("preflight session replay accepted")
	}
}
