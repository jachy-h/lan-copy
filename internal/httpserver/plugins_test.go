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

func TestPluginReadsAllowLAN(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	server.listPlugins(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("LAN list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/plugins/event-catalog", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	server.pluginEventCatalog(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "file.uploaded") {
		t.Fatalf("LAN catalog status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestPluginWritesAllowLANWithActionHeader(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/demo/events/test", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	request.SetPathValue("id", "demo")
	server.pluginTestEvent(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("LAN write without action status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/plugins/demo/events/test", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	request.SetPathValue("id", "demo")
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	server.pluginTestEvent(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("LAN write with action should reach the handler, status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/plugins/demo/events/test", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	request.SetPathValue("id", "demo")
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	request.Header.Set("Origin", "http://evil.example")
	server.pluginTestEvent(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("LAN cross-origin write status = %d", recorder.Code)
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

func TestPluginEventRoutes(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/plugins/event-catalog", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	server.pluginEventCatalog(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "file.uploaded") {
		t.Fatalf("catalog status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/plugins/demo/events", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.SetPathValue("id", "demo")
	server.pluginEvents(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin events status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/plugins/demo/events/test", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.SetPathValue("id", "demo")
	server.pluginTestEvent(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action status = %d", recorder.Code)
	}
}

func TestPluginSubroutesServed(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/plugins", "/plugins/installed", "/plugins/images", "/plugins/integration"} {
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "插件服务") {
			t.Fatalf("%s body = %s", path, recorder.Body.String())
		}
	}
}

func TestPluginRuntimeStartAllowsLANWithActionHeader(t *testing.T) {
	server := &Server{plugins: &plugin.Manager{}}
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/runtime/start", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	recorder := httptest.NewRecorder()
	server.startPluginRuntime(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action header status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/plugins/runtime/start", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	request.Header.Set("X-Lan-Copy-Action", pluginAction)
	recorder = httptest.NewRecorder()
	server.startPluginRuntime(recorder, request)
	if recorder.Code == http.StatusForbidden {
		t.Fatalf("LAN start with action header rejected: %s", recorder.Body.String())
	}

	dispatch := server.pluginDispatch()
	request = httptest.NewRequest(http.MethodGet, "/api/plugins/runtime/start", nil)
	request.RemoteAddr = "192.168.1.20:5555"
	recorder = httptest.NewRecorder()
	dispatch.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", recorder.Code)
	}
}
