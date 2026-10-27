package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"lan-copy/internal/printing"
)

type documentHTTPBackend struct{ calls atomic.Int32 }

func (*documentHTTPBackend) Printers() ([]printing.Printer, error) {
	return []printing.Printer{{Name: "printer"}}, nil
}
func (*documentHTTPBackend) PrintTest(string) (int, error) { return 1, nil }
func (*documentHTTPBackend) Layout(string) (printing.Layout, error) {
	return printing.Layout{Width: 25.4, Height: 25.4, PrintableWidth: 2.54, PrintableHeight: 2.54, Key: "layout"}, nil
}
func (b *documentHTTPBackend) PrintDocument(_ string, document printing.Document) (int, error) {
	for _, page := range document.Pages {
		if _, err := os.Stat(page); err != nil {
			return 0, err
		}
	}
	b.calls.Add(1)
	return 123, nil
}
func printUpload(t *testing.T, filename, key string, count int, corrupt bool) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	field, err := writer.CreateFormField("manifest")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(field).Encode(map[string]any{"printer": "printer", "name": filename, "layoutKey": key, "pages": count}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		part, err := writer.CreateFormFile("page", fmt.Sprintf("%03d.png", i+1))
		if err != nil {
			t.Fatal(err)
		}
		if corrupt && i == count-1 {
			io.WriteString(part, "broken")
		} else if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 20, 20))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}
func TestPrintDocumentHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, key, action string
		pages             int
		corrupt           bool
		want              int
	}{
		{"photo.png", "layout", "", 1, false, 403},
		{"report.docx", "layout", "print-document", 1, false, 400},
		{"report.pdf", "stale", "print-document", 2, false, 409},
		{"report.pdf", "layout", "print-document", 2, true, 400},
		{"report.pdf", "layout", "print-document", 2, false, 202},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.name, tc.want), func(t *testing.T) {
			backend := &documentHTTPBackend{}
			s := New(Config{Printing: newPrintingService(t, backend)})
			body, contentType := printUpload(t, tc.name, tc.key, tc.pages, tc.corrupt)
			request := httptest.NewRequest("POST", "http://lan.test/api/printing/documents", body)
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("X-Lan-Copy-Action", tc.action)
			request.Header.Set("Origin", "http://lan.test")
			request.RemoteAddr = "192.168.1.30:5000"
			result := httptest.NewRecorder()
			s.Handler().ServeHTTP(result, request)
			if result.Code != tc.want {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
			if tc.want == 202 {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					jobs := s.printing.Jobs()
					if len(jobs) == 1 && jobs[0].Status == "submitted" {
						break
					}
					time.Sleep(time.Millisecond)
				}
				jobs := s.printing.Jobs()
				if backend.calls.Load() != 1 || len(jobs) != 1 || jobs[0].SystemID != 123 || jobs[0].Status != "submitted" {
					t.Fatalf("calls=%d jobs=%+v", backend.calls.Load(), jobs)
				}
			} else if backend.calls.Load() != 0 {
				t.Fatal("rejected upload printed")
			}
		})
	}
}
func TestPrintLayoutHTTP(t *testing.T) {
	s := New(Config{Printing: newPrintingService(t, &documentHTTPBackend{})})
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 400}, {"?printer=printer", 200}, {"?printer=missing", 404}, {"?printer=%00", 400}} {
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, httptest.NewRequest("GET", "/api/printing/layout"+tc.query, nil))
		if result.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.query, result.Code, result.Body.String())
		}
	}
}
