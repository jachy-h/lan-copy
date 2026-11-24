package filestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrFileTooLarge reports a file that exceeds the caller's size budget.
var ErrFileTooLarge = errors.New("file exceeds size limit")

// FileSnapshot is one locked read of a regular file inside the shared root.
type FileSnapshot struct {
	Data     []byte
	Version  string
	Size     int64
	Modified time.Time
}

// SnapshotFile reads a regular file under the shared file lock and returns a
// content version. It rejects symlinks and files above maxBytes.
func (s *Store) SnapshotFile(name string, maxBytes int64) (FileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotFileLocked(name, maxBytes)
}

func (s *Store) snapshotFileLocked(name string, maxBytes int64) (FileSnapshot, error) {
	if name == "" {
		return FileSnapshot{}, fs.ErrInvalid
	}
	resolved, err := s.Path(name)
	if err != nil {
		return FileSnapshot{}, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return FileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return FileSnapshot{}, fs.ErrInvalid
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return FileSnapshot{}, ErrFileTooLarge
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return FileSnapshot{}, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return FileSnapshot{}, ErrFileTooLarge
	}
	return FileSnapshot{Data: data, Version: Version(data), Size: int64(len(data)), Modified: info.ModTime()}, nil
}

// UpdateFile reads a regular file, applies fn and atomically replaces it under
// the same file lock used by uploads and file mutations. fn returning an error
// leaves the original file untouched.
func (s *Store) UpdateFile(name string, maxBytes int64, fn func([]byte) ([]byte, error)) (FileSnapshot, error) {
	if fn == nil {
		return FileSnapshot{}, fs.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.snapshotFileLocked(name, maxBytes)
	if err != nil {
		return FileSnapshot{}, err
	}
	next, err := fn(current.Data)
	if err != nil {
		return FileSnapshot{}, err
	}
	if maxBytes > 0 && int64(len(next)) > maxBytes {
		return FileSnapshot{}, ErrFileTooLarge
	}
	if err := s.replaceFileLocked(name, next); err != nil {
		return FileSnapshot{}, err
	}
	return FileSnapshot{Data: next, Version: Version(next), Size: int64(len(next)), Modified: time.Now()}, nil
}

func (s *Store) replaceFileLocked(name string, data []byte) error {
	resolved, err := s.Path(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(resolved)
	temp, err := os.CreateTemp(dir, ".lan-copy-table-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, resolved); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// WriteFile atomically creates or replaces a regular file inside the shared
// root. The parent directory must already exist. Readers only ever observe the
// previous contents or the complete new contents, never a partial write.
func (s *Store) WriteFile(name string, data []byte) (FileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return FileSnapshot{}, fs.ErrInvalid
	}
	dir, base := path.Split(name)
	dir = path.Clean(dir)
	if dir == "." {
		dir = ""
	}
	parent, err := s.Path(dir)
	if err != nil {
		return FileSnapshot{}, err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return FileSnapshot{}, err
	}
	if !info.IsDir() {
		return FileSnapshot{}, fs.ErrInvalid
	}
	safe, err := SafeName(base)
	if err != nil || safe != base || base == "" {
		return FileSnapshot{}, fs.ErrInvalid
	}
	target := filepath.Join(parent, safe)
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return FileSnapshot{}, fs.ErrPermission
		}
		if !info.Mode().IsRegular() {
			return FileSnapshot{}, fs.ErrInvalid
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return FileSnapshot{}, err
	}
	temp, err := os.CreateTemp(parent, ".lan-copy-table-")
	if err != nil {
		return FileSnapshot{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return FileSnapshot{}, err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return FileSnapshot{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return FileSnapshot{}, err
	}
	if err := temp.Close(); err != nil {
		return FileSnapshot{}, err
	}
	if err := os.Rename(tempName, target); err != nil {
		return FileSnapshot{}, err
	}
	if err := syncDirectory(parent); err != nil {
		return FileSnapshot{}, err
	}
	return FileSnapshot{Data: data, Version: Version(data), Size: int64(len(data)), Modified: time.Now()}, nil
}

// CreateFile creates a new regular file inside the shared root. The parent
// directory must already exist; an existing target returns fs.ErrExist.
func (s *Store) CreateFile(name string, data []byte) (FileSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || strings.Contains(name, "\\") {
		return FileSnapshot{}, fs.ErrInvalid
	}
	dir, base := path.Split(name)
	dir = path.Clean(dir)
	if dir == "." {
		dir = ""
	}
	parent, err := s.Path(dir)
	if err != nil {
		return FileSnapshot{}, err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return FileSnapshot{}, err
	}
	if !info.IsDir() {
		return FileSnapshot{}, fs.ErrInvalid
	}
	safe, err := SafeName(base)
	if err != nil || safe != base || base == "" {
		return FileSnapshot{}, fs.ErrInvalid
	}
	target := filepath.Join(parent, safe)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return FileSnapshot{}, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(target)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return FileSnapshot{}, err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return FileSnapshot{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return FileSnapshot{}, err
	}
	if err := syncDirectory(parent); err != nil {
		_ = os.Remove(target)
		return FileSnapshot{}, err
	}
	return FileSnapshot{Data: data, Version: Version(data), Size: int64(len(data)), Modified: time.Now()}, nil
}

// Version returns the stable content version used by smart tables.
func Version(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
