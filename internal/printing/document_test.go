package printing

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type documentTestBackend struct {
	layout        Layout
	printed       atomic.Int32
	fail          bool
	observedPages atomic.Int32
}

func (b *documentTestBackend) Printers() ([]Printer, error)  { return []Printer{{Name: "printer"}}, nil }
func (b *documentTestBackend) PrintTest(string) (int, error) { return 1, nil }
func (b *documentTestBackend) Layout(string) (Layout, error) { return b.layout, nil }
func (b *documentTestBackend) PrintDocument(_ string, d Document) (int, error) {
	b.printed.Add(1)
	for _, page := range d.Pages {
		if _, err := os.Stat(page); err != nil {
			return 0, err
		}
	}
	b.observedPages.Store(int32(len(d.Pages)))
	if b.fail {
		return 0, errors.New("driver failed")
	}
	return 77, nil
}
func testLayout() Layout {
	return Layout{Width: 25.4, Height: 25.4, PrintableWidth: 2.54, PrintableHeight: 2.54, Key: "test"}
}
func pageBytes(t *testing.T, layout Layout) []byte {
	t.Helper()
	w, h := layout.RasterSize()
	pic := image.NewRGBA(image.Rect(0, 0, w, h))
	pic.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, pic); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func documentService(t *testing.T) (*Service, *documentTestBackend) {
	t.Helper()
	backend := &documentTestBackend{layout: testLayout()}
	s, err := NewWithBackend(backend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s, backend
}
func assertEmptySpool(t *testing.T, s *Service) {
	t.Helper()
	entries, err := os.ReadDir(s.spoolRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spool: %v %v", entries, err)
	}
}
func TestDocumentCommitAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "driver failure"}[fail], func(t *testing.T) {
			s, b := documentService(t)
			b.fail = fail
			draft, err := s.BeginDocument("printer", "report.pdf", "test", 2)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Test("printer"); !errors.Is(err, ErrBusy) {
				t.Fatalf("busy: %v", err)
			}
			data := pageBytes(t, b.layout)
			if err = draft.StagePage(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			if _, err = draft.Commit(); !errors.Is(err, ErrDocument) {
				t.Fatalf("incomplete: %v", err)
			}
			if b.printed.Load() != 0 {
				t.Fatal("printed incomplete document")
			}
			if err = draft.StagePage(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			job, err := draft.Commit()
			if err != nil {
				t.Fatal(err)
			}
			draft.Abort() // Ownership already transferred; must not remove active pages.
			result := awaitJob(t, s, job.ID)
			if (result.Status == "failed") != fail || b.printed.Load() != 1 || b.observedPages.Load() != 2 || result.Name != "report.pdf" || result.Pages != 2 {
				t.Fatalf("result=%+v", result)
			}
			assertEmptySpool(t, s)
		})
	}
}
func TestDocumentRejectsInvalidInput(t *testing.T) {
	s, b := documentService(t)
	for _, tc := range []struct {
		name, key, printer string
		pages              int
		want               error
	}{
		{"../photo.jpg", "test", "printer", 1, ErrDocument},
		{"photo.docx", "test", "printer", 1, ErrDocument},
		{"photo.png", "test", "printer", 2, ErrDocument},
		{"report.pdf", "test", "printer", 51, ErrTooLarge},
		{"report.pdf", "old", "printer", 1, ErrLayoutChanged},
		{"report.pdf", "test", "missing", 1, ErrNotFound},
	} {
		if _, err := s.BeginDocument(tc.printer, tc.name, tc.key, tc.pages); !errors.Is(err, tc.want) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
	data := pageBytes(t, b.layout)
	wrong := testLayout()
	wrong.PrintableWidth = 5.08
	for _, payload := range [][]byte{[]byte("not PNG"), data[:24], pageBytes(t, wrong), []byte(strings.Repeat("x", MaxPageBytes+1))} {
		draft, err := s.BeginDocument("printer", "photo.png", "test", 1)
		if err != nil {
			t.Fatal(err)
		}
		if err = draft.StagePage(bytes.NewReader(payload)); err == nil {
			t.Fatal("invalid raster accepted")
		}
		draft.Abort()
		assertEmptySpool(t, s)
	}
	if b.printed.Load() != 0 {
		t.Fatal("invalid data reached printer")
	}
}
func TestSpoolStartupCleanupNeverReplays(t *testing.T) {
	dir := t.TempDir()
	s, err := NewWithBackend(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(s.spoolRoot, ".pages-abandoned")
	unknown := filepath.Join(s.spoolRoot, ".pages-unknown")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "001.png"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "personal.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewWithBackend(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("abandoned pages remain")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown directory removed")
	}
	if len(restarted.Jobs()) != 0 {
		t.Fatal("jobs replayed after startup")
	}
}
func TestLayoutValidation(t *testing.T) {
	s, b := documentService(t)
	b.layout.PrintableWidth = 1000
	if _, err := s.Layout("printer"); err == nil {
		t.Fatal("invalid layout accepted")
	}
	b.layout = Layout{Width: 2000, Height: 2000, PrintableWidth: 2000, PrintableHeight: 2000, Key: "huge"}
	if _, err := s.Layout("printer"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large: %v", err)
	}
}

func TestDocumentRechecksLayoutAndAggregateSize(t *testing.T) {
	s, b := documentService(t)
	draft, err := s.BeginDocument("printer", "photo.png", "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	data := pageBytes(t, b.layout)
	if err = draft.StagePage(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	b.layout.Key = "new"
	job, err := draft.Commit()
	if err != nil {
		t.Fatal(err)
	}
	result := awaitJob(t, s, job.ID)
	if result.Status != "failed" || result.Error != ErrLayoutChanged.Error() || b.printed.Load() != 0 {
		t.Fatalf("result=%+v", result)
	}
	assertEmptySpool(t, s)
	draft, err = s.BeginDocument("printer", "photo.png", "new", 1)
	if err != nil {
		t.Fatal(err)
	}
	draft.bytes = MaxDocumentBytes
	if err = draft.StagePage(bytes.NewReader(data)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("aggregate limit: %v", err)
	}
	draft.Abort()
	assertEmptySpool(t, s)
}
