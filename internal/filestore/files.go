package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type File struct {
	Name    string
	Size    int64
	ModTime time.Time
}

func (s *Store) ListFiles() ([]File, error) {
	files := make([]File, 0)
	err := filepath.WalkDir(s.dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if filePath == s.dir {
			return nil
		}
		relative, err := filepath.Rel(s.dir, filePath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if _, err := s.Path(name); err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, File{Name: name, Size: info.Size(), ModTime: info.ModTime()})
		}
		return nil
	})
	return files, err
}

func (s *Store) RegularPath(name string) (string, fs.FileInfo, error) {
	resolved, err := s.Path(name)
	if err != nil {
		return "", nil, err
	}
	if name == "" {
		return "", nil, fs.ErrInvalid
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fs.ErrNotExist
	}
	return resolved, info, nil
}

func (s *Store) OpenRegular(name string) (*os.File, fs.FileInfo, error) {
	resolved, info, err := s.RegularPath(name)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, nil, err
	}
	return file, info, nil
}

func (s *Store) DeleteFile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	resolved, err := s.Path(name)
	if err != nil {
		return err
	}
	if name == "" {
		return fs.ErrInvalid
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fs.ErrNotExist
	}
	return os.Remove(resolved)
}

func (s *Store) CheckDestination(destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.destinationLocked(destination)
	return err
}

func (s *Store) Preflight(destination string, roots []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := s.destinationLocked(destination)
	if err != nil {
		return nil, err
	}
	conflicts := make([]string, 0)
	for _, root := range roots {
		_, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(root)))
		if statErr == nil {
			conflicts = append(conflicts, root)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	return conflicts, nil
}

func (s *Store) destinationLocked(destination string) (string, error) {
	target, err := s.Path(destination)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fs.ErrInvalid
	}
	return target, nil
}
