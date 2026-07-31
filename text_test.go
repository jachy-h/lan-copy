package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTextLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	app, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", "电脑上的一段文字\nhttps://example.com"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created textSnippet
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !validTextID(created.ID) || created.Content != "电脑上的一段文字\nhttps://example.com" {
		t.Fatalf("unexpected text: %+v", created)
	}

	restarted, err := newServer(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/texts", nil))
	var listed struct {
		Texts []textSnippet `json:"texts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Texts) != 1 || listed.Texts[0].ID != created.ID {
		t.Fatalf("unexpected texts after restart: %+v", listed.Texts)
	}

	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	var files struct {
		Files []fileInfo `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 0 {
		t.Fatalf("text storage leaked into files: %+v", files.Files)
	}

	recorder = httptest.NewRecorder()
	restarted.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/"+created.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestTextValidation(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.routes()

	tests := []struct {
		name    string
		content string
		status  int
	}{
		{name: "empty", content: "  \n", status: http.StatusBadRequest},
		{name: "too large", content: strings.Repeat("x", maxTextBytes+1), status: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, textRequest(http.MethodPost, "/api/texts", test.content))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/not-an-id", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d", recorder.Code)
	}
}

func textRequest(method, target, content string) *http.Request {
	body, _ := json.Marshal(map[string]string{"content": content})
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
