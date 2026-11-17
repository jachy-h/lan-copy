package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lan-copy/internal/plugin"
)

func TestPluginRoutesRequireManager(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/plugins/runtime", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	server.pluginRuntime(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestPluginRoutesAreLocalOnly(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	for _, test := range []struct {
		method string
		path   string
		header string
		body   string
	}{
		{http.MethodGet, "/api/plugins", "", ""},
		{http.MethodGet, "/api/plugins/images", "", ""},
		{http.MethodPost, "/api/plugins/install", "", `{}`},
		{http.MethodPost, "/api/plugins/install", pluginAction, `{}`},
		{http.MethodDelete, "/api/plugins/demo", pluginAction, ``},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.RemoteAddr = "192.168.1.20:5555"
		if test.header != "" {
			request.Header.Set("X-Lan-Copy-Action", test.header)
		}
		handler := map[string]http.HandlerFunc{
			"/api/plugins":         server.listPlugins,
			"/api/plugins/images":  server.listPluginImages,
			"/api/plugins/install": server.installPlugin,
			"/api/plugins/demo":    server.uninstallPlugin,
		}[test.path]
		handler(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d", test.method, test.path, recorder.Code)
		}
	}
}

func TestPluginWriteRequiresActionAndOrigin(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(`{}`))
	request.RemoteAddr = "127.0.0.1:1234"
	server.installPlugin(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action status = %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(`{}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	request.Header.Set("Origin", "http://evil.example")
	recorder = httptest.NewRecorder()
	server.installPlugin(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}
}

func TestPluginInstallRejectsUnknownFields(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(`{"imageId":"img-1","hostConfig":{}}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	server.installPlugin(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPluginUninstallRequiresDataConfirmation(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/plugins/demo", strings.NewReader(`{"deleteData":true}`))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	request.SetPathValue("id", "demo")
	server.uninstallPlugin(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
