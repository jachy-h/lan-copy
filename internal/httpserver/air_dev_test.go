package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAirDevHomepageIsolation(t *testing.T) {
	for _, dev := range []bool{false, true} {
		name := "normal"
		if dev {
			name = "air"
		}
		t.Run(name, func(t *testing.T) {
			server := New(Config{AirDev: dev, Web: fstest.MapFS{
				"index.html":        {Data: []byte("embedded production homepage")},
				"vendor/example.js": {Data: []byte("vendor asset")},
				"logo/example.svg":  {Data: []byte("logo asset")},
			}})
			defer server.cancelApp()
			handler := server.Handler()
			for _, route := range []string{"/", "/?folder=example", "/healthz", "/vendor/example.js", "/logo/example.svg"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest("GET", route, nil))
				if response.Code != 200 {
					t.Fatalf("%s: status %d", route, response.Code)
				}
				expected := map[string]string{"/healthz": "ok\n", "/vendor/example.js": "vendor asset", "/logo/example.svg": "logo asset"}[route]
				if strings.HasPrefix(route, "/?") || route == "/" {
					expected = "embedded production homepage"
					if dev {
						expected = "请通过 <code>make frontend-dev</code> 启动前端调试项目"
						if response.Header().Get("Cache-Control") != "no-store" {
							t.Fatal("development instructions must not be cached")
						}
						if strings.Contains(response.Body.String(), "embedded production homepage") {
							t.Fatal("development root served production page")
						}
					}
				}
				if !strings.Contains(response.Body.String(), expected) {
					t.Fatalf("%s: missing %q in %s", route, expected, response.Body.String())
				}
			}
		})
	}
}
