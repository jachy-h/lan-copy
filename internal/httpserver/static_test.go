package httpserver

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticCachePolicy(t *testing.T) {
	handler := StaticFiles(fstest.MapFS{
		"index.html":                        {Data: []byte("index")},
		"preview.html":                      {Data: []byte("preview")},
		"_frontend/assets/main-Abc123_X.js": {Data: []byte("module")},
		"_frontend/assets/main.js":          {Data: []byte("fixed")},
		"vendor/lib.js":                     {Data: []byte("vendor")},
	})
	for _, tc := range []struct {
		url, cache string
		status     int
	}{
		{"/", "no-cache", 200}, {"/preview.html", "no-cache", 200},
		{"/_frontend/assets/main-Abc123_X.js", "public, max-age=31536000, immutable", 200},
		{"/_frontend/assets/main.js", "no-cache", 200}, {"/vendor/lib.js", "no-cache", 200},
		{"/_frontend/assets/missing-Abc123_X.js", "no-cache", 404},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(method, tc.url, nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != tc.cache {
				t.Errorf("%s %s status=%d cache=%q", method, tc.url, w.Code, w.Header().Get("Cache-Control"))
			}
		}
	}
}
