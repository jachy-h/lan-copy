package httpserver

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThumbnailsAndPreviewMetadata(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(source, image.Rect(0, 0, 200, 200), &image.Uniform{C: color.NRGBA{R: 220, A: 255}}, image.Point{}, draw.Src)
	name := "照片 #.PNG"
	file, err := os.Create(filepath.Join(app.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, source); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for fileName, body := range map[string]string{"drawing.svg": `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><rect width="10" height="10"/></svg>`, "report.docx": "document", "notes.txt": "text", "broken.png": "not an image"} {
		if err := os.WriteFile(filepath.Join(app.dir, fileName), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := app.routes()
	request := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", target, nil))
		return recorder
	}
	result := request("/thumbnails/" + url.PathEscape(name))
	if result.Code != 200 || result.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail status=%d type=%s", result.Code, result.Header().Get("Content-Type"))
	}
	thumb, err := jpeg.Decode(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if thumb.Bounds().Dx() != 128 || thumb.Bounds().Dy() != 64 {
		t.Fatalf("unexpected thumbnail size: %v", thumb.Bounds())
	}
	red, _, _, _ := thumb.At(10, 10).RGBA()
	if red < 50000 {
		t.Fatal("image pixels not preserved")
	}
	red, green, blue, _ := thumb.At(100, 10).RGBA()
	if red < 64000 || green < 64000 || blue < 64000 {
		t.Fatal("transparent area should have a white background")
	}
	cachedRequest := httptest.NewRequest("GET", "/thumbnails/"+url.PathEscape(name), nil)
	cachedRequest.Header.Set("If-None-Match", result.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, cachedRequest)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("thumbnail revalidation failed")
	}
	for _, tc := range []struct {
		target string
		status int
	}{
		{"/thumbnails/drawing.svg", 307}, {"/thumbnails/report.docx", 415}, {"/thumbnails/broken.png", 415}, {"/thumbnails/missing.png", 404}, {"/thumbnails/..%2Fsecret.png", 400},
	} {
		if res := request(tc.target); res.Code != tc.status {
			t.Errorf("%s: got %d want %d", tc.target, res.Code, tc.status)
		}
	}
	svg := request("/preview-files/drawing.svg")
	if svg.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(svg.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("SVG must be served as a sandboxed image")
	}
	for _, target := range []string{"/api/files", "/api/filesystem"} {
		result := request(target)
		var listing struct {
			Files   []fileInfo       `json:"files"`
			Entries []directoryEntry `json:"entries"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		types := map[string]string{}
		for _, file := range listing.Files {
			types[file.Name] = file.PreviewType
		}
		for _, file := range listing.Entries {
			types[file.Name] = file.PreviewType
		}
		if types[name] != "image/png" || types["drawing.svg"] != "image/svg+xml" || types["notes.txt"] != "text/plain; charset=utf-8" || types["report.docx"] != "" {
			t.Fatalf("%s: incorrect preview metadata %v", target, types)
		}
	}
	// Thumbnail requests must share the file endpoint's path and symlink boundary.
	outside := filepath.Join(t.TempDir(), "private.png")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.dir, "link.png")); err == nil {
		if res := request("/thumbnails/link.png"); res.Code != http.StatusBadRequest {
			t.Fatal("thumbnail followed a symlink")
		}
	}
}
