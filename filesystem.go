package main

import (
	"context"
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

var maxDirectorySearchEntries = 10000

var errDirectorySearchLimit = errors.New("directory search traversal limit reached")

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
		if err := validateSharedComponent(component); err != nil {
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

// validateSharedComponent is the common cross-platform rule for every path
// component that can enter or be addressed inside the shared directory.
func validateSharedComponent(component string) error {
	if component == "" || component == "." || component == ".." || strings.TrimSpace(component) != component || strings.ContainsAny(component, "/\\:*?\"<>|") || strings.HasSuffix(component, ".") || strings.ContainsRune(component, 0) {
		return fs.ErrInvalid
	}
	lower := strings.ToLower(component)
	if strings.HasPrefix(lower, ".upload-") || strings.HasPrefix(lower, ".lan-copy-") {
		return fs.ErrInvalid
	}
	for _, char := range component {
		if char < 32 || char == 127 {
			return fs.ErrInvalid
		}
	}
	// Windows reserves these device names even when one or more extensions
	// follow them. COM/LPT also accept the superscript digits documented by
	// Windows, and CONIN$/CONOUT$ are reserved console device names.
	stem := component
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(stem)
	runes := []rune(stem)
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || (len(runes) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && (runes[3] >= '1' && runes[3] <= '9' || runes[3] == '¹' || runes[3] == '²' || runes[3] == '³')) {
		return fs.ErrInvalid
	}
	return nil
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
	name := r.URL.Query().Get("path")
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	result, err := func() ([]directoryEntry, error) {
		// Browsing and search are best-effort reads. Releasing fileMu lets
		// uploads and mutations proceed while the filesystem is being walked.
		dir, err := s.sharedPath(name)
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
		result := make([]directoryEntry, 0)
		visited := 0
		err = filepath.WalkDir(dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrNotExist) {
					return nil // An in-app mutation removed this entry during the search.
				}
				return walkErr
			}
			if err := r.Context().Err(); err != nil {
				return err
			}
			if filePath == dir {
				return nil
			}
			visited++
			if visited > maxDirectorySearchEntries {
				return errDirectorySearchLimit
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
			return nil, err
		}
		return result, nil
	}()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if errors.Is(err, errDirectorySearchLimit) {
			writeError(w, http.StatusRequestEntityTooLarge, "搜索范围过大，请缩小搜索范围")
			return
		}
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
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
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
		if err := validateSharedComponent(name); err != nil {
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
