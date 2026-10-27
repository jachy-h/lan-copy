package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lan-copy/internal/printing"
)

type printingBackend struct{}

func (printingBackend) Printers() ([]printing.Printer, error) {
	return []printing.Printer{{Name: "Office USB", Default: true}}, nil
}
func (printingBackend) PrintTest(string) (int, error) { return 7, nil }

func TestPrintingHTTP(t *testing.T) {
	s := New(Config{Printing: newPrintingService(t, printingBackend{})})
	handler := s.Handler()
	for _, tc := range []struct {
		body, action, origin, site string
		want                       int
	}{
		{`{"printer":"Office USB"}`, "", "", "", 403},
		{`{"printer":"Office USB"}`, "print-test", "http://evil.test", "", 403},
		{`{"printer":"Office USB"}`, "print-test", "", "cross-site", 403},
		{`{"printer":""}`, "print-test", "", "", 400},
		{`{"printer":"Office USB","file":"anything"}`, "print-test", "", "", 400},
		{`{"printer":"Office USB"} {}`, "print-test", "", "", 400},
		{`{"printer":"Office\u0000USB"}`, "print-test", "", "", 400},
		{`{"printer":"Office USB"}`, "print-test", "http://lan-copy.test", "same-origin", 202},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://lan-copy.test/api/printing/test", strings.NewReader(tc.body))
		r.RemoteAddr = "192.168.1.20:5000"
		r.Header.Set("X-Lan-Copy-Action", tc.action)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/printing", "/api/printing/jobs"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestPrintingUnavailable(t *testing.T) {
	s := New(Config{})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/printing", nil))
	var state struct {
		Available bool
		Printers  []printing.Printer
		Reason    string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Available || state.Printers == nil || state.Reason == "" {
		t.Fatalf("state=%s err=%v", w.Body.String(), err)
	}
	r := httptest.NewRequest("POST", "/api/printing/test", strings.NewReader(`{"printer":"Office USB"}`))
	r.Header.Set("X-Lan-Copy-Action", "print-test")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", w.Code)
	}
}

func newPrintingService(t *testing.T, backend printing.Backend) *printing.Service {
	t.Helper()
	service, err := printing.NewWithBackend(backend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return service
}
