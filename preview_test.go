package main

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserPreview(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(app.dir, "照片"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"clip.mp4": "0123456789",
		"song.mp3": "0123456789", "paper.pdf": "%PDF-1.4\n",
		"source.html": "<script>alert('unsafe')</script>", "icon.svg": "<svg onload='alert(1)'/>",
		"report.doc": "document", "large.log": strings.Repeat("x", 300000),
	}
	files["照片/测试 #.PNG"] = "0123456789"
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(app.dir, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := app.routes()
	for _, tc := range []struct{ name, mediaType string }{
		{"照片/测试 #.PNG", "image/png"}, {"clip.mp4", "video/mp4"}, {"song.mp3", "audio/mpeg"},
		{"paper.pdf", "application/pdf"}, {"source.html", "text/plain; charset=utf-8"}, {"icon.svg", "image/svg+xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(method, "/preview-files/"+url.PathEscape(tc.name), nil))
				if recorder.Code != 200 {
					t.Fatalf("%s status = %d: %s", method, recorder.Code, recorder.Body.String())
				}
				if recorder.Header().Get("Content-Type") != tc.mediaType {
					t.Fatalf("type = %s", recorder.Header().Get("Content-Type"))
				}
				if !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "inline;") {
					t.Fatal("preview is not inline")
				}
				if recorder.Header().Get("X-Frame-Options") != "DENY" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatal("missing preview security headers")
				}
				if method == http.MethodHead && recorder.Body.Len() != 0 {
					t.Fatal("HEAD returned a body")
				}
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/files/"+url.PathEscape(tc.name), nil))
			if !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "attachment;") {
				t.Fatal("download no longer an attachment")
			}
		})
	}
	for _, tc := range []struct{ name, byteRange, want, contentRange string }{
		{"clip.mp4", "bytes=2-5", "2345", "bytes 2-5/10"},
		{"large.log", "bytes=0-262143", strings.Repeat("x", 262144), "bytes 0-262143/300000"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/preview-files/"+url.PathEscape(tc.name), nil)
		request.Header.Set("Range", tc.byteRange)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != 206 || recorder.Body.String() != tc.want || recorder.Header().Get("Content-Range") != tc.contentRange {
			t.Fatalf("range %s status=%d range=%s len=%d", tc.name, recorder.Code, recorder.Header().Get("Content-Range"), recorder.Body.Len())
		}
	}
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"report.doc", 415}, {"missing.jpg", 404}, {"照片", 404},
		{"../outside.txt", 400}, {".lan-copy-texts.json", 400},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/preview-files/"+url.PathEscape(tc.name), nil))
		if recorder.Code != tc.status {
			t.Errorf("%s status=%d want=%d", tc.name, recorder.Code, tc.status)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.dir, "link.txt")); err == nil {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/preview-files/link.txt", nil))
		if recorder.Code != 400 || strings.Contains(recorder.Body.String(), "secret") {
			t.Fatal("preview exposed a symlink")
		}
	}
}

func TestPreviewPageAndBundledPDFResources(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/preview.html", "/preview.js", "/vendor/pdfjs/legacy/build/pdf.min.mjs", "/vendor/pdfjs/legacy/build/pdf.worker.min.mjs", "/vendor/pdfjs/standard_fonts/LiberationSans-Regular.ttf"} {
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != 200 || recorder.Body.Len() == 0 {
			t.Fatalf("missing bundled preview asset %s: %d", target, recorder.Code)
		}
		if strings.HasSuffix(target, ".mjs") && !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("module MIME type: %s", recorder.Header().Get("Content-Type"))
		}
		if target == "/preview.html" && (!strings.Contains(recorder.Header().Get("Content-Security-Policy"), "worker-src 'self'") || recorder.Header().Get("X-Frame-Options") != "DENY") {
			t.Fatal("preview page security policy missing")
		}
	}
}

// Reproduce an incorrect Windows MIME registry mapping on every test platform.
func TestBundledAssetTypesOverrideSystemMappings(t *testing.T) {
	for _, ext := range []string{".js", ".mjs", ".css", ".wasm"} {
		previous := mime.TypeByExtension(ext)
		if err := mime.AddExtensionType(ext, "text/plain; charset=utf-8"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := mime.AddExtensionType(ext, previous); err != nil {
				t.Error(err)
			}
		})
	}
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, contentType string }{
		{"/preview.js", "text/javascript; charset=utf-8"},
		{"/vendor/pdfjs/legacy/build/pdf.min.mjs", "text/javascript; charset=utf-8"},
		{"/vendor/pdfjs/legacy/build/pdf.worker.min.mjs", "text/javascript; charset=utf-8"},
		{"/style.css", "text/css; charset=utf-8"},
		{"/vendor/pdfjs/wasm/openjpeg.wasm", "application/wasm"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, httptest.NewRequest(method, tc.url, nil))
			if w.Code != http.StatusOK || w.Header().Get("Content-Type") != tc.contentType {
				t.Errorf("%s %s: status=%d Content-Type=%q, want %q", method, tc.url, w.Code, w.Header().Get("Content-Type"), tc.contentType)
			}
		}
	}
}
