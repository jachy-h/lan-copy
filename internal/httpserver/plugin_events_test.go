package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"lan-copy/internal/filestore"
	"lan-copy/internal/plugin"
)

type capturedPluginEvent struct {
	Type string
	Data json.RawMessage
}

func capturePluginEvents(app *Server) *[]capturedPluginEvent {
	events := &[]capturedPluginEvent{}
	app.publishPluginEvent = func(eventType string, data any) {
		encoded, err := json.Marshal(data)
		if err != nil {
			panic(err)
		}
		*events = append(*events, capturedPluginEvent{Type: eventType, Data: encoded})
	}
	return events
}

func lastEvent(t *testing.T, events []capturedPluginEvent, eventType string) capturedPluginEvent {
	t.Helper()
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == eventType {
			return events[index]
		}
	}
	t.Fatalf("event %s not emitted; got %+v", eventType, events)
	return capturedPluginEvent{}
}

func mutate(t *testing.T, app *Server, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/filesystem/"+action, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	return recorder
}

func TestFileLifecycleEvents(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	events := capturePluginEvents(app)

	entries := []filestore.TreeEntry{
		{Path: "folder", Directory: true},
		{Path: "folder/a.txt"},
		{Path: "folder/empty", Directory: true},
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, treeRequest(t, entries, []treeTestPart{{field: "file-1", body: "hello"}}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", recorder.Code, recorder.Body.String())
	}
	uploaded := lastEvent(t, *events, "file.uploaded")
	var uploadData plugin.FileUploadedData
	if err := json.Unmarshal(uploaded.Data, &uploadData); err != nil {
		t.Fatal(err)
	}
	if uploadData.Counts.Files != 1 || uploadData.Counts.Directories != 2 {
		t.Fatalf("upload counts = %+v", uploadData.Counts)
	}
	if len(uploadData.Files) != 1 || uploadData.Files[0].Path != "folder/a.txt" || uploadData.Files[0].Size != 5 {
		t.Fatalf("upload files = %+v", uploadData.Files)
	}
	if uploadData.Truncated {
		t.Fatal("small upload was marked truncated")
	}

	// mkdir
	recorder = mutate(t, app, "mkdir", `{"path":"","name":"archive"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("mkdir status = %d: %s", recorder.Code, recorder.Body.String())
	}
	created := lastEvent(t, *events, "folder.created")
	var createdData plugin.EntryEventData
	if err := json.Unmarshal(created.Data, &createdData); err != nil {
		t.Fatal(err)
	}
	if createdData.Path != "archive" || createdData.Name != "archive" {
		t.Fatalf("folder.created data = %+v", createdData)
	}

	// rename folder
	recorder = mutate(t, app, "rename", `{"path":"archive","destination":"","name":"archive2"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("rename status = %d: %s", recorder.Code, recorder.Body.String())
	}
	renamed := lastEvent(t, *events, "folder.renamed")
	var renamedData plugin.EntryEventData
	if err := json.Unmarshal(renamed.Data, &renamedData); err != nil {
		t.Fatal(err)
	}
	if renamedData.Path != "archive2" || renamedData.PreviousPath != "archive" || renamedData.PreviousName != "archive" {
		t.Fatalf("folder.renamed data = %+v", renamedData)
	}

	// rename file
	recorder = mutate(t, app, "rename", `{"path":"folder/a.txt","destination":"folder","name":"b.txt"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("file rename status = %d: %s", recorder.Code, recorder.Body.String())
	}
	fileRenamed := lastEvent(t, *events, "file.renamed")
	var fileData plugin.EntryEventData
	if err := json.Unmarshal(fileRenamed.Data, &fileData); err != nil {
		t.Fatal(err)
	}
	if fileData.Path != "folder/b.txt" || fileData.Size != 5 {
		t.Fatalf("file.renamed data = %+v", fileData)
	}

	// move file
	recorder = mutate(t, app, "move", `{"path":"folder/b.txt","destination":"archive2"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("move status = %d: %s", recorder.Code, recorder.Body.String())
	}
	moved := lastEvent(t, *events, "file.moved")
	if err := json.Unmarshal(moved.Data, &fileData); err != nil {
		t.Fatal(err)
	}
	if fileData.Path != "archive2/b.txt" || fileData.PreviousPath != "folder/b.txt" {
		t.Fatalf("file.moved data = %+v", fileData)
	}

	// delete file through the directory mutation endpoint
	recorder = mutate(t, app, "delete", `{"path":"archive2/b.txt"}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete file status = %d: %s", recorder.Code, recorder.Body.String())
	}
	deleted := lastEvent(t, *events, "file.deleted")
	if err := json.Unmarshal(deleted.Data, &fileData); err != nil {
		t.Fatal(err)
	}
	if fileData.Path != "archive2/b.txt" || fileData.Size != 5 {
		t.Fatalf("file.deleted data = %+v", fileData)
	}

	// delete folder recursively
	recorder = mutate(t, app, "delete", `{"path":"archive2","recursive":true}`)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete folder status = %d: %s", recorder.Code, recorder.Body.String())
	}
	folderDeleted := lastEvent(t, *events, "folder.deleted")
	if err := json.Unmarshal(folderDeleted.Data, &fileData); err != nil {
		t.Fatal(err)
	}
	if fileData.Path != "archive2" || !fileData.Recursive {
		t.Fatalf("folder.deleted data = %+v", fileData)
	}

	// A rejected operation must not emit an event.
	before := len(*events)
	recorder = mutate(t, app, "mkdir", `{"path":"","name":"folder"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate mkdir status = %d", recorder.Code)
	}
	if len(*events) != before {
		t.Fatalf("failed operation emitted an event: %+v", (*events)[before:])
	}
}

func TestDeleteFileEvent(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	events := capturePluginEvents(app)
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, uploadRequest(t, map[string]string{"hello.txt": "hi"}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/files/hello.txt", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var data plugin.EntryEventData
	if err := json.Unmarshal(lastEvent(t, *events, "file.deleted").Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Path != "hello.txt" || data.Name != "hello.txt" || data.Size != 2 {
		t.Fatalf("file.deleted data = %+v", data)
	}
}

func TestTextLifecycleEvents(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	events := capturePluginEvents(app)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/texts", bytes.NewBufferString(`{"content":"note"}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create text status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var fragment struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &fragment); err != nil {
		t.Fatal(err)
	}
	var textData plugin.TextEventData
	if err := json.Unmarshal(lastEvent(t, *events, "text.created").Data, &textData); err != nil {
		t.Fatal(err)
	}
	if textData.ID != fragment.ID || textData.Kind != "text" || textData.Size == 0 {
		t.Fatalf("text.created data = %+v", textData)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/texts/"+fragment.ID+"/save", bytes.NewBufferString(`{"path":"","name":"saved.txt"}`))
	request.Header.Set("Content-Type", "application/json")
	app.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("save text status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var saved plugin.TextSavedData
	if err := json.Unmarshal(lastEvent(t, *events, "text.saved").Data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != fragment.ID || len(saved.Files) != 1 || saved.Files[0].Path != "saved.txt" {
		t.Fatalf("text.saved data = %+v", saved)
	}

	recorder = httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/texts/"+fragment.ID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete text status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(lastEvent(t, *events, "text.deleted").Data, &textData); err != nil {
		t.Fatal(err)
	}
	if textData.ID != fragment.ID {
		t.Fatalf("text.deleted data = %+v", textData)
	}
}

func TestUploadEventTruncatesLargeLists(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	events := capturePluginEvents(app)
	entries := make([]filestore.TreeEntry, 0, maxEventListEntries+5)
	parts := make([]treeTestPart, 0, maxEventListEntries+5)
	for index := 0; index < maxEventListEntries+5; index++ {
		entries = append(entries, filestore.TreeEntry{Path: "file-" + string(rune('a'+index%26)) + string(rune('a'+index/26)) + ".txt"})
	}
	for index := range entries {
		parts = append(parts, treeTestPart{field: "file-" + strconv.Itoa(index), body: "x"})
	}
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, treeRequest(t, entries, parts))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var data plugin.FileUploadedData
	if err := json.Unmarshal(lastEvent(t, *events, "file.uploaded").Data, &data); err != nil {
		t.Fatal(err)
	}
	if !data.Truncated || len(data.Files) != maxEventListEntries || data.Counts.Files != maxEventListEntries+5 {
		t.Fatalf("truncated upload data: counts=%+v files=%d truncated=%v", data.Counts, len(data.Files), data.Truncated)
	}
}
