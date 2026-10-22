package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type directoryEntry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Directory   bool      `json:"directory"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified"`
	PreviewType string    `json:"previewType"`
}

// Resolve each component separately: shared paths must never traverse a symlink
// or reach internal upload/text storage files, even inside nested directories.
func (s *server) sharedPath(name string) (string, error) {
	if name == "" {
		return s.dir, nil
	}
	if strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return "", fs.ErrInvalid
	}
	current := s.dir
	for _, component := range strings.Split(name, "/") {
		clean, err := safeName(component)
		if err != nil || clean != component || strings.ContainsAny(component, ":*?\"<>|") || strings.HasSuffix(component, ".") {
			return "", fs.ErrInvalid
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fs.ErrPermission
		}
	}
	return current, nil
}

func filesystemError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "无法完成文件操作"
	switch {
	case errors.Is(err, fs.ErrInvalid), errors.Is(err, fs.ErrPermission):
		status, message = http.StatusBadRequest, "路径无效，或包含不可访问的文件"
	case errors.Is(err, fs.ErrNotExist):
		status, message = http.StatusNotFound, "文件或文件夹不存在，请刷新后重试"
	case errors.Is(err, fs.ErrExist):
		status, message = http.StatusConflict, "目标位置已存在同名文件或文件夹"
	}
	writeError(w, status, message)
}

func (s *server) listDirectory(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.URL.Query().Get("path")
	dir, err := s.sharedPath(name)
	if err != nil {
		filesystemError(w, err)
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		filesystemError(w, err)
		return
	}
	if !info.IsDir() {
		filesystemError(w, fs.ErrInvalid)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	result := make([]directoryEntry, 0)
	err = filepath.WalkDir(dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == dir {
			return nil
		}
		relative, err := filepath.Rel(s.dir, filePath)
		if err != nil {
			return err
		}
		sharedName := filepath.ToSlash(relative)
		if _, err := s.sharedPath(sharedName); err != nil {
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
			mediaType := ""
			if !info.IsDir() {
				mediaType = previewContentType(sharedName)
			}
			result = append(result, directoryEntry{Name: entry.Name(), Path: sharedName, Directory: info.IsDir(), Size: info.Size(), Modified: info.ModTime(), PreviewType: mediaType})
		}
		if query == "" && entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		filesystemError(w, err)
		return
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Directory != result[j].Directory {
			return result[i].Directory
		}
		return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": name, "entries": result})
}

func (s *server) mutateDirectory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path        string `json:"path"`
		Destination string `json:"destination"`
		Name        string `json:"name"`
		Recursive   bool   `json:"recursive"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件操作数据格式无效")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.sharedPath(input.Path)
	if err != nil {
		filesystemError(w, err)
		return
	}
	switch r.PathValue("action") {
	case "mkdir", "move", "rename":
		parent := source
		name := input.Name
		if r.PathValue("action") != "mkdir" {
			if input.Path == "" {
				writeError(w, 400, "不能移动或重命名共享根目录")
				return
			}
			parent, err = s.sharedPath(input.Destination)
			if err != nil {
				filesystemError(w, err)
				return
			}
			if r.PathValue("action") == "move" {
				name = path.Base(input.Path)
			}
		}
		clean, err := safeName(name)
		if err != nil || name != clean || strings.ContainsAny(name, "\\/:*?\"<>|") || strings.HasSuffix(name, ".") {
			filesystemError(w, fs.ErrInvalid)
			return
		}
		info, err := os.Stat(parent)
		if err != nil {
			filesystemError(w, err)
			return
		}
		if !info.IsDir() {
			writeError(w, 400, "目标必须是文件夹")
			return
		}
		target := filepath.Join(parent, name)
		relative := path.Join(input.Destination, name)
		if r.PathValue("action") == "mkdir" {
			relative = path.Join(input.Path, name)
		}
		if r.PathValue("action") != "mkdir" && (relative == input.Path || strings.HasPrefix(relative, input.Path+"/")) {
			writeError(w, 400, "不能移动到原位置或自身子目录")
			return
		}
		if _, err := os.Lstat(target); err == nil {
			filesystemError(w, fs.ErrExist)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			filesystemError(w, err)
			return
		}
		if r.PathValue("action") == "mkdir" {
			err = os.Mkdir(target, 0755)
		} else {
			err = os.Rename(source, target)
		}
		if err != nil {
			filesystemError(w, err)
			return
		}
	case "delete":
		if input.Path == "" {
			writeError(w, 400, "不能删除共享根目录")
			return
		}
		info, err := os.Lstat(source)
		if err != nil {
			filesystemError(w, err)
			return
		}
		if info.IsDir() && !input.Recursive {
			directory, err := os.Open(source)
			if err != nil {
				filesystemError(w, err)
				return
			}
			_, readErr := directory.Readdirnames(1)
			_ = directory.Close()
			if readErr == nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "此文件夹包含内容，确认后将删除全部文件和子文件夹", "requiresConfirmation": true})
				return
			}
			if !errors.Is(readErr, io.EOF) {
				filesystemError(w, readErr)
				return
			}
		}
		if info.IsDir() && input.Recursive {
			// RemoveAll removes descendant symlinks themselves without following them.
			err = os.RemoveAll(source)
		} else {
			err = os.Remove(source)
		}
		if err != nil {
			// Recursive removal can partially succeed (e.g. a locked Windows file).
			s.notifyFilesChanged()
			filesystemError(w, err)
			return
		}
	default:
		writeError(w, 404, "未知的文件操作")
		return
	}
	s.notifyFilesChanged()
	w.WriteHeader(http.StatusNoContent)
}

func errOrInvalid(err error) error {
	if err == nil {
		return fs.ErrInvalid
	}
	return err
}
