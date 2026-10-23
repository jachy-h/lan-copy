package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadResourceConfigPersistsAndValidates(t *testing.T) {
	dir := t.TempDir()
	config, err := loadUploadResourceConfig(dir)
	if err != nil || config != defaultUploadResourceConfig {
		t.Fatalf("defaults = %+v, %v", config, err)
	}
	want := uploadResourceConfig{Concurrent: 2, StagingGiB: 7, IdleSeconds: 45}
	if err := saveUploadResourceConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	want = uploadResourceConfig{Concurrent: 4, StagingGiB: 8, IdleSeconds: 30}
	if err := saveUploadResourceConfig(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadUploadResourceConfig(dir)
	if err != nil || got != want {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, uploadResourceConfigName), []byte(`{"concurrentUploads":0,"stagingGiB":7,"idleTimeoutSeconds":45}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUploadResourceConfig(dir); err == nil {
		t.Fatal("invalid settings accepted")
	}
}

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

func TestUploadResourceManagerBudgets(t *testing.T) {
	m := newUploadResourceManager(uploadResourceConfig{Concurrent: 1, StagingGiB: 1, IdleSeconds: 5})
	if !m.begin() || m.begin() {
		t.Fatal("concurrency limit not enforced")
	}
	if !m.reserve(gib) || m.reserve(1) {
		t.Fatal("staging limit not enforced")
	}
	m.release(gib)
	m.end()
	if !m.begin() {
		t.Fatal("released upload slot was not reusable")
	}
	m.end()
}

func TestUploadBudgetWriterUsesRemainingCapacity(t *testing.T) {
	m := newUploadResourceManager(uploadResourceConfig{Concurrent: 1, StagingGiB: 1, IdleSeconds: 5})
	m.staged = gib - 2
	var output bytes.Buffer
	var staged int64
	w := uploadBudgetWriter{writer: &output, manager: m, staged: &staged}
	n, err := w.Write([]byte("hello"))
	if n != 2 || !errors.Is(err, errUploadStagingBudget) || output.String() != "he" || staged != 2 {
		t.Fatalf("write=(%d,%v), output=%q staged=%d", n, err, output.String(), staged)
	}
}

func TestUploadResourceSettingsLocalWrite(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir, uploadResources: newUploadResourceManager(defaultUploadResourceConfig)}
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
		Config   uploadResourceConfig `json:"config"`
		Editable bool                 `json:"editable"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config != (uploadResourceConfig{Concurrent: 4, StagingGiB: 12, IdleSeconds: 90}) || !result.Editable {
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
	entries := []uploadTreeEntry{{Path: "folder", Directory: true}, {Path: "folder/a.txt"}}
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
	if !app.uploadPreflight.consume(result.Session, "", entries) {
		t.Fatal("valid preflight session rejected")
	}
	if app.uploadPreflight.consume(result.Session, "", entries) {
		t.Fatal("preflight session replay accepted")
	}
}
