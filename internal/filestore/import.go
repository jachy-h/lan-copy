package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SaveFile writes bytes copied from a fragment into a shared directory. The
// destination directory is resolved through the same path policy as every
// other file operation, and an existing name is never overwritten.
func (s *Store) SaveFile(destination, name string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.Path(destination)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fs.ErrInvalid
	}
	safe, err := SafeName(name)
	if err != nil || strings.ContainsAny(name, `/\`) {
		return "", fs.ErrInvalid
	}
	available, err := AvailableName(dir, safe)
	if err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(dir, ".lan-copy-fragment-")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	target := filepath.Join(dir, available)
	if err := os.Rename(tempName, target); err != nil {
		return "", err
	}
	if err := syncDirectory(dir); err != nil {
		rollback := errors.Join(err, os.Remove(target), syncDirectory(dir))
		return "", rollback
	}
	return filepath.ToSlash(filepath.Join(destination, available)), nil
}
