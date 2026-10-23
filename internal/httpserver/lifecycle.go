package httpserver

import (
	"log"
	"net/http"
	"sync"
)

type transferKind uint8

const (
	transferUpload transferKind = iota
	transferDownload
)

type transferCounts struct {
	Uploads   int `json:"uploads"`
	Downloads int `json:"downloads"`
}

func (c transferCounts) total() int { return c.Uploads + c.Downloads }

type transferTracker struct {
	mu       sync.Mutex
	counts   transferCounts
	stopping bool
}

func (t *transferTracker) begin(kind transferKind) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping {
		return false
	}
	if kind == transferUpload {
		t.counts.Uploads++
	} else {
		t.counts.Downloads++
	}
	return true
}

func (t *transferTracker) end(kind transferKind) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if kind == transferUpload {
		t.counts.Uploads--
	} else {
		t.counts.Downloads--
	}
}

func (t *transferTracker) snapshot() (transferCounts, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts, t.stopping
}

func (t *transferTracker) beginShutdown(confirmed bool, expected *transferCounts) (transferCounts, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping {
		return t.counts, true
	}
	if expected != nil && *expected != t.counts {
		return t.counts, false
	}
	if t.counts.total() > 0 && !confirmed {
		return t.counts, false
	}
	t.stopping = true
	return t.counts, true
}

func (t *transferTracker) stop() transferCounts {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopping = true
	return t.counts
}

func (s *Server) trackTransfer(kind transferKind, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.transfers.begin(kind) {
			writeError(w, http.StatusServiceUnavailable, "服务正在关闭，无法开始新的文件传输")
			return
		}
		defer s.transfers.end(kind)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestShutdown(reason string) {
	counts := s.transfers.stop()
	s.shutdownOnce.Do(func() {
		s.shutdownReason = reason
		s.cancelApp()
		close(s.shutdownRequested)
		log.Printf("开始关停（%s）；在途上传 %d 个、下载 %d 个", reason, counts.Uploads, counts.Downloads)
	})
}
