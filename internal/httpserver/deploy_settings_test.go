package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lan-copy/internal/deploytoken"
	"lan-copy/internal/updater"
)

func deployTokenRequest(method, body string) *http.Request {
	r := httptest.NewRequest(method, "http://192.168.1.2:8080/api/settings/deploy-token", strings.NewReader(body))
	r.RemoteAddr = "192.168.1.9:2000"
	r.Header.Set("Origin", "http://192.168.1.2:8080")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Lan-Copy-Action", "deploy-token")
	return r
}

func TestDeployTokenSettingsFromLAN(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	app.deployAuth = newDeployAuth(t, "")
	app.updater = updater.New(updater.Config{Disabled: "updates disabled"})
	handler := app.Handler()

	request := deployTokenRequest(http.MethodGet, "")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"source":"none"`) {
		t.Fatalf("initial status = %d %s", recorder.Code, recorder.Body.String())
	}

	request = deployTokenRequest(http.MethodPut, `{"token":"pasted-token"}`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save status = %d %s", recorder.Code, recorder.Body.String())
	}
	var status deploytoken.Status
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status != (deploytoken.Status{Configured: true, Source: "file"}) || !app.deployAuth.Match("Bearer pasted-token") {
		t.Fatalf("unexpected saved token status: %+v", status)
	}
	if strings.Contains(recorder.Body.String(), "pasted-token") {
		t.Fatal("settings response leaked the token")
	}

	request = deployTokenRequest(http.MethodGet, "")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"source":"file"`) {
		t.Fatalf("saved status = %d %s", recorder.Code, recorder.Body.String())
	}

	request = deployTokenRequest(http.MethodPut, `{"token":"updated-token"}`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !app.deployAuth.Match("Bearer updated-token") || app.deployAuth.Match("Bearer pasted-token") {
		t.Fatalf("token update failed: %d %s", recorder.Code, recorder.Body.String())
	}

	request = deployTokenRequest(http.MethodDelete, "")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || app.deployAuth.Configured() || !strings.Contains(recorder.Body.String(), `"source":"none"`) {
		t.Fatalf("delete status = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDeployTokenSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		body    string
		headers map[string]string
		want    int
	}{
		{name: "missing action header", method: http.MethodPut, body: `{"token":"secret"}`, headers: map[string]string{"X-Lan-Copy-Action": ""}, want: http.StatusForbidden},
		{name: "cross site", method: http.MethodPut, body: `{"token":"secret"}`, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, want: http.StatusForbidden},
		{name: "cross origin", method: http.MethodPut, body: `{"token":"secret"}`, headers: map[string]string{"Origin": "https://example.test"}, want: http.StatusForbidden},
		{name: "delete needs action header", method: http.MethodDelete, headers: map[string]string{"X-Lan-Copy-Action": ""}, want: http.StatusForbidden},
		{name: "empty token", method: http.MethodPut, body: `{"token":""}`, want: http.StatusBadRequest},
		{name: "space in token", method: http.MethodPut, body: `{"token":"has space"}`, want: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPut, body: `{"token":"secret","extra":true}`, want: http.StatusBadRequest},
		{name: "invalid json", method: http.MethodPut, body: `{`, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := newServer(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			app.deployAuth = newDeployAuth(t, "")
			request := deployTokenRequest(tc.method, tc.body)
			for name, value := range tc.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			app.Handler().ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, recorder.Code, recorder.Body.String())
			}
			if app.deployAuth.Configured() {
				t.Fatal("invalid request configured a token")
			}
		})
	}
}

func TestDeployTokenSettingsUnavailableWithoutStore(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, deployTokenRequest(http.MethodGet, ""))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
