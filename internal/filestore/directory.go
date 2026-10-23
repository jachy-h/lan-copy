package filestore

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrSearchLimit = errors.New("directory search traversal limit reached")
var ErrRootMutation = errors.New("cannot mutate shared root")
var ErrTargetNotDirectory = errors.New("target must be a directory")
var ErrSelfMove = errors.New("cannot move into itself")
var ErrNonEmpty = errors.New("directory contains content")
var ErrUnknownAction = errors.New("unknown file action")

type Entry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Directory   bool      `json:"directory"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified"`
	PreviewType string    `json:"previewType"`
}

type Mutation struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	Name        string `json:"name"`
	Recursive   bool   `json:"recursive"`
}

func (s *Store) ListDirectory(ctx context.Context, name, query string, limit int) ([]Entry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	dir, err := s.Path(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fs.ErrInvalid
	}
	result := make([]Entry, 0)
	visited := 0
	err = filepath.WalkDir(dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == dir {
			return nil
		}
		visited++
		if visited > limit {
			return ErrSearchLimit
		}
		relative, err := filepath.Rel(s.dir, filePath)
		if err != nil {
			return err
		}
		sharedName := filepath.ToSlash(relative)
		if _, err := s.Path(sharedName); err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if (info.IsDir() || info.Mode().IsRegular()) && (query == "" || strings.Contains(strings.ToLower(entry.Name()), query)) {
			result = append(result, Entry{Name: entry.Name(), Path: sharedName, Directory: info.IsDir(), Size: info.Size(), Modified: info.ModTime()})
		}
		if query == "" && entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Directory != result[j].Directory {
			return result[i].Directory
		}
		return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path)
	})
	return result, nil
}

func (s *Store) MutateDirectory(action string, input Mutation, onPartial func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.Path(input.Path)
	if err != nil {
		return err
	}
	switch action {
	case "mkdir", "move", "rename":
		parent, name := source, input.Name
		if action != "mkdir" {
			if input.Path == "" {
				return ErrRootMutation
			}
			parent, err = s.Path(input.Destination)
			if err != nil {
				return err
			}
			if action == "move" {
				name = path.Base(input.Path)
			}
		}
		if err := ValidateComponent(name); err != nil {
			return fs.ErrInvalid
		}
		info, err := os.Stat(parent)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return ErrTargetNotDirectory
		}
		target := filepath.Join(parent, name)
		relative := path.Join(input.Destination, name)
		if action == "mkdir" {
			relative = path.Join(input.Path, name)
		}
		if action != "mkdir" && (relative == input.Path || strings.HasPrefix(relative, input.Path+"/")) {
			return ErrSelfMove
		}
		if _, err := os.Lstat(target); err == nil {
			return fs.ErrExist
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if action == "mkdir" {
			return os.Mkdir(target, 0755)
		}
		return os.Rename(source, target)
	case "delete":
		if input.Path == "" {
			return ErrRootMutation
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if info.IsDir() && !input.Recursive {
			directory, err := os.Open(source)
			if err != nil {
				return err
			}
			_, readErr := directory.Readdirnames(1)
			_ = directory.Close()
			if readErr == nil {
				return ErrNonEmpty
			}
			if !errors.Is(readErr, io.EOF) {
				return readErr
			}
		}
		if info.IsDir() && input.Recursive {
			// RemoveAll removes descendant symlinks without following them.
			err = os.RemoveAll(source)
			if err != nil && onPartial != nil {
				onPartial()
			}
		} else {
			err = os.Remove(source)
		}
		return err
	default:
		return ErrUnknownAction
	}
}
