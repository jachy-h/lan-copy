package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenContainingFolderWithNestedName(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(app.dir, "资料"), 0755); err != nil {
		t.Fatal(err)
	}
	name := "资料/报告 #.txt"
	fullPath := filepath.Join(app.dir, filepath.FromSlash(name))
	if err := os.WriteFile(fullPath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	app.openPath = func(got, action string) error {
		calls++
		if got != fullPath || action != "folder" {
			t.Fatalf("unexpected local action: %s %s", got, action)
		}
		return nil
	}
	for _, tc := range []struct {
		remote, header string
		want           int
	}{
		{"127.0.0.1:1234", "folder", 202}, {"[::1]:1234", "folder", 202},
		{"203.0.113.8:1234", "folder", 403}, {"127.0.0.1:1234", "", 403},
	} {
		request := httptest.NewRequest("POST", "/api/files/"+url.PathEscape(name)+"/folder", nil)
		request.RemoteAddr = tc.remote
		request.Header.Set("X-Lan-Copy-Action", tc.header)
		result := httptest.NewRecorder()
		app.routes().ServeHTTP(result, request)
		if result.Code != tc.want {
			t.Errorf("%s: status=%d want=%d: %s", tc.remote, result.Code, tc.want, result.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected number of local actions: %d", calls)
	}
}
