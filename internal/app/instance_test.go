package app

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInstanceURL(t *testing.T) {
	for _, tc := range []struct{ listen, want string }{
		{":8080", "http://127.0.0.1:8080"}, {"0.0.0.0:8080", "http://127.0.0.1:8080"},
		{"127.0.0.1:9123", "http://127.0.0.1:9123"}, {"[::]:8080", "http://[::1]:8080"},
		{"[::1]:9123", "http://[::1]:9123"}, {"192.168.1.2:8080", "http://192.168.1.2:8080"},
	} {
		got, err := instanceURL(tc.listen)
		if err != nil || got != tc.want {
			t.Errorf("%s: %s, %v", tc.listen, got, err)
		}
	}
	for _, listen := range []string{"invalid", ":0", ":", "http://localhost:8080"} {
		if _, err := instanceURL(listen); err == nil {
			t.Errorf("accepted %s", listen)
		}
	}
}

func TestRepeatedLaunchOpensExistingInstance(t *testing.T) {
	existing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Lan-Copy-Service", "lan-copy")
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer existing.Close()
	var opened []string
	found, err := OpenRunningInstance(strings.TrimPrefix(existing.URL, "http://"), func(address string) error { opened = append(opened, address); return nil })
	if err != nil || !found || len(opened) != 1 || opened[0] != existing.URL {
		t.Fatalf("found=%v opened=%v err=%v", found, opened, err)
	}
	// Browser failure must not start another server or hide a genuine instance.
	browserError := errors.New("no default browser")
	found, err = OpenRunningInstance(strings.TrimPrefix(existing.URL, "http://"), func(string) error { return browserError })
	if !found || !errors.Is(err, browserError) {
		t.Fatalf("browser failure lost: %v %v", found, err)
	}
}

func TestRepeatedLaunchRejectsOtherServices(t *testing.T) {
	for _, tc := range []struct {
		name, marker, body string
		status             int
	}{
		{"generic health", "", "ok", 200}, {"wrong product", "another-app", "ok", 200},
		{"wrong body", "lan-copy", "not ready", 200}, {"error status", "lan-copy", "ok", 500},
		{"redirect", "lan-copy", "ok", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Lan-Copy-Service", tc.marker)
				if tc.status == 302 {
					w.Header().Set("Location", "/redirect")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer existing.Close()
			called := false
			found, err := OpenRunningInstance(strings.TrimPrefix(existing.URL, "http://"), func(string) error { called = true; return nil })
			if found || called || err != nil {
				t.Fatalf("opened another service: %v %v %v", found, called, err)
			}
		})
	}
}

func TestRepeatedLaunchWaitsForFirstStartup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	existing := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Lan-Copy-Service", "lan-copy")
		_, _ = w.Write([]byte("ok\n"))
	}))
	_ = existing.Listener.Close()
	existing.Listener = listener
	started := make(chan struct{})
	go func() { time.Sleep(450 * time.Millisecond); existing.Start(); close(started) }()
	called := false
	found, err := OpenRunningInstance(listener.Addr().String(), func(string) error { called = true; return nil })
	<-started
	defer existing.Close()
	if err != nil || !found || !called {
		t.Fatalf("concurrent startup failed: %v %v %v", found, called, err)
	}
}
