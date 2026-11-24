package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"lan-copy/internal/filestore"
	"lan-copy/internal/plugin"
	"lan-copy/internal/tablestore"
)

func mustTableStore(t *testing.T) *tablestore.Store {
	t.Helper()
	dir := t.TempDir()
	files := filestore.New(dir, filepath.Join(dir, ".app-data"))
	return tablestore.New(files)
}

func TestSmartTableCreateQueryAndUpdate(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/create", bytes.NewBufferString(`{"path":"data/orders.csv","columns":["id","status"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Lan-Copy-Action", tableAction)
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil || created.Version == "" {
		t.Fatalf("create response = %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/tables/lock-and-update", bytes.NewBufferString(`{"path":"data/orders.csv","expectedVersion":"`+created.Version+`","keyColumn":"id","operations":[{"kind":"insert","values":{"id":"A","status":"new"}},{"kind":"insert","values":{"id":"B","status":"new"}}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Lan-Copy-Action", tableAction)
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var updated struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil || updated.Version == created.Version {
		t.Fatalf("update response = %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/tables/query", bytes.NewBufferString(`{"path":"data/orders.csv","filters":[{"column":"status","op":"eq","value":"new"}],"sort":{"column":"id","order":"asc"}}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("query status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Version string `json:"version"`
		Total   int    `json:"total"`
		Rows    []struct {
			Values map[string]string `json:"values"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != updated.Version || result.Total != 2 || result.Rows[0].Values["id"] != "A" {
		t.Fatalf("query result = %+v", result)
	}

	// A stale version must not overwrite the table.
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/tables/lock-and-update", bytes.NewBufferString(`{"path":"data/orders.csv","expectedVersion":"`+created.Version+`","keyColumn":"id","operations":[{"kind":"delete","key":"A"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Lan-Copy-Action", tableAction)
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var conflict struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &conflict); err != nil || conflict.Version != updated.Version {
		t.Fatalf("conflict response = %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/tables", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("data/orders.csv")) {
		t.Fatalf("list body = %s", recorder.Body.String())
	}
}

func TestSmartTableWriteRequiresAction(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/create", bytes.NewBufferString(`{"path":"a.csv","columns":["id"]}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action status = %d", recorder.Code)
	}
}

func TestPluginTableAPIReturnsUnauthorizedWithoutToken(t *testing.T) {
	server := &Server{tables: mustTableStore(t), plugins: &plugin.Manager{}, web: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/plugin-api/tables"},
		{http.MethodPost, "/api/plugin-api/tables/query"},
		{http.MethodPost, "/api/plugin-api/tables/lock-and-update"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(`{"path":"a.csv"}`))
		server.routes().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, body = %s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTablesPageRedirectsToFiles(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/tables", "/tables.html"} {
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusFound {
			t.Fatalf("%s status = %d", target, recorder.Code)
		}
		if location := recorder.Header().Get("Location"); location != "/" {
			t.Fatalf("%s location = %q", target, location)
		}
	}
}

func TestTableMarkTogglesListingAndRequiresAction(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "data", "orders.csv"), []byte("id,status\nA,new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/mark", bytes.NewBufferString(`{"path":"data/orders.csv","marked":true}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action status = %d", recorder.Code)
	}

	markTable(t, app, "data/orders.csv", true)
	entries := listDirectoryEntries(t, app, "data")
	if len(entries) != 1 || !entries[0].Table {
		t.Fatalf("marked listing = %+v", entries)
	}

	markTable(t, app, "data/orders.csv", false)
	entries = listDirectoryEntries(t, app, "data")
	if len(entries) != 1 || entries[0].Table {
		t.Fatalf("unmarked listing = %+v", entries)
	}
}

func TestTableMarkRejectsInvalidTable(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		body string
		code int
	}{
		{`{"path":"missing.csv","marked":true}`, http.StatusNotFound},
		{`{"path":"notes.txt","marked":true}`, http.StatusBadRequest},
		{`{"path":"../outside.csv","marked":true}`, http.StatusBadRequest},
		{`{"path":"missing.csv","marked":false}`, http.StatusOK},
		{`{"path":"missing.csv"}`, http.StatusBadRequest},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/tables/mark", bytes.NewBufferString(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Lan-Copy-Action", tableAction)
		app.routes().ServeHTTP(recorder, request)
		if recorder.Code != test.code {
			t.Fatalf("%s status = %d, body = %s", test.body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTableMarkFollowsRenameAndDelete(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "data", "orders.csv"), []byte("id,status\nA,new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markTable(t, app, "data/orders.csv", true)
	saveTableLayout(t, app, `{"path":"data/orders.csv","order":["status","id"],"hidden":["status"]}`)
	mutateFilesystem(t, app, "rename", `{"path":"data/orders.csv","destination":"data","name":"sales.csv"}`)
	entries := listDirectoryEntries(t, app, "data")
	if len(entries) != 1 || entries[0].Path != "data/sales.csv" || !entries[0].Table {
		t.Fatalf("renamed listing = %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "data", "orders.csv.hidden.metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("old layout sidecar still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "data", "sales.csv.hidden.metadata.json")); err != nil {
		t.Fatalf("renamed layout sidecar missing: %v", err)
	}
	mutateFilesystem(t, app, "delete", `{"path":"data/sales.csv"}`)
	tables, err := os.ReadFile(filepath.Join(app.dataDir, tablestore.MarksFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(tables, []byte("sales.csv")) {
		t.Fatalf("deleted mark remains: %s", tables)
	}
	if _, err := os.Stat(filepath.Join(app.dir, "data", "sales.csv.hidden.metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("deleted layout sidecar remains: %v", err)
	}
}

func TestTableLayoutQueryAndSave(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "data", "orders.csv"), []byte("id,name,status\nA,Alpha,new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/layout", bytes.NewBufferString(`{"path":"data/orders.csv","order":["status","id"],"hidden":["name"]}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing action status = %d", recorder.Code)
	}

	saveTableLayout(t, app, `{"path":"data/orders.csv","order":["status","id","name"],"hidden":["id","name"]}`)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/tables/query", bytes.NewBufferString(`{"path":"data/orders.csv"}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("query status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Columns []string          `json:"columns"`
		Layout  tablestore.Layout `json:"layout"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Columns) != 3 || result.Columns[0] != "id" {
		t.Fatalf("columns = %v", result.Columns)
	}
	if len(result.Layout.Order) != 3 || result.Layout.Order[0] != "status" || len(result.Layout.Hidden) != 2 || result.Layout.Hidden[0] != "id" || result.Layout.Hidden[1] != "name" {
		t.Fatalf("layout = %+v", result.Layout)
	}

	server := &Server{tables: app.tables}
	recorder = httptest.NewRecorder()
	server.queryTableCore(recorder, httptest.NewRequest(http.MethodPost, "/api/plugin-api/tables/query", nil), tableQueryRequest{Path: "data/orders.csv"}, false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("plugin core status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte(`"layout"`)) {
		t.Fatalf("plugin response contains layout: %s", recorder.Body.String())
	}
}

func TestHiddenMetadataFilesStayOutOfFileSurfaces(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app.dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.dir, "data", "orders.csv"), []byte("id\nA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(app.dir, "data", "orders.csv.hidden.metadata.json")
	if err := os.WriteFile(sidecar, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := listDirectoryEntries(t, app, "data")
	if len(entries) != 1 || entries[0].Path != "data/orders.csv" {
		t.Fatalf("listing = %+v", entries)
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/filesystem?"+url.Values{"q": {"hidden.metadata"}}.Encode(), nil))
	if recorder.Code != http.StatusOK || bytes.Contains(recorder.Body.Bytes(), []byte("hidden.metadata")) {
		t.Fatalf("search response = %d: %s", recorder.Code, recorder.Body.String())
	}
	escaped := url.PathEscape("data/orders.csv.hidden.metadata.json")
	for _, target := range []string{
		"/files/" + escaped,
		"/preview-files/" + escaped,
		"/thumbnails/" + escaped,
	} {
		recorder = httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d", target, recorder.Code)
		}
	}
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/filesystem/delete", bytes.NewBufferString(`{"path":"data/orders.csv.hidden.metadata.json"}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("delete status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar was deleted: %v", err)
	}
}

func saveTableLayout(t *testing.T, app *Server, body string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/layout", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Lan-Copy-Action", tableAction)
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("layout status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

type listedEntry struct {
	Path  string `json:"path"`
	Table bool   `json:"table"`
}

func markTable(t *testing.T, app *Server, path string, marked bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "marked": marked})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tables/mark", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Lan-Copy-Action", tableAction)
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mark %s=%v status = %d: %s", path, marked, recorder.Code, recorder.Body.String())
	}
}

func listDirectoryEntries(t *testing.T, app *Server, path string) []listedEntry {
	t.Helper()
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/filesystem?"+url.Values{"path": {path}}.Encode(), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var listing struct {
		Entries []listedEntry `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	return listing.Entries
}

func mutateFilesystem(t *testing.T, app *Server, action, body string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/filesystem/"+action, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("%s status = %d: %s", action, recorder.Code, recorder.Body.String())
	}
}
