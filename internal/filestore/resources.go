package filestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const ResourceConfigName = "upload-resources.json"
const ResourceConfigBackupName = ".upload-resources-backup.json"
const gib = int64(1 << 30)

type ResourceConfig struct {
	Concurrent  int   `json:"concurrentUploads"`
	StagingGiB  int64 `json:"stagingGiB"`
	IdleSeconds int   `json:"idleTimeoutSeconds"`
}

var DefaultResourceConfig = ResourceConfig{Concurrent: 3, StagingGiB: 20, IdleSeconds: 60}

func (c ResourceConfig) Valid() bool {
	return c.Concurrent >= 1 && c.Concurrent <= 32 && c.StagingGiB >= 1 && c.StagingGiB <= 1024 && c.IdleSeconds >= 5 && c.IdleSeconds <= 600
}

func LoadResourceConfig(dataDir string) (ResourceConfig, error) {
	name := filepath.Join(dataDir, ResourceConfigName)
	backup := filepath.Join(dataDir, ResourceConfigBackupName)
	if _, statErr := os.Lstat(name); errors.Is(statErr, os.ErrNotExist) {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			if err := os.Rename(backup, name); err != nil {
				return ResourceConfig{}, err
			}
			if err := syncDirectory(dataDir); err != nil {
				return ResourceConfig{}, err
			}
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return ResourceConfig{}, backupErr
		}
	} else if statErr != nil {
		return ResourceConfig{}, statErr
	} else if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ResourceConfig{}, err
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultResourceConfig, nil
	}
	if err != nil {
		return ResourceConfig{}, err
	}
	var config ResourceConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return config, errors.New("trailing configuration data")
	}
	if !config.Valid() {
		return config, errors.New("upload resource configuration is out of range")
	}
	return config, nil
}

func SaveResourceConfig(dataDir string, config ResourceConfig) error {
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
	destination := filepath.Join(dataDir, ResourceConfigName)
	backup := filepath.Join(dataDir, ResourceConfigBackupName)
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

type Resources struct {
	mu     sync.Mutex
	config ResourceConfig
	active int
	staged int64
}

func NewResources(config ResourceConfig) *Resources {
	return &Resources{config: config}
}

func (m *Resources) Begin() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active >= m.config.Concurrent {
		return false
	}
	m.active++
	return true
}

func (m *Resources) End() { m.mu.Lock(); m.active--; m.mu.Unlock() }

func (m *Resources) Reserve(bytes int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bytes < 0 || bytes > m.config.StagingGiB*gib-m.staged {
		return false
	}
	m.staged += bytes
	return true
}

func (m *Resources) ReserveUpTo(bytes int64) int64 {
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

func (m *Resources) Release(bytes int64) {
	m.mu.Lock()
	m.staged -= bytes
	if m.staged < 0 {
		m.staged = 0
	}
	m.mu.Unlock()
}

func (m *Resources) Snapshot() ResourceConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

func (m *Resources) Configure(dataDir string, config ResourceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := SaveResourceConfig(dataDir, config); err != nil {
		return err
	}
	m.config = config
	return nil
}
