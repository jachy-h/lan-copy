package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type receivedUploadHook struct {
	header      http.Header
	body        []byte
	signatureOK bool
	calls       *atomic.Int32
}

func uploadHookServer(t *testing.T, manifest Manifest, token *string, response string, status int) (*httptest.Server, *atomic.Int32, chan receivedUploadHook) {
	t.Helper()
	received := make(chan receivedUploadHook, 4)
	calls := &atomic.Int32{}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok","version":"1.0.0"}`)
			return
		case "/manifest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestJSON)
			return
		case "/version":
			_, _ = io.WriteString(w, `{"version":"1.0.0"}`)
			return
		case manifest.UploadHook.Path:
			calls.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			timestamp := r.Header.Get(UploadHookHeaderTimestamp)
			mac := hmac.New(sha256.New, []byte(*token))
			_, _ = mac.Write([]byte(timestamp))
			_, _ = mac.Write([]byte("."))
			_, _ = mac.Write(body)
			expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
			received <- receivedUploadHook{
				header: r.Header.Clone(), body: body,
				signatureOK: hmac.Equal([]byte(expected), []byte(r.Header.Get(UploadHookHeaderSignature))),
				calls:       calls,
			}
			w.WriteHeader(status)
			if response != "" {
				_, _ = io.WriteString(w, response)
			}
			return
		}
		http.NotFound(w, r)
	}))
	return server, calls, received
}

func testManifestWithUploadHook(port int) Manifest {
	manifest := testManifest(port)
	manifest.UI = &ManifestUI{Path: "/"}
	manifest.UploadHook = &ManifestUploadHook{Path: "/hooks/upload", TimeoutSeconds: 2}
	return NormalizeManifest(manifest)
}

func startUploadHookPlugin(t *testing.T, manager *Manager, runtime *fakeRuntime, manifest Manifest, serverURL string, token *string) {
	t.Helper()
	runtime.address = serverURL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) == 0 {
		t.Fatal("container was not created")
	}
	*token = envValue(runtime.created[0].Env, EventTokenEnv)
	if *token == "" {
		t.Fatal("event token was not injected")
	}
}

func TestManifestUploadHookValidation(t *testing.T) {
	manifest := testManifestWithUploadHook(8080)
	if manifest.UploadHook == nil || manifest.UploadHook.TimeoutSeconds != 2 {
		t.Fatalf("unexpected hook: %+v", manifest.UploadHook)
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid hook rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"relative path", func(m *Manifest) { m.UploadHook.Path = "hooks/upload" }},
		{"query path", func(m *Manifest) { m.UploadHook.Path = "/hooks/upload?x=1" }},
		{"traversal path", func(m *Manifest) { m.UploadHook.Path = "/hooks/../upload" }},
		{"timeout low", func(m *Manifest) { m.UploadHook.TimeoutSeconds = -1 }},
		{"timeout high", func(m *Manifest) { m.UploadHook.TimeoutSeconds = MaxUploadHookTimeoutSeconds + 1 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := testManifestWithUploadHook(8080)
			test.mutate(&candidate)
			if err := ValidateManifest(candidate); err == nil {
				t.Fatal("invalid upload hook accepted")
			}
		})
	}

	raw := `{"schemaVersion":1,"id":"demo","name":"Demo","version":"1.0.0","protocolVersion":"1.0","runtime":"http","containerPort":8080,"endpoints":{"health":"/health","manifest":"/manifest","version":"/version","capabilities":"/capabilities"},"capabilities":["a.b"],"ui":{"path":"/"},"uploadHook":{"path":"/hooks/upload"}}`
	parsed, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.UploadHook == nil || parsed.UploadHook.TimeoutSeconds != DefaultUploadHookTimeoutSeconds {
		t.Fatalf("default timeout not applied: %+v", parsed.UploadHook)
	}
}

func TestRunUploadHooksDeliversSignedRequestAndDialog(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifestWithUploadHook(8080)
	token := new(string)
	server, calls, received := uploadHookServer(t, manifest, token, `{"schemaVersion":1,"dialogs":[{"title":"确认发票","path":"/review/abc?x=1"}]}`, http.StatusOK)
	defer server.Close()
	startUploadHookPlugin(t, manager, runtime, manifest, server.URL, token)

	dialogs := manager.RunUploadHooks(context.Background(), FileUploadedData{
		Destination: "发票",
		Files:       []UploadedFile{{Path: "发票/a.pdf", Size: 12}},
		Counts:      EventCounts{Files: 1},
	})
	if len(dialogs) != 1 {
		t.Fatalf("dialogs = %+v", dialogs)
	}
	got := dialogs[0]
	if got.PluginID != "demo" || got.PluginName != "Demo" || got.Title != "确认发票" || got.Path != "/review/abc?x=1" {
		t.Fatalf("unexpected dialog: %+v", got)
	}
	select {
	case request := <-received:
		if !request.signatureOK {
			t.Fatal("hook signature did not verify")
		}
		if request.header.Get(UploadHookHeaderName) != "file.uploaded" || request.header.Get(UploadHookHeaderID) == "" {
			t.Fatalf("unexpected hook headers: %+v", request.header)
		}
		var event Event
		if err := json.Unmarshal(request.body, &event); err != nil {
			t.Fatal(err)
		}
		var data FileUploadedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if event.Type != "file.uploaded" || data.Destination != "发票" || len(data.Files) != 1 || data.Files[0].Path != "发票/a.pdf" {
			t.Fatalf("unexpected hook event: %+v %+v", event, data)
		}
	case <-time.After(time.Second):
		t.Fatal("hook was not called")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestRunUploadHooksAcceptsNoDialogAndRejectsInvalidPages(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		response string
	}{
		{"no content", http.StatusNoContent, ""},
		{"empty dialogs", http.StatusOK, `{"schemaVersion":1,"dialogs":[]}`},
		{"external url", http.StatusOK, `{"schemaVersion":1,"dialogs":[{"path":"https://example.com/review"}]}`},
		{"protocol relative", http.StatusOK, `{"schemaVersion":1,"dialogs":[{"path":"//example.com/review"}]}`},
		{"traversal", http.StatusOK, `{"schemaVersion":1,"dialogs":[{"path":"/review/../secret"}]}`},
		{"fragment", http.StatusOK, `{"schemaVersion":1,"dialogs":[{"path":"/review#secret"}]}`},
		{"plugin prefix", http.StatusOK, `{"schemaVersion":1,"dialogs":[{"path":"/plugin-app/other/review"}]}`},
		{"wrong schema", http.StatusOK, `{"schemaVersion":2,"dialogs":[{"path":"/review/ok"}]}`},
		{"plugin error", http.StatusInternalServerError, `{"error":"boom"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manager, runtime, _ := testManager(t)
			manifest := testManifestWithUploadHook(8080)
			token := new(string)
			server, _, _ := uploadHookServer(t, manifest, token, test.response, test.status)
			defer server.Close()
			startUploadHookPlugin(t, manager, runtime, manifest, server.URL, token)
			dialogs := manager.RunUploadHooks(context.Background(), FileUploadedData{
				Files: []UploadedFile{{Path: "发票/a.pdf", Size: 1}},
			})
			if len(dialogs) != 0 {
				t.Fatalf("unexpected dialogs: %+v", dialogs)
			}
		})
	}
}

func TestRunUploadHooksSkipsStoppedAndUnhealthyPlugins(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifestWithUploadHook(8080)
	token := new(string)
	server, calls, _ := uploadHookServer(t, manifest, token, `{"schemaVersion":1,"dialogs":[{"path":"/review/ok"}]}`, http.StatusOK)
	defer server.Close()
	startUploadHookPlugin(t, manager, runtime, manifest, server.URL, token)

	if err := manager.Stop(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if dialogs := manager.RunUploadHooks(context.Background(), FileUploadedData{Files: []UploadedFile{{Path: "a.pdf"}}}); len(dialogs) != 0 {
		t.Fatalf("stopped plugin returned dialogs: %+v", dialogs)
	}
	if calls.Load() != 0 {
		t.Fatalf("stopped plugin was called %d times", calls.Load())
	}
}

func TestRunUploadHooksRequiresFiles(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifestWithUploadHook(8080)
	token := new(string)
	server, calls, _ := uploadHookServer(t, manifest, token, `{"schemaVersion":1,"dialogs":[{"path":"/review/ok"}]}`, http.StatusOK)
	defer server.Close()
	startUploadHookPlugin(t, manager, runtime, manifest, server.URL, token)

	if dialogs := manager.RunUploadHooks(context.Background(), FileUploadedData{}); len(dialogs) != 0 {
		t.Fatalf("empty upload returned dialogs: %+v", dialogs)
	}
	if calls.Load() != 0 {
		t.Fatalf("empty upload called the hook %d times", calls.Load())
	}
}

func TestValidateUploadHookPagePath(t *testing.T) {
	valid := []string{"/review/abc", "/review/abc?x=1", "/", "/a/b-c_d"}
	for _, path := range valid {
		if got, ok := validateUploadHookPagePath(path); !ok || got != path {
			t.Errorf("valid path %q rejected", path)
		}
	}
	invalid := []string{
		"", "review/abc", "https://example.com/x", "//example.com/x", "/review/../x",
		"/review/./x", "/plugin-app/demo/x", "/review#x", "/review x", "/review\\x", "/%2e%2e/secret", "/review/%00",
	}
	for _, path := range invalid {
		if _, ok := validateUploadHookPagePath(path); ok {
			t.Errorf("invalid path %q accepted", path)
		}
	}
}

func TestSanitizeUploadHookTitle(t *testing.T) {
	if got := sanitizeUploadHookTitle("", "Demo Plugin"); got != "Demo Plugin" {
		t.Fatalf("default title = %q", got)
	}
	if got := sanitizeUploadHookTitle("  a\x00b  ", "Demo"); got != "ab" {
		t.Fatalf("sanitized title = %q", got)
	}
	long := strings.Repeat("票", 120)
	if got := sanitizeUploadHookTitle(long, "Demo"); len([]rune(got)) != 100 {
		t.Fatalf("long title length = %d", len([]rune(got)))
	}
}
