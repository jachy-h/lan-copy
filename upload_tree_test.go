package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type treeTestPart struct{ field, body string }

func treeRequest(t *testing.T, entries []uploadTreeEntry, parts []treeTestPart) *http.Request {
	return treeReplaceRequest(t, entries, parts, nil)
}

func treeReplaceRequest(t *testing.T, entries []uploadTreeEntry, parts []treeTestPart, replace []string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	manifest, err := writer.CreateFormField("manifest")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(manifest).Encode(uploadTreeManifest{Entries: entries, Replace: replace}); err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		file, err := writer.CreateFormFile(part.field, "ignored.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/upload-tree", bytes.NewReader(body.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestFolderUploadReplacesOnlyConfirmedRoots(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "目标", "相册"), 0755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(app.dir, "目标", "相册", "keep.txt")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []uploadTreeEntry{{Path: "相册", Directory: true}, {Path: "相册/空目录", Directory: true}, {Path: "相册/子目录/a.txt"}, {Path: "readme.txt"}}
	request := treeReplaceRequest(t, entries, []treeTestPart{{"file-2", "nested"}, {"file-3", "loose"}}, []string{"相册"})
	request.URL.RawQuery = url.Values{"path": {"目标"}}.Encode()
	updates, unsubscribe := app.subscribeChanges()
	defer unsubscribe()
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, request)
	if result.Code != 201 {
		t.Fatalf("%d: %s", result.Code, result.Body.String())
	}
	for name, want := range map[string]string{"目标/相册/子目录/a.txt": "nested", "目标/readme.txt": "loose"} {
		got, err := os.ReadFile(filepath.Join(app.dir, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if info, err := os.Stat(filepath.Join(app.dir, "目标", "相册", "空目录")); err != nil || !info.IsDir() {
		t.Fatal("empty directory missing")
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("old folder content was not replaced")
	}
	var response struct {
		Files       []fileInfo `json:"files"`
		Directories []string   `json:"directories"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Files) != 2 || response.Files[0].Name != "目标/相册/子目录/a.txt" || len(response.Directories) != 3 {
		t.Fatalf("unexpected response: %+v", response)
	}
	select {
	case <-updates:
	default:
		t.Fatal("successful upload did not notify browsers")
	}
	root, _ := os.ReadDir(app.dir)
	for _, entry := range root {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatal("staging directory leaked")
		}
	}
}

func TestEmptyFolderUpload(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, treeRequest(t, []uploadTreeEntry{{Path: "空文件夹/更深", Directory: true}}, nil))
	if result.Code != 201 {
		t.Fatalf("%d: %s", result.Code, result.Body.String())
	}
	if info, err := os.Stat(filepath.Join(app.dir, "空文件夹", "更深")); err != nil || !info.IsDir() {
		t.Fatal("empty folder upload failed")
	}
}

func TestStartupRecoversInterruptedUploadTransaction(t *testing.T) {
	dir := t.TempDir()
	txn := filepath.Join(dir, uploadTransactionPrefix+"interrupted")
	stage := filepath.Join(txn, "stage")
	backup := filepath.Join(txn, "backup")
	for _, name := range []string{filepath.Join(dir, "target"), stage, backup} {
		if err := os.MkdirAll(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a process exit after the replacement and a new root were installed.
	if err := os.Mkdir(filepath.Join(dir, "target", "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target", "folder", "file.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(backup, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "folder", "file.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target", "new.txt"), []byte("new root"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(uploadTransactionRecord{Destination: "target", Roots: []string{"folder", "new.txt"}, Replaced: []string{"folder"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txn, "transaction.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newServer(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "target", "folder", "file.txt"))
	if err != nil || string(got) != "original" {
		t.Fatalf("original replacement content = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "target", "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("partially installed root remains: %v", err)
	}
	if _, err := os.Stat(txn); !os.IsNotExist(err) {
		t.Fatalf("recovered transaction remains: %v", err)
	}
}

func TestStartupKeepsCommittedUploadTransaction(t *testing.T) {
	dir := t.TempDir()
	txn := filepath.Join(dir, uploadTransactionPrefix+"committed")
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(txn, "stage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(txn, "backup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target", "kept.txt"), []byte("committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txn, "committed"), []byte(uploadCommitMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newServer(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "target", "kept.txt")); err != nil || string(data) != "committed" {
		t.Fatalf("committed upload = %q, %v", data, err)
	}
	if _, err := os.Stat(txn); !os.IsNotExist(err) {
		t.Fatalf("committed transaction remains: %v", err)
	}
}

func TestLegacyUploadEndpointRemoved(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("")))
	if result.Code != http.StatusMethodNotAllowed {
		t.Fatalf("legacy upload endpoint status = %d, want %d", result.Code, http.StatusMethodNotAllowed)
	}
}

func TestFileNameConflictSkipsNamesInSameUploadBatch(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "a.txt"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []uploadTreeEntry{{Path: "a.txt"}, {Path: "a (2).txt"}}
	parts := []treeTestPart{{field: "file-0", body: "first"}, {field: "file-1", body: "second"}}
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, treeRequest(t, entries, parts))
	if result.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", result.Code, result.Body.String())
	}
	for name, want := range map[string]string{"a.txt": "existing", "a (2).txt": "second", "a (3).txt": "first"} {
		data, err := os.ReadFile(filepath.Join(app.dir, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", name, data, err, want)
		}
	}
}

func TestFolderUploadRejectsInvalidManifests(t *testing.T) {
	cases := [][]uploadTreeEntry{
		nil, {{Path: "../outside"}}, {{Path: "/absolute"}}, {{Path: "a\\b"}}, {{Path: "a/../b"}}, {{Path: "a//b"}}, {{Path: "a/./b"}},
		{{Path: "a:x"}}, {{Path: "a/b."}}, {{Path: "a/ b"}}, {{Path: ".upload-private/a"}}, {{Path: ".UPLOAD-private/a"}}, {{Path: "a/.lan-copy-texts.json"}}, {{Path: "CON.txt"}}, {{Path: "nested/Lpt9.log"}},
		{{Path: "a"}, {Path: "a/b.txt"}}, {{Path: "a"}, {Path: "a"}}, {{Path: "Photo/a"}, {Path: "photo/b"}}, {{Path: "a"}, {Path: "A"}},
		{{Path: strings.Repeat("a/", 64) + "file"}},
	}
	for index, entries := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			app, _ := newServer(t.TempDir(), 1<<20)
			result := httptest.NewRecorder()
			app.routes().ServeHTTP(result, treeRequest(t, entries, nil))
			if result.Code != 400 {
				t.Fatalf("%d: %s", result.Code, result.Body.String())
			}
			children, _ := os.ReadDir(app.dir)
			if len(children) != 0 {
				t.Fatalf("invalid upload changed storage: %v", children)
			}
		})
	}
}

func TestFolderUploadRollsBackIncompleteAndOversizeRequests(t *testing.T) {
	entries := []uploadTreeEntry{{Path: "folder", Directory: true}, {Path: "folder/a.txt"}, {Path: "folder/b.txt"}}
	for _, tc := range []struct {
		name  string
		parts []treeTestPart
		limit int64
		want  int
	}{
		{"missing", []treeTestPart{{"file-1", "first"}}, 1 << 20, 400},
		{"duplicate", []treeTestPart{{"file-1", "first"}, {"file-1", "second"}}, 1 << 20, 400},
		{"directory body", []treeTestPart{{"file-0", "invalid"}}, 1 << 20, 400},
		{"unknown index", []treeTestPart{{"file-99", "invalid"}}, 1 << 20, 400},
		{"wrong field", []treeTestPart{{"files", "invalid"}}, 1 << 20, 400},
		{"oversize", []treeTestPart{{"file-1", "first"}, {"file-2", strings.Repeat("x", 4096)}}, 2048, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newServer(t.TempDir(), tc.limit)
			updates, cancel := app.subscribeChanges()
			defer cancel()
			result := httptest.NewRecorder()
			app.routes().ServeHTTP(result, treeRequest(t, entries, tc.parts))
			if result.Code != tc.want {
				t.Fatalf("%d: %s", result.Code, result.Body.String())
			}
			children, _ := os.ReadDir(app.dir)
			if len(children) != 0 {
				t.Fatalf("partial upload leaked files: %v", children)
			}
			select {
			case <-updates:
				t.Fatal("failed upload notified as a success")
			default:
			}
		})
	}
}

func TestFolderUploadDestinationBoundary(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	for _, destination := range []string{"../outside", ".upload-private", "missing"} {
		request := treeRequest(t, []uploadTreeEntry{{Path: "folder", Directory: true}}, nil)
		request.URL.RawQuery = url.Values{"path": {destination}}.Encode()
		result := httptest.NewRecorder()
		app.routes().ServeHTTP(result, request)
		if result.Code != 400 && result.Code != 404 {
			t.Fatalf("unsafe destination %s: %d", destination, result.Code)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(app.dir, "link")); err == nil {
		request := treeRequest(t, []uploadTreeEntry{{Path: "folder", Directory: true}}, nil)
		request.URL.RawQuery = "path=link"
		result := httptest.NewRecorder()
		app.routes().ServeHTTP(result, request)
		if result.Code != 400 {
			t.Fatalf("symlink destination accepted: %d", result.Code)
		}
		children, _ := os.ReadDir(outside)
		if len(children) != 0 {
			t.Fatal("wrote outside shared root")
		}
	}
}

func TestConcurrentFolderUploadsRequireConfirmation(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	handler := app.routes()
	entries := []uploadTreeEntry{{Path: "folder/file.txt"}}
	requests := []*http.Request{treeRequest(t, entries, []treeTestPart{{"file-0", "one"}}), treeRequest(t, entries, []treeTestPart{{"file-0", "two"}})}
	results := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var group sync.WaitGroup
	for index := range requests {
		group.Add(1)
		go func(index int) { defer group.Done(); handler.ServeHTTP(results[index], requests[index]) }(index)
	}
	group.Wait()
	codes := []int{results[0].Code, results[1].Code}
	sort.Ints(codes)
	if codes[0] != 201 || codes[1] != 409 {
		t.Fatalf("expected success and conflict, got %v", codes)
	}
	data, err := os.ReadFile(filepath.Join(app.dir, "folder", "file.txt"))
	if err != nil || (string(data) != "one" && string(data) != "two") {
		t.Fatalf("unexpected contents: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "folder (2)")); !os.IsNotExist(err) {
		t.Fatal("conflict silently renamed upload")
	}
}

func TestUploadConflictLeavesEntireBatchUntouched(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	if err := os.Mkdir(filepath.Join(app.dir, "existing"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "existing", "keep.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []uploadTreeEntry{{Path: "existing", Directory: true}, {Path: "existing/new.txt"}, {Path: "new.txt"}}
	result := httptest.NewRecorder()
	app.routes().ServeHTTP(result, treeRequest(t, entries, []treeTestPart{{"file-1", "replacement"}, {"file-2", "new"}}))
	if result.Code != 409 {
		t.Fatalf("%d: %s", result.Code, result.Body.String())
	}
	var response struct {
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil || len(response.Conflicts) != 1 || response.Conflicts[0] != "existing" {
		t.Fatalf("invalid conflicts: %+v %v", response, err)
	}
	data, err := os.ReadFile(filepath.Join(app.dir, "existing", "keep.txt"))
	if err != nil || string(data) != "original" {
		t.Fatal("original changed before confirmation")
	}
	if _, err := os.Stat(filepath.Join(app.dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("conflicting batch partially committed")
	}
}

func TestReplacementRollbackRestoresOriginal(t *testing.T) {
	app, _ := newServer(t.TempDir(), 1<<20)
	original := filepath.Join(app.dir, "a.txt")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "a.txt"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	// The missing second staged file forces failure after replacing the first.
	if _, err := app.commitUploadTree(stage, "", []string{"a.txt", "missing.txt"}, map[string]bool{"a.txt": true}); err == nil {
		t.Fatal("expected commit failure")
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "original" {
		t.Fatalf("original not restored: %q %v", data, err)
	}
}
