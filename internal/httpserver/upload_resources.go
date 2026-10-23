package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"lan-copy/internal/filestore"
)

var errUploadIdleTimeout = errors.New("upload request body idle timeout")

type idleTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	expired atomic.Bool
}

func newIdleTimeoutBody(body io.ReadCloser, timeout time.Duration) *idleTimeoutBody {
	b := &idleTimeoutBody{body: body, timeout: timeout}
	b.timer = time.AfterFunc(timeout, func() { b.expired.Store(true); _ = body.Close() })
	return b
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if b.expired.Load() {
		return n, errUploadIdleTimeout
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error { b.timer.Stop(); return b.body.Close() }

func validUploadResourceRequest(r *http.Request) bool {
	if !isLocalRequest(r) || r.Header.Get("X-Lan-Copy-Action") != "upload-resources" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
	}
	return true
}

func (s *Server) uploadResourceSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut && !validUploadResourceRequest(r) {
		writeError(w, http.StatusForbidden, "请在运行 Lan Copy 的电脑上从本机设置页面修改上传资源限制")
		return
	}
	if s.uploadResources == nil {
		writeError(w, http.StatusServiceUnavailable, "上传资源设置不可用")
		return
	}
	if r.Method == http.MethodPut {
		var config filestore.ResourceConfig
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			writeError(w, http.StatusBadRequest, "上传资源设置格式无效")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || !config.Valid() {
			writeError(w, http.StatusBadRequest, "上传资源设置超出允许范围")
			return
		}
		if err := s.uploadResources.Configure(s.dataDir, config); err != nil {
			writeError(w, http.StatusInternalServerError, "无法保存上传资源设置")
			return
		}
	}
	config := s.uploadResources.Snapshot()
	if !isLocalRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"config": config, "editable": false, "reason": "仅运行 Lan Copy 的电脑可以修改。"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config, "editable": true})
}
