package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResourceUsageReportsOccupancy(t *testing.T) {
	dir := t.TempDir()
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer app.cancelApp()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "world.txt"), []byte("world!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !app.uploadResources.Begin() {
		t.Fatal("failed to reserve upload slot")
	}
	defer app.uploadResources.End()
	if !app.uploadResources.Reserve(5) {
		t.Fatal("failed to reserve staging bytes")
	}
	defer app.uploadResources.Release(5)
	if !app.transfers.begin(transferDownload) {
		t.Fatal("failed to track download")
	}
	defer app.transfers.end(transferDownload)

	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/resource-usage", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache = %q", cache)
	}
	var result struct {
		Storage struct {
			Bytes int64 `json:"bytes"`
			Files int   `json:"files"`
			Types []struct {
				Kind  string `json:"kind"`
				Bytes int64  `json:"bytes"`
				Files int    `json:"files"`
			} `json:"types"`
		} `json:"storage"`
		Uploads struct {
			Active int `json:"active"`
			Limit  int `json:"limit"`
		} `json:"uploads"`
		Staging struct {
			UsedBytes  int64 `json:"usedBytes"`
			LimitBytes int64 `json:"limitBytes"`
		} `json:"staging"`
		Downloads struct {
			Active int `json:"active"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Storage.Bytes != 11 || result.Storage.Files != 2 {
		t.Fatalf("storage = %+v", result.Storage)
	}
	if len(result.Storage.Types) != 1 || result.Storage.Types[0].Kind != "document" || result.Storage.Types[0].Bytes != 11 || result.Storage.Types[0].Files != 2 {
		t.Fatalf("storage types = %+v", result.Storage.Types)
	}
	if result.Uploads.Active != 1 || result.Uploads.Limit != 3 {
		t.Fatalf("uploads = %+v", result.Uploads)
	}
	if result.Staging.UsedBytes != 5 || result.Staging.LimitBytes != 20*(1<<30) {
		t.Fatalf("staging = %+v", result.Staging)
	}
	if result.Downloads.Active != 1 {
		t.Fatalf("downloads = %+v", result.Downloads)
	}

	app.uploadResources = nil
	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/resource-usage", nil))
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Uploads.Active != 0 || result.Uploads.Limit != 0 || result.Staging.UsedBytes != 0 || result.Staging.LimitBytes != 0 {
		t.Fatalf("disabled resources = %+v %+v", result.Uploads, result.Staging)
	}

	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/resource-usage", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post status = %d", recorder.Code)
	}
}

func TestResourceUsageStorageFailure(t *testing.T) {
	dir := t.TempDir()
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer app.cancelApp()
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/resource-usage", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
