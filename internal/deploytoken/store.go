// Package deploytoken persists the bearer token used by the local deployment entry.
package deploytoken

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	FileName       = "deploy-token.json"
	maxTokenLength = 512
)

var ErrInvalidToken = errors.New("部署令牌必须为 1–512 个可打印 ASCII 字符且不含空格")

type fileState struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

type Status struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
}

// Store keeps only the SHA-256 digest of the bearer authorization value. The
// environment token remains a fallback when no token has been saved.
type Store struct {
	mu             sync.RWMutex
	path           string
	envHash        [32]byte
	envConfigured  bool
	fileHash       [32]byte
	fileConfigured bool
}

func New(dataDir, environmentToken string) (*Store, error) {
	store := &Store{path: filepath.Join(dataDir, FileName)}
	if environmentToken != "" {
		if !Valid(environmentToken) {
			return nil, ErrInvalidToken
		}
		store.envHash = digest(environmentToken)
		store.envConfigured = true
	}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var state fileState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected deploy token data after JSON value")
	}
	if state.Version != 1 {
		return nil, errors.New("unsupported deploy token state version")
	}
	hash, err := hex.DecodeString(state.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return nil, errors.New("invalid deploy token digest")
	}
	copy(store.fileHash[:], hash)
	store.fileConfigured = true
	return store, nil
}

func Valid(token string) bool {
	if token == "" || len(token) > maxTokenLength {
		return false
	}
	for index := 0; index < len(token); index++ {
		if token[index] < 33 || token[index] > 126 {
			return false
		}
	}
	return true
}

func digest(token string) [32]byte {
	return sha256.Sum256([]byte("Bearer " + token))
}

func (s *Store) Configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileConfigured || s.envConfigured
}

func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.fileConfigured:
		return Status{Configured: true, Source: "file"}
	case s.envConfigured:
		return Status{Configured: true, Source: "environment"}
	default:
		return Status{Configured: false, Source: "none"}
	}
}

func (s *Store) Match(authorization string) bool {
	s.mu.RLock()
	hash, ok := s.fileHash, s.fileConfigured
	if !ok {
		hash, ok = s.envHash, s.envConfigured
	}
	s.mu.RUnlock()
	if !ok {
		return false
	}
	actual := sha256.Sum256([]byte(authorization))
	return subtle.ConstantTimeCompare(hash[:], actual[:]) == 1
}

func (s *Store) Save(token string) error {
	if !Valid(token) {
		return ErrInvalidToken
	}
	hash := digest(token)
	data, err := json.MarshalIndent(fileState{Version: 1, SHA256: hex.EncodeToString(hash[:])}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.mu.Lock()
	s.fileHash = hash
	s.fileConfigured = true
	s.mu.Unlock()
	return nil
}

func (s *Store) Clear() error {
	err := os.Remove(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.mu.Lock()
	s.fileHash = [32]byte{}
	s.fileConfigured = false
	s.mu.Unlock()
	if err == nil {
		return syncDirectory(filepath.Dir(s.path))
	}
	return nil
}

func writeFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".deploy-token-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
