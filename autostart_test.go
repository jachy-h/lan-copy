package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeStartup struct {
	enabled           bool
	calls             int
	readErr, writeErr error
}

func (f *fakeStartup) Status() (bool, string, error) { return f.enabled, "", f.readErr }
func (f *fakeStartup) Set(v bool) error {
	f.calls++
	if f.writeErr != nil {
		return f.writeErr
	}
	f.enabled = v
	return nil
}

func TestAutostartSettings(t *testing.T) {
	for _, tc := range []struct {
		name, method, remote, action, origin, site, body string
		code, writes                                     int
		disabled                                         string
		fail                                             bool
		enabled                                          bool
	}{
		{name: "read", method: "GET", remote: "127.0.0.1:123", code: 200},
		{name: "enable", method: "PUT", remote: "127.0.0.1:123", action: "autostart", origin: "http://localhost:8080", site: "same-origin", body: `{"enabled":true}`, code: 200, writes: 1},
		{name: "disable", method: "PUT", remote: "[::1]:123", action: "autostart", body: `{"enabled":false}`, code: 200, writes: 1},
		{name: "remote read disabled", method: "GET", remote: "192.0.2.10:123", code: 200},
		{name: "remote read enabled", method: "GET", remote: "192.0.2.10:123", enabled: true, code: 200},
		{name: "remote write", method: "PUT", remote: "192.0.2.10:123", action: "autostart", body: `{"enabled":true}`, code: 403},
		{name: "missing action", method: "PUT", remote: "127.0.0.1:123", body: `{"enabled":true}`, code: 403},
		{name: "cross origin", method: "PUT", remote: "127.0.0.1:123", action: "autostart", origin: "http://evil.example", body: `{"enabled":true}`, code: 403},
		{name: "cross site", method: "PUT", remote: "127.0.0.1:123", action: "autostart", site: "cross-site", body: `{"enabled":true}`, code: 403},
		{name: "missing value", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{}`, code: 400},
		{name: "null value", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":null}`, code: 400},
		{name: "wrong type", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":"true"}`, code: 400},
		{name: "unknown option", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":true,"path":"/tmp"}`, code: 400},
		{name: "trailing data", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":true}{}`, code: 400},
		{name: "oversized", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":true}` + strings.Repeat(" ", 1100), code: 400},
		{name: "unsupported", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":true}`, disabled: "unsupported", code: 503},
		{name: "OS failure", method: "PUT", remote: "127.0.0.1:123", action: "autostart", body: `{"enabled":true}`, fail: true, code: 500, writes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeStartup{enabled: tc.enabled}
			if tc.fail {
				fake.writeErr = errors.New("access denied")
			}
			s := &server{autostart: &autostartService{backend: fake, disabled: tc.disabled}}
			r := httptest.NewRequest(tc.method, "http://localhost:8080/api/settings/autostart", strings.NewReader(tc.body))
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Lan-Copy-Action", tc.action)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			w := httptest.NewRecorder()
			s.autostartSettings(w, r)
			if w.Code != tc.code || fake.calls != tc.writes {
				t.Fatalf("status=%d, calls=%d, body=%s", w.Code, fake.calls, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("settings must not be cached")
			}
			if tc.code == http.StatusOK {
				var state autostartState
				if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
					t.Fatal(err)
				}
				wantEnabled := tc.enabled
				if tc.method == http.MethodPut {
					wantEnabled = strings.Contains(tc.body, ":true")
				}
				if state.Available != isLocalRequest(r) || state.Enabled != wantEnabled {
					t.Fatalf("unexpected state: %+v", state)
				}
				if !isLocalRequest(r) && !strings.Contains(state.Reason, "仅运行 Lan Copy 的电脑可以修改") {
					t.Fatalf("missing read-only explanation: %+v", state)
				}
			}
		})
	}
}

func TestAutostartUnavailable(t *testing.T) {
	for _, a := range []*autostartService{nil, {disabled: "unsupported"}, {backend: &fakeStartup{readErr: errors.New("denied")}}} {
		state := a.state()
		if state.Available || state.Enabled || state.Reason == "" {
			t.Fatalf("unexpected state: %+v", state)
		}
	}
}

func TestStartupSavedArguments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	path, err := startupConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	expected := []string{"--autostart", "--working-dir", filepath.Join(root, "工作 & 目录"), "-dir", filepath.Join(root, "shared files"), "-listen", "127.0.0.1:9876", "-max-gb", "3"}
	data, _ := json.Marshal(startupSpec{Args: expected})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := startupLaunchArgs()
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("args=%q err=%v", actual, err)
	}
	for _, data := range []string{`{`, `{}`, `{"Args":["--internal-autostart"]}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := startupLaunchArgs(); err == nil {
			t.Fatal("invalid startup config accepted")
		}
	}
}
