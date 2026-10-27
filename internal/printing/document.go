package printing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"lan-copy/internal/filestore"
)

const (
	MaxDocumentPages = 50
	MaxDocumentBytes = 128 << 20
	MaxPageBytes     = 12 << 20
	MaxPagePixels    = 16_000_000
	RasterDPI        = 200
)

var (
	ErrDocument      = errors.New("无效的打印文档")
	ErrTooLarge      = errors.New("打印文档超过大小、页数或像素限制")
	ErrLayoutChanged = errors.New("打印机纸张或设置已变化，请刷新打印预览")
)

// Layout describes the physical paper and its printable rectangle in mm.
// Key binds the preview to the exact device metrics, not a client-selected size.
type Layout struct {
	Width           float64 `json:"width"`
	Height          float64 `json:"height"`
	Left            float64 `json:"left"`
	Top             float64 `json:"top"`
	PrintableWidth  float64 `json:"printableWidth"`
	PrintableHeight float64 `json:"printableHeight"`
	Key             string  `json:"key"`
}

func (l Layout) valid() bool {
	for _, v := range []float64{l.Width, l.Height, l.Left, l.Top, l.PrintableWidth, l.PrintableHeight} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return l.Width > 0 && l.Height > 0 && l.Left >= 0 && l.Top >= 0 && l.PrintableWidth > 0 && l.PrintableHeight > 0 && l.Left+l.PrintableWidth <= l.Width+0.1 && l.Top+l.PrintableHeight <= l.Height+0.1 && l.Key != ""
}

func layoutKey(values ...int) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(values)))
	return hex.EncodeToString(sum[:])
}

func (l Layout) RasterSize() (int, int) {
	return int(math.Round(l.PrintableWidth * RasterDPI / 25.4)), int(math.Round(l.PrintableHeight * RasterDPI / 25.4))
}

type Document struct {
	Name   string
	Pages  []string
	Layout Layout
}

type documentBackend interface {
	Layout(string) (Layout, error)
	PrintDocument(string, Document) (int, error)
}

func (s *Service) Layout(printer string) (Layout, error) {
	if s == nil {
		return Layout{}, ErrUnavailable
	}
	backend, ok := s.backend.(documentBackend)
	if !ok {
		return Layout{}, ErrUnavailable
	}
	printers, err := s.Printers()
	if err != nil {
		return Layout{}, err
	}
	found := false
	for _, p := range printers {
		if p.Name == printer {
			found = true
			break
		}
	}
	if !found {
		return Layout{}, ErrNotFound
	}
	layout, err := backend.Layout(printer)
	if err != nil {
		return Layout{}, err
	}
	if !layout.valid() {
		return Layout{}, fmt.Errorf("无法读取有效的打印版面")
	}
	w, h := layout.RasterSize()
	if w < 1 || h < 1 || int64(w)*int64(h) > MaxPagePixels {
		return Layout{}, ErrTooLarge
	}
	return layout, nil
}

// Draft owns the temporary pages and the single submission slot. Abort is
// mandatory until Commit transfers ownership to the asynchronous print job.
// A Draft is used sequentially by a single upload request.
type Draft struct {
	service   *Service
	job       Job
	directory string
	document  Document
	expected  int
	bytes     int64
	finished  bool
}

func (s *Service) BeginDocument(printer, name, key string, pages int) (*Draft, error) {
	safe, err := filestore.SafeName(name)
	if err != nil || safe != name {
		return nil, ErrDocument
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
	case ".jpg", ".jpeg", ".png":
		if pages != 1 {
			return nil, ErrDocument
		}
	default:
		return nil, ErrDocument
	}
	if pages < 1 || pages > MaxDocumentPages {
		return nil, ErrTooLarge
	}
	if s == nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	s.busy = true
	s.mu.Unlock()
	release := true
	defer func() {
		if release {
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
		}
	}()
	layout, err := s.Layout(printer)
	if err != nil {
		return nil, err
	}
	if key != layout.Key {
		return nil, ErrLayoutChanged
	}
	if s.spoolRoot == "" {
		return nil, fmt.Errorf("打印暂存目录未配置")
	}
	dir, err := os.MkdirTemp(s.spoolRoot, ".pages-")
	if err != nil {
		return nil, err
	}
	job := s.record(printer, name, pages, "preparing")
	release = false
	return &Draft{service: s, job: job, directory: dir, document: Document{Name: name, Layout: layout}, expected: pages}, nil
}

func (d *Draft) StagePage(source io.Reader) error {
	if d.finished || len(d.document.Pages) >= d.expected {
		return ErrDocument
	}
	name := filepath.Join(d.directory, fmt.Sprintf("%03d.png", len(d.document.Pages)+1))
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	n, err := io.Copy(file, io.LimitReader(source, MaxPageBytes+1))
	if err != nil {
		return err
	}
	d.bytes += n
	if n > MaxPageBytes || d.bytes > MaxDocumentBytes {
		return ErrTooLarge
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	config, err := png.DecodeConfig(file)
	if err != nil {
		return ErrDocument
	}
	width, height := d.document.Layout.RasterSize()
	if config.Width != width || config.Height != height || int64(config.Width)*int64(config.Height) > MaxPagePixels {
		return ErrDocument
	}
	// Decode every page before any physical printing. A valid header alone is
	// insufficient: truncated image data must not produce partial documents.
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err = png.Decode(file); err != nil {
		return ErrDocument
	}
	if err = file.Close(); err != nil {
		return err
	}
	d.document.Pages = append(d.document.Pages, name)
	return nil
}

func (d *Draft) Abort() {
	if d.finished {
		return
	}
	d.finished = true
	err := os.RemoveAll(d.directory)
	message := fmt.Errorf("文档接收未完成，未发送到打印机")
	if err != nil {
		message = fmt.Errorf("文档接收未完成；清理暂存页失败：%w", err)
	}
	d.service.update(d.job.ID, "failed", 0, message)
}

func (d *Draft) Commit() (Job, error) {
	if d.finished || len(d.document.Pages) != d.expected {
		return Job{}, ErrDocument
	}
	d.finished = true
	d.service.update(d.job.ID, "queued", 0, nil)
	job := d.job
	job.Status = "queued"
	go func() {
		s := d.service
		s.update(job.ID, "processing", 0, nil)
		backend := s.backend.(documentBackend)
		layout, err := s.Layout(job.Printer) // Recheck device availability after receiving.
		if err == nil && layout.Key != d.document.Layout.Key {
			err = ErrLayoutChanged
		}
		var systemID int
		if err == nil {
			systemID, err = backend.PrintDocument(job.Printer, d.document)
		}
		cleanupErr := os.RemoveAll(d.directory)
		status := "submitted"
		if err != nil {
			status = "failed"
		}
		// Cleanup errors do not turn a successful physical submission into a failure
		// that might encourage a duplicate print. They are shown as a separate note.
		if cleanupErr != nil {
			s.mu.Lock()
			for i := range s.jobs {
				if s.jobs[i].ID == job.ID {
					s.jobs[i].Warning = "清理暂存页失败，将在下次启动时重试"
				}
			}
			s.mu.Unlock()
		}
		s.update(job.ID, status, systemID, err)
	}()
	return job, nil
}

var pageFile = regexp.MustCompile(`^[0-9]{3}\.png$`)

// NewWithBackend prepares a private, component-owned directory. Incomplete jobs
// are discarded on startup and never replayed (they may already have printed).
func NewWithBackend(backend Backend, dataDir string) (*Service, error) {
	root := filepath.Join(dataDir, "print-spool")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("打印暂存目录无效")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".pages-") || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		target := filepath.Join(root, entry.Name())
		pages, err := os.ReadDir(target)
		if err != nil {
			return nil, err
		}
		owned := true
		for _, page := range pages {
			if !pageFile.MatchString(page.Name()) || !page.Type().IsRegular() {
				owned = false
				break
			}
		}
		if owned {
			if err := os.RemoveAll(target); err != nil {
				return nil, err
			}
		}
	}
	s := &Service{backend: backend, spoolRoot: root}
	return s, nil
}
