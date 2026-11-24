package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSystemUsageReportsAppAndPlugins(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer app.cancelApp()
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/system-usage", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache = %q", cache)
	}
	var result struct {
		App struct {
			CPUPercent  float64   `json:"cpuPercent"`
			MemoryBytes int64     `json:"memoryBytes"`
			CPUReady    bool      `json:"cpuReady"`
			SampledAt   time.Time `json:"sampledAt"`
		} `json:"app"`
		Plugins struct {
			Running     int               `json:"running"`
			Unavailable int               `json:"unavailable"`
			Items       []json.RawMessage `json:"items"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.App.MemoryBytes <= 0 || result.App.SampledAt.IsZero() {
		t.Fatalf("app = %+v", result.App)
	}
	if result.Plugins.Running != 0 || result.Plugins.Unavailable != 0 || len(result.Plugins.Items) != 0 {
		t.Fatalf("plugins = %+v", result.Plugins)
	}
	if !strings.Contains(recorder.Body.String(), `"items":[]`) {
		t.Fatalf("empty plugin list should be an array: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/system-usage", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post status = %d", recorder.Code)
	}
}
