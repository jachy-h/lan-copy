package httpserver

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lan-copy/internal/plugin"
)

func proxyTestServer(t *testing.T, target plugin.ProxyTarget) *Server {
	t.Helper()
	return &Server{
		pluginProxyTransport: newPluginProxyTransport(),
		pluginUITarget: func(string) (plugin.ProxyTarget, error) {
			return target, nil
		},
	}
}

func TestPluginUIRedirectsToTrailingSlash(t *testing.T) {
	server := proxyTestServer(t, plugin.ProxyTarget{BaseURL: "http://127.0.0.1:1", UIPath: "/"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://lan.local/plugin-app/demo?a=1", nil)
	server.pluginUIProxy(recorder, request)
	if recorder.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/plugin-app/demo/?a=1" {
		t.Fatalf("location = %q", location)
	}
}

func TestPluginUIProxyForwardsRequest(t *testing.T) {
	type observed struct {
		method, path, query, body string
		headers                   http.Header
	}
	seen := make(chan observed, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- observed{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body), headers: r.Header.Clone()}
		w.Header().Set("Location", "/login?next=%2Fdashboard")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "plugin page")
	}))
	defer upstream.Close()

	server := proxyTestServer(t, plugin.ProxyTarget{BaseURL: upstream.URL, UIPath: "/console"})
	handler := SecurityHeaders(http.HandlerFunc(server.pluginUIProxy))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://lan.local/plugin-app/demo/assets/app.js?mode=edit", strings.NewReader("payload"))
	request.RemoteAddr = "192.168.1.20:5555"
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "plugin page" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location != "/plugin-app/demo/login?next=%2Fdashboard" {
		t.Fatalf("location = %q", location)
	}
	if policy := recorder.Header().Get("Content-Security-Policy"); policy != "default-src 'none'" {
		t.Fatalf("upstream CSP not preserved: %q", policy)
	}

	got := <-seen
	if got.method != http.MethodPost || got.path != "/console/assets/app.js" || got.query != "mode=edit" || got.body != "payload" {
		t.Fatalf("upstream request = %+v", got)
	}
	if prefix := got.headers.Get("X-Forwarded-Prefix"); prefix != "/plugin-app/demo" {
		t.Fatalf("X-Forwarded-Prefix = %q", prefix)
	}
	if host := got.headers.Get("X-Forwarded-Host"); host != "lan.local" {
		t.Fatalf("X-Forwarded-Host = %q", host)
	}
	if proto := got.headers.Get("X-Forwarded-Proto"); proto != "http" {
		t.Fatalf("X-Forwarded-Proto = %q", proto)
	}
	if forwarded := got.headers.Get("X-Forwarded-For"); !strings.Contains(forwarded, "192.168.1.20") {
		t.Fatalf("X-Forwarded-For = %q", forwarded)
	}
}

func TestPluginUIProxyRemovesLanCopySecurityHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>plugin</title>")
	}))
	defer upstream.Close()

	server := proxyTestServer(t, plugin.ProxyTarget{BaseURL: upstream.URL, UIPath: "/"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://lan.local/plugin-app/demo/", nil)
	SecurityHeaders(http.HandlerFunc(server.pluginUIProxy)).ServeHTTP(recorder, request)

	if policy := recorder.Header().Get("Content-Security-Policy"); policy != "" {
		t.Fatalf("CSP = %q, want empty", policy)
	}
	if frame := recorder.Header().Get("X-Frame-Options"); frame != "" {
		t.Fatalf("X-Frame-Options = %q, want empty", frame)
	}
	if nosniff := recorder.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", nosniff)
	}
}

func TestPluginUIProxyErrorResponses(t *testing.T) {
	tests := []struct {
		name    string
		target  func(string) (plugin.ProxyTarget, error)
		status  int
		retry   string
		body    string
		request string
	}{
		{"no ui", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{}, fmt.Errorf("%w: demo", plugin.ErrPluginNoUI)
		}, http.StatusNotFound, "", "没有提供 Web 界面", "http://lan.local/plugin-app/demo/"},
		{"not installed", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{}, fmt.Errorf("%w: demo", plugin.ErrPluginNotInstalled)
		}, http.StatusNotFound, "", "未安装", "http://lan.local/plugin-app/demo/"},
		{"not running", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{}, fmt.Errorf("%w: demo", plugin.ErrPluginNotRunning)
		}, http.StatusServiceUnavailable, "5", "未运行", "http://lan.local/plugin-app/demo/"},
		{"unhealthy", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{}, fmt.Errorf("%w: demo", plugin.ErrPluginUnhealthy)
		}, http.StatusServiceUnavailable, "5", "不健康", "http://lan.local/plugin-app/demo/"},
		{"unknown", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{}, fmt.Errorf("boom")
		}, http.StatusInternalServerError, "", "无法解析", "http://lan.local/plugin-app/demo/"},
		{"traversal", func(string) (plugin.ProxyTarget, error) {
			return plugin.ProxyTarget{BaseURL: "http://127.0.0.1:1", UIPath: "/"}, nil
		}, http.StatusNotFound, "", "不存在", "http://lan.local/plugin-app/demo/%2e%2e/secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{pluginUITarget: test.target, pluginProxyTransport: newPluginProxyTransport()}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.request, nil)
			server.pluginUIProxy(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if retry := recorder.Header().Get("Retry-After"); retry != test.retry {
				t.Fatalf("Retry-After = %q, want %q", retry, test.retry)
			}
			if !strings.Contains(recorder.Body.String(), test.body) {
				t.Fatalf("body = %q, want %q", recorder.Body.String(), test.body)
			}
		})
	}
}

func TestPluginUIProxyRequiresManager(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://lan.local/plugin-app/demo/", nil)
	server.pluginUIProxy(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestPluginUIProxyStreamsServerSentEvents(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: two\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })

	server := proxyTestServer(t, plugin.ProxyTarget{BaseURL: upstream.URL, UIPath: "/"})
	proxy := httptest.NewServer(SecurityHeaders(http.HandlerFunc(server.pluginUIProxy)))
	defer proxy.Close()

	request, err := http.NewRequest(http.MethodGet, proxy.URL+"/plugin-app/demo/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	first := readPluginUILine(t, lines)
	if first != "data: one" {
		t.Fatalf("first line = %q", first)
	}
	releaseOnce.Do(func() { close(release) })
	if second := readPluginUILine(t, lines); second != "data: two" {
		t.Fatalf("second line = %q", second)
	}
}

func readPluginUILine(t *testing.T, lines <-chan string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before the expected event")
			}
			if line != "" {
				return line
			}
		case <-deadline:
			t.Fatal("timed out waiting for streamed data")
		}
	}
}

func TestJoinPluginUIPath(t *testing.T) {
	tests := []struct{ base, rest, want string }{
		{"/", "", "/"},
		{"/", "assets/app.js", "/assets/app.js"},
		{"/console", "", "/console/"},
		{"/console/", "assets/app.js", "/console/assets/app.js"},
		{"/console", "/deep/link", "/console/deep/link"},
	}
	for _, test := range tests {
		if got := joinPluginUIPath(test.base, test.rest); got != test.want {
			t.Fatalf("joinPluginUIPath(%q, %q) = %q, want %q", test.base, test.rest, got, test.want)
		}
	}
}

func TestRewritePluginUILocation(t *testing.T) {
	tests := []struct{ value, want string }{
		{"/login", "/plugin-app/demo/login"},
		{"/login?next=%2Fdashboard", "/plugin-app/demo/login?next=%2Fdashboard"},
		{"/plugin-app/demo/already", "/plugin-app/demo/already"},
		{"relative/path", "relative/path"},
		{"http://127.0.0.1:4567/login", "/plugin-app/demo/login"},
		{"http://lan.local/login?x=1", "/plugin-app/demo/login?x=1"},
		{"https://other.example/login", "https://other.example/login"},
		{"//other.example/login", "//other.example/login"},
	}
	for _, test := range tests {
		header := http.Header{}
		header.Set("Location", test.value)
		rewritePluginUILocation(header, "/plugin-app/demo", "127.0.0.1:4567", "lan.local")
		if got := header.Get("Location"); got != test.want {
			t.Fatalf("rewrite %q = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestPluginUIRouteIsRegistered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "page:"+r.URL.Path)
	}))
	defer upstream.Close()

	server := proxyTestServer(t, plugin.ProxyTarget{BaseURL: upstream.URL, UIPath: "/"})
	server.web = http.NotFoundHandler()
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://lan.local/plugin-app/demo/", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "page:/" {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "http://lan.local/plugin-app/demo", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPermanentRedirect {
		t.Fatalf("redirect status = %d", recorder.Code)
	}
}
