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
	"os"
	"strings"
	"testing"
	"time"
)

func testManifestWithEvents(port int, events ...string) Manifest {
	manifest := testManifest(port)
	manifest.Events = events
	normalized := NormalizeManifest(manifest)
	return normalized
}

func importManifestImage(t *testing.T, manager *Manager, runtime *fakeRuntime, manifest Manifest) Image {
	t.Helper()
	runtime.imported = []ImageInfo{testImageInfo(t, manifest)}
	staged, err := manager.StageImage("demo.tar", strings.NewReader(string(makeTar(t))))
	if err != nil {
		t.Fatal(err)
	}
	image, err := manager.ImportStaged(context.Background(), staged, nil)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func envValue(env []string, name string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

type receivedEvent struct {
	header      http.Header
	body        []byte
	event       Event
	signatureOK bool
}

func eventServer(t *testing.T, manifest Manifest, token *string, status int) (*httptest.Server, chan receivedEvent) {
	t.Helper()
	received := make(chan receivedEvent, 8)
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
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var event Event
		_ = json.Unmarshal(body, &event)
		timestamp := r.Header.Get(EventHeaderTimestamp)
		mac := hmac.New(sha256.New, []byte(*token))
		_, _ = mac.Write([]byte(timestamp))
		_, _ = mac.Write([]byte("."))
		_, _ = mac.Write(body)
		expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
		received <- receivedEvent{
			header: r.Header.Clone(), body: body, event: event,
			signatureOK: hmac.Equal([]byte(expected), []byte(r.Header.Get(EventHeaderSignature))),
		}
		w.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	return server, received
}

func waitForEvent(t *testing.T, ch chan receivedEvent) receivedEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(4 * time.Second):
		t.Fatal("event was not delivered")
		return receivedEvent{}
	}
}

func waitForDelivered(t *testing.T, manager *Manager, want uint64) EventEndpointView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var view EventEndpointView
	for time.Now().Before(deadline) {
		var err error
		view, err = manager.EventEndpoint("demo")
		if err != nil {
			t.Fatal(err)
		}
		if view.Stats.Delivered >= want {
			return view
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("delivery stats = %+v", view.Stats)
	return view
}

func TestManifestEvents(t *testing.T) {
	manifest, err := ParseManifest(`{"schemaVersion":1,"id":"demo","name":"Demo","version":"1.0.0","protocolVersion":"1.0","runtime":"http","containerPort":8080,"endpoints":{"health":"/health","manifest":"/manifest","version":"/version","capabilities":"/capabilities"},"capabilities":["a.b"],"events":["file.uploaded","folder.*","*"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Endpoints.Events != DefaultEventsEndpoint {
		t.Fatalf("default event endpoint = %q", manifest.Endpoints.Events)
	}
	if len(manifest.Events) != 3 {
		t.Fatalf("events = %+v", manifest.Events)
	}
	invalid := []string{"File.Uploaded", "file..uploaded", "file.uploaded.*", "", "file", "file.uploaded extra"}
	for _, subscription := range invalid {
		if validEventSubscription(subscription) {
			t.Errorf("invalid subscription %q accepted", subscription)
		}
	}
	for _, subscription := range []string{"*", "file.*", "file.uploaded", "text.created"} {
		if !validEventSubscription(subscription) {
			t.Errorf("valid subscription %q rejected", subscription)
		}
	}
	duplicate := testManifestWithEvents(8080, "file.uploaded", "file.uploaded")
	if err := ValidateManifest(duplicate); err == nil {
		t.Fatal("duplicate subscriptions accepted")
	}
	tooMany := testManifestWithEvents(8080)
	for i := 0; i <= MaxEventSubscriptions; i++ {
		tooMany.Events = append(tooMany.Events, "file.uploaded")
	}
	if err := ValidateManifest(tooMany); err == nil {
		t.Fatal("too many subscriptions accepted")
	}
	badEndpoint := testManifestWithEvents(8080, "file.uploaded")
	badEndpoint.Endpoints.Events = "events"
	if err := ValidateManifest(badEndpoint); err == nil {
		t.Fatal("relative event endpoint accepted")
	}
	if err := ValidateConfig(Config{Env: []EnvVar{{Name: EventTokenEnv, Value: "x"}}}, testManifest(8080)); err == nil {
		t.Fatal("reserved event token variable accepted")
	}
}

func TestEventSubscriptionMatching(t *testing.T) {
	if !matchesEventSubscription([]string{"file.uploaded"}, "file.uploaded") {
		t.Fatal("exact subscription did not match")
	}
	if !matchesEventSubscription([]string{"file.*"}, "file.moved") {
		t.Fatal("wildcard subscription did not match")
	}
	if !matchesEventSubscription([]string{"*"}, "text.created") {
		t.Fatal("global subscription did not match")
	}
	if matchesEventSubscription([]string{"file.*"}, "text.created") {
		t.Fatal("wildcard matched another domain")
	}
	for _, entry := range EventCatalog() {
		if !validEventType(entry.Type) {
			t.Errorf("catalog type %q is invalid", entry.Type)
		}
		if len(entry.Data) == 0 || !json.Valid(entry.Data) {
			t.Errorf("catalog sample for %q is invalid", entry.Type)
		}
	}
}

func TestPublishDeliversSignedEvent(t *testing.T) {
	manager, runtime, store := testManager(t)
	manifest := testManifestWithEvents(8080, "file.uploaded", "system.ping")
	token := new(string)
	server, received := eventServer(t, manifest, token, 200)
	defer server.Close()
	runtime.address = server.URL
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
	eventToken := envValue(runtime.created[0].Env, EventTokenEnv)
	if eventToken == "" {
		t.Fatal("event token was not injected")
	}
	*token = eventToken

	// The token must never be readable in the state file.
	stateData, err := os.ReadFile(store.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateData), eventToken) || strings.Contains(string(stateData), "eventToken") {
		t.Fatal("derived event token leaked into the state file")
	}

	eventID := manager.Publish("file.uploaded", EntryEventData{Path: "docs/a.pdf", Name: "a.pdf", Size: 12})
	if eventID == "" {
		t.Fatal("publish returned no event id")
	}
	got := waitForEvent(t, received)
	if !got.signatureOK {
		t.Fatalf("signature mismatch: headers=%v body=%s", got.header, got.body)
	}
	if got.header.Get(EventHeaderName) != "file.uploaded" || got.header.Get(EventHeaderID) != eventID {
		t.Fatalf("unexpected headers: %v", got.header)
	}
	if got.event.ID != eventID || got.event.Type != "file.uploaded" || got.event.SchemaVersion != 1 {
		t.Fatalf("unexpected event: %+v", got.event)
	}
	var data EntryEventData
	if err := json.Unmarshal(got.event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Path != "docs/a.pdf" || data.Size != 12 {
		t.Fatalf("unexpected data: %+v", data)
	}
	view := waitForDelivered(t, manager, 1)
	if len(view.Deliveries) == 0 || view.Deliveries[len(view.Deliveries)-1].Status != "delivered" {
		t.Fatalf("deliveries = %+v", view.Deliveries)
	}
	if len(view.Subscriptions) != 2 || view.Endpoint != "/events" {
		t.Fatalf("endpoint view = %+v", view)
	}

	// Unsubscribed types are not queued for this plugin.
	manager.Publish("file.deleted", EntryEventData{Path: "docs/a.pdf"})
	select {
	case event := <-received:
		t.Fatalf("unexpected event delivered: %+v", event.event)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestTestEventBypassesSubscription(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifest(8080)
	token := new(string)
	server, received := eventServer(t, manifest, token, 200)
	defer server.Close()
	runtime.address = server.URL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	*token = envValue(runtime.created[0].Env, EventTokenEnv)
	id, err := manager.SendTestEvent("demo")
	if err != nil {
		t.Fatal(err)
	}
	got := waitForEvent(t, received)
	if got.event.Type != "system.ping" || got.event.ID != id {
		t.Fatalf("unexpected test event: %+v", got.event)
	}
}

func TestEventDeliveryFailureIsBounded(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifestWithEvents(8080, "file.deleted")
	token := new(string)
	server, received := eventServer(t, manifest, token, http.StatusBadRequest)
	defer server.Close()
	runtime.address = server.URL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	*token = envValue(runtime.created[0].Env, EventTokenEnv)
	manager.Publish("file.deleted", EntryEventData{Path: "docs/a.pdf"})
	waitForEvent(t, received)
	deadline := time.Now().Add(2 * time.Second)
	for {
		view, err := manager.EventEndpoint("demo")
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Deliveries) > 0 {
			delivery := view.Deliveries[len(view.Deliveries)-1]
			if delivery.Status != "failed" || delivery.Attempts != 1 || delivery.StatusCode != http.StatusBadRequest {
				t.Fatalf("delivery = %+v", delivery)
			}
			if view.Stats.Failed != 1 {
				t.Fatalf("stats = %+v", view.Stats)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery record was not written")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestEventEndpointUnknownPlugin(t *testing.T) {
	manager, _, _ := testManager(t)
	if _, err := manager.EventEndpoint("missing"); err == nil {
		t.Fatal("unknown plugin accepted")
	}
	if _, err := manager.SendTestEvent("missing"); err == nil {
		t.Fatal("test event for unknown plugin accepted")
	}
}

func TestViewsDoNotExposeEventToken(t *testing.T) {
	manager, runtime, _ := testManager(t)
	manifest := testManifestWithEvents(8080)
	token := new(string)
	server, _ := eventServer(t, manifest, token, 200)
	defer server.Close()
	runtime.address = server.URL
	image := importManifestImage(t, manager, runtime, manifest)
	if _, err := manager.Install(context.Background(), InstallRequest{ImageID: image.ID}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.created) == 0 {
		t.Fatal("container was not created")
	}
	*token = envValue(runtime.created[0].Env, EventTokenEnv)
	if *token == "" {
		t.Fatal("event token was not injected")
	}
	views, err := json.Marshal(manager.Plugins())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(views), *token) || strings.Contains(string(views), "eventToken") {
		t.Fatal("plugin view exposes the event token")
	}
	detail, err := manager.Detail(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), *token) || strings.Contains(string(encoded), "eventToken") {
		t.Fatal("plugin detail exposes the event token")
	}
}

func TestEventLoggerNilSafe(t *testing.T) {
	// HTTP tests assemble managers directly; publishing must stay a no-op
	// instead of panicking when the manager was not fully initialized.
	var manager *Manager
	if id := manager.Publish("file.uploaded", nil); id != "" {
		t.Fatal("nil manager published an event")
	}
	if id := (&Manager{}).Publish("file.uploaded", nil); id != "" {
		t.Fatal("uninitialized manager published an event")
	}
}
