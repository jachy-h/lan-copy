package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const uploadResourceConfigName = "upload-resources.json"
const uploadResourceConfigBackupName = ".upload-resources-backup.json"
const gib = int64(1 << 30)

type uploadResourceConfig struct {
	Concurrent  int   `json:"concurrentUploads"`
	StagingGiB  int64 `json:"stagingGiB"`
	IdleSeconds int   `json:"idleTimeoutSeconds"`
}

var defaultUploadResourceConfig = uploadResourceConfig{Concurrent: 3, StagingGiB: 20, IdleSeconds: 60}

func (c uploadResourceConfig) valid() bool {
	return c.Concurrent >= 1 && c.Concurrent <= 32 && c.StagingGiB >= 1 && c.StagingGiB <= 1024 && c.IdleSeconds >= 5 && c.IdleSeconds <= 600
}

func loadUploadResourceConfig(dataDir string) (uploadResourceConfig, error) {
	name := filepath.Join(dataDir, uploadResourceConfigName)
	backup := filepath.Join(dataDir, uploadResourceConfigBackupName)
	if _, statErr := os.Lstat(name); errors.Is(statErr, os.ErrNotExist) {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			if err := os.Rename(backup, name); err != nil {
				return uploadResourceConfig{}, err
			}
			if err := syncDirectory(dataDir); err != nil {
				return uploadResourceConfig{}, err
			}
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return uploadResourceConfig{}, backupErr
		}
	} else if statErr != nil {
		return uploadResourceConfig{}, statErr
	} else if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return uploadResourceConfig{}, err
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return defaultUploadResourceConfig, nil
	}
	if err != nil {
		return uploadResourceConfig{}, err
	}
	var config uploadResourceConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return config, errors.New("trailing configuration data")
	}
	if !config.valid() {
		return config, errors.New("upload resource configuration is out of range")
	}
	return config, nil
}

func saveUploadResourceConfig(dataDir string, config uploadResourceConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dataDir, ".upload-resources-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	destination := filepath.Join(dataDir, uploadResourceConfigName)
	backup := filepath.Join(dataDir, uploadResourceConfigBackupName)
	oldExists := false
	if _, statErr := os.Lstat(destination); statErr == nil {
		if err = os.Rename(destination, backup); err != nil {
			return err
		}
		oldExists = true
		if err = syncDirectory(dataDir); err != nil {
			return errors.Join(err, os.Rename(backup, destination), syncDirectory(dataDir))
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err = os.Rename(name, destination); err != nil {
		if oldExists {
			return errors.Join(err, os.Rename(backup, destination), syncDirectory(dataDir))
		}
		return err
	}
	if err = syncDirectory(dataDir); err != nil {
		rollbackErr := os.Remove(destination)
		if oldExists {
			rollbackErr = errors.Join(rollbackErr, os.Rename(backup, destination))
		}
		return errors.Join(err, rollbackErr, syncDirectory(dataDir))
	}
	if oldExists {
		if err = os.Remove(backup); err != nil {
			return err
		}
		return syncDirectory(dataDir)
	}
	return nil
}

type uploadResourceManager struct {
	mu     sync.Mutex
	config uploadResourceConfig
	active int
	staged int64
}

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

func newUploadResourceManager(config uploadResourceConfig) *uploadResourceManager {
	return &uploadResourceManager{config: config}
}

func (m *uploadResourceManager) begin() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active >= m.config.Concurrent {
		return false
	}
	m.active++
	return true
}

func (m *uploadResourceManager) end() { m.mu.Lock(); m.active--; m.mu.Unlock() }

func (m *uploadResourceManager) reserve(bytes int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bytes < 0 || bytes > m.config.StagingGiB*gib-m.staged {
		return false
	}
	m.staged += bytes
	return true
}

func (m *uploadResourceManager) reserveUpTo(bytes int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	available := m.config.StagingGiB*gib - m.staged
	if available <= 0 || bytes <= 0 {
		return 0
	}
	if bytes > available {
		bytes = available
	}
	m.staged += bytes
	return bytes
}

func (m *uploadResourceManager) release(bytes int64) {
	m.mu.Lock()
	m.staged -= bytes
	if m.staged < 0 {
		m.staged = 0
	}
	m.mu.Unlock()
}

func (m *uploadResourceManager) snapshot() uploadResourceConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

func (m *uploadResourceManager) configure(dataDir string, config uploadResourceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := saveUploadResourceConfig(dataDir, config); err != nil {
		return err
	}
	m.config = config
	return nil
}

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

func (s *server) uploadResourceSettings(w http.ResponseWriter, r *http.Request) {
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
		var config uploadResourceConfig
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			writeError(w, http.StatusBadRequest, "上传资源设置格式无效")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || !config.valid() {
			writeError(w, http.StatusBadRequest, "上传资源设置超出允许范围")
			return
		}
		if err := s.uploadResources.configure(s.dataDir, config); err != nil {
			writeError(w, http.StatusInternalServerError, "无法保存上传资源设置")
			return
		}
	}
	config := s.uploadResources.snapshot()
	if !isLocalRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"config": config, "editable": false, "reason": "仅运行 Lan Copy 的电脑可以修改。"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": config, "editable": true})
}
