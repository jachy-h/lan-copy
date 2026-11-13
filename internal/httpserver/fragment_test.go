package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lan-copy/internal/textstore"
)

func TestFragmentImageLifecycleAndSaveToSharedSpace(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, imageUploadRequest(t, "截图.png", onePixelPNG))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created textstore.Fragment
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != "image" || created.ExpiresAt.IsZero() {
		t.Fatalf("unexpected fragment: %+v", created)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts/"+created.ID+"/image", nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), onePixelPNG) {
		t.Fatalf("image response = %d, %d bytes", recorder.Code, recorder.Body.Len())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("content type = %q", contentType)
	}
	if policy := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "sandbox") {
		t.Fatalf("missing image sandbox policy: %q", policy)
	}

	if err := os.MkdirAll(filepath.Join(app.dir, "相册"), 0o755); err != nil {
		t.Fatal(err)
	}
	saveBody, _ := json.Marshal(map[string]string{"path": "相册"})
	recorder = httptest.NewRecorder()
	save := httptest.NewRequest(http.MethodPost, "/api/texts/"+created.ID+"/save", bytes.NewReader(saveBody))
	save.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, save)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var saved struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Path != "相册/截图.png" || saved.Name != "截图.png" {
		t.Fatalf("saved = %+v", saved)
	}
	if data, err := os.ReadFile(filepath.Join(app.dir, "相册", "截图.png")); err != nil || !bytes.Equal(data, onePixelPNG) {
		t.Fatalf("saved image mismatch: %v", err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts", nil))
	var listed struct {
		Texts     []textstore.Fragment `json:"texts"`
		Fragments []textstore.Fragment `json:"fragments"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Texts) != 1 || len(listed.Fragments) != 1 {
		t.Fatalf("listed fragments = %+v", listed)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/"+created.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts/"+created.ID+"/image", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted image status = %d", recorder.Code)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "相册", "截图.png")); err != nil {
		t.Fatalf("saved copy should survive fragment deletion: %v", err)
	}
}

func TestFragmentTextCanBeSavedToSharedSpace(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", "共享笔记"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d", recorder.Code)
	}
	var created textstore.Fragment
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": "", "name": "笔记.txt"})
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/texts/"+created.ID+"/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(app.dir, "笔记.txt"))
	if err != nil || string(data) != "共享笔记" {
		t.Fatalf("saved text = %q, %v", data, err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/texts/"+created.ID+"/save", bytes.NewReader(body)))
	if strings.Contains(recorder.Body.String(), "已存在") {
		t.Fatalf("duplicate save must not report a conflict: %s", recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.dir, "笔记 (2).txt")); err != nil {
		t.Fatalf("duplicate save should create a numbered copy: %v", err)
	}
}

func TestFragmentSettingsPersistAndRejectInvalidValues(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	recorder := httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/api/settings/fragments", nil)
	get.RemoteAddr = "127.0.0.1:1234"
	handler.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"expiryDays":7`) {
		t.Fatalf("default settings = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	put := httptest.NewRequest(http.MethodPut, "/api/settings/fragments", strings.NewReader(`{"expiryDays":14}`))
	put.RemoteAddr = "127.0.0.1:1234"
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("X-Lan-Copy-Action", "fragments")
	handler.ServeHTTP(recorder, put)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"expiryDays":14`) {
		t.Fatalf("save settings = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	invalid := httptest.NewRequest(http.MethodPut, "/api/settings/fragments", strings.NewReader(`{"expiryDays":0}`))
	invalid.RemoteAddr = "127.0.0.1:1234"
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("X-Lan-Copy-Action", "fragments")
	handler.ServeHTTP(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings status = %d", recorder.Code)
	}
	restarted, err := newServer(app.dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, get)
	if !strings.Contains(recorder.Body.String(), `"expiryDays":14`) {
		t.Fatalf("settings did not persist: %s", recorder.Body.String())
	}
}

func TestFragmentImageValidation(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, imageUploadRequest(t, "notes.txt", []byte("not an image")))
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported image status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts/not-valid/image", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/texts/0011223344556677/save", strings.NewReader(`{"path":"../"}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing fragment save status = %d", recorder.Code)
	}
}

func TestMixedFragmentLifecycleAndSave(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, fragmentUploadRequest(t, "图文一起发送", map[string][]byte{"第一张.png": onePixelPNG, "第二张.png": onePixelPNG}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create mixed status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created textstore.Fragment
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != "mixed" || len(created.Images) != 2 || created.Content != "图文一起发送" {
		t.Fatalf("mixed fragment = %+v", created)
	}
	if created.Images[0].Width != 1 || created.Images[0].Height != 1 {
		t.Fatalf("image dimensions = %+v", created.Images[0])
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts/"+created.ID+"/images/1", nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), onePixelPNG) {
		t.Fatalf("second image response = %d, %d bytes", recorder.Code, recorder.Body.Len())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts/"+created.ID+"/images/9", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("out-of-range image status = %d", recorder.Code)
	}

	body, _ := json.Marshal(map[string]string{"path": "", "name": "合并碎片.txt"})
	recorder = httptest.NewRecorder()
	save := httptest.NewRequest(http.MethodPost, "/api/texts/"+created.ID+"/save", bytes.NewReader(body))
	save.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, save)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save mixed status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var saved struct {
		Files []struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"files"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Files) != 3 || saved.Path == "" {
		t.Fatalf("saved mixed files = %+v", saved)
	}
	if data, err := os.ReadFile(filepath.Join(app.dir, "合并碎片.txt")); err != nil || string(data) != "图文一起发送" {
		t.Fatalf("saved mixed text = %q, %v", data, err)
	}
	for _, name := range []string{"第一张.png", "第二张.png"} {
		if data, err := os.ReadFile(filepath.Join(app.dir, name)); err != nil || !bytes.Equal(data, onePixelPNG) {
			t.Fatalf("saved mixed image %s: %v", name, err)
		}
	}
}

func fragmentUploadRequest(t *testing.T, content string, images map[string][]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("content", content); err != nil {
		t.Fatal(err)
	}
	for name, data := range images {
		part, err := writer.CreateFormFile("images", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/texts/fragments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func imageUploadRequest(t *testing.T, name string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/texts/images", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
