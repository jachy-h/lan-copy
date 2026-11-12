package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"

	"lan-copy/internal/deploytoken"
	"lan-copy/internal/updater"
)

func newDeployAuth(t *testing.T, token string) *deploytoken.Store {
	t.Helper()
	store, err := deploytoken.New(t.TempDir(), token)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestDeployAccess(t *testing.T) {
	for _, tc := range []struct {
		name, token, auth, method string
		want                      int
	}{
		{"disabled", "", "Bearer secret", "POST", 503},
		{"missing", "secret", "", "POST", 401},
		{"wrong", "secret", "Bearer wrong", "POST", 401},
		{"scheme required", "secret", "secret", "POST", 401},
		{"authenticated shares update restrictions", "secret", "Bearer secret", "POST", 503},
		{"method", "secret", "Bearer secret", "GET", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := newServer(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			s.deployAuth = newDeployAuth(t, tc.token)
			s.updater = updater.New(updater.Config{Disabled: "updates disabled"})
			r := httptest.NewRequest(tc.method, "/api/deploy", strings.NewReader("bad archive"))
			r.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			select {
			case <-s.shutdownRequested:
				t.Fatal("rejected deployment stopped service")
			default:
			}
		})
	}
}
