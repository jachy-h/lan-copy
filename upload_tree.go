package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxTreeEntries = 10000
const maxTreeManifest = 2 << 20

type uploadTreeEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
}

type uploadTreeManifest struct {
	Entries []uploadTreeEntry `json:"entries"`
	Replace []string          `json:"replace,omitempty"`
}

// All paths are relative to a private staging directory, never to the host.
// Reject case aliases too, so the same manifest works on Windows and macOS.
func validateUploadTree(entries []uploadTreeEntry) ([]string, []string, error) {
	if len(entries) == 0 || len(entries) > maxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	explicit := make(map[string]bool)
	canonical := make(map[string]string)
	directories := make(map[string]bool)
	roots := make(map[string]bool)
	for _, entry := range entries {
		if len(entry.Path) > 4096 || entry.Path == "" || path.Clean(entry.Path) != entry.Path || strings.Contains(entry.Path, "\\") || strings.HasPrefix(entry.Path, "/") {
			return nil, nil, fs.ErrInvalid
		}
		components := strings.Split(entry.Path, "/")
		if len(components) > 64 {
			return nil, nil, fs.ErrInvalid
		}
		for index, component := range components {
			clean, err := safeName(component)
			lower := strings.ToLower(component)
			if err != nil || clean != component || strings.ContainsAny(component, ":*?\"<>|") || strings.HasSuffix(component, ".") || strings.HasPrefix(lower, ".upload-") || strings.HasPrefix(lower, ".lan-copy-") {
				return nil, nil, fs.ErrInvalid
			}
			prefix := strings.Join(components[:index+1], "/")
			key := strings.ToLower(prefix)
			if existing, ok := canonical[key]; ok && existing != prefix {
				return nil, nil, fs.ErrInvalid
			}
			canonical[key] = prefix
			if index < len(components)-1 {
				directories[prefix] = true
			}
		}
		if _, exists := explicit[entry.Path]; exists {
			return nil, nil, fs.ErrInvalid
		}
		explicit[entry.Path] = entry.Directory
		if entry.Directory {
			directories[entry.Path] = true
		}
		roots[components[0]] = true
	}
	for directory := range directories {
		if isDirectory, exists := explicit[directory]; exists && !isDirectory {
			return nil, nil, fs.ErrInvalid
		}
	}
	// Implicit parent directories also count toward the operation limit.
	if len(canonical) > maxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	dirs := make([]string, 0, len(directories))
	top := make([]string, 0, len(roots))
	for directory := range directories {
		dirs = append(dirs, directory)
	}
	for root := range roots {
		top = append(top, root)
	}
	sort.Strings(dirs)
	sort.Strings(top)
	return dirs, top, nil
}

func treeUploadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "文件总大小超过上传限制")
		return
	}
	writeError(w, http.StatusBadRequest, "上传数据不完整，请重新上传文件夹")
}

func (s *server) uploadTree(w http.ResponseWriter, r *http.Request) {
	destination := r.URL.Query().Get("path")
	// Check the destination before receiving data, then revalidate at commit time.
	s.mu.Lock()
	target, err := s.sharedPath(destination)
	if err == nil {
		var info fs.FileInfo
		info, err = os.Stat(target)
		if err == nil && !info.IsDir() {
			err = fs.ErrInvalid
		}
	}
	s.mu.Unlock()
	if err != nil {
		filesystemError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "请选择要上传的文件或文件夹")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		treeUploadError(w, err)
		return
	}
	if part.FormName() != "manifest" || part.FileName() != "" {
		writeError(w, 400, "缺少文件夹目录清单")
		return
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(part, maxTreeManifest+1))
	if err != nil {
		treeUploadError(w, err)
		return
	}
	if len(manifestBytes) > maxTreeManifest {
		writeError(w, 413, "文件夹目录清单过大")
		return
	}
	var manifest uploadTreeManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件夹目录清单无效")
		return
	}
	directories, roots, err := validateUploadTree(manifest.Entries)
	if err != nil {
		writeError(w, 400, "文件夹路径无效或数量超限（最多 10000 项、64 层目录）")
		return
	}
	allowed := make(map[string]bool)
	for _, root := range manifest.Replace {
		found := false
		for _, candidate := range roots {
			if candidate == root {
				found = true
				break
			}
		}
		if !found {
			writeError(w, 400, "替换文件名无效")
			return
		}
		allowed[root] = true
	}
	_ = part.Close()
	stage, err := os.MkdirTemp(s.dir, ".upload-tree-")
	if err != nil {
		writeError(w, 500, "无法创建上传临时目录")
		return
	}
	defer os.RemoveAll(stage)
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0755); err != nil {
			writeError(w, 500, "无法创建上传目录")
			return
		}
	}
	seen := make(map[int]bool)
	saved := make([]fileInfo, 0)
	for {
		part, err = reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			treeUploadError(w, err)
			return
		}
		index, parseErr := strconv.Atoi(strings.TrimPrefix(part.FormName(), "file-"))
		if parseErr != nil || index < 0 || index >= len(manifest.Entries) || part.FormName() != fmt.Sprintf("file-%d", index) || part.FileName() == "" || seen[index] || manifest.Entries[index].Directory {
			writeError(w, 400, "上传文件与目录清单不匹配")
			return
		}
		entry := manifest.Entries[index]
		file, err := os.OpenFile(filepath.Join(stage, filepath.FromSlash(entry.Path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			writeError(w, 500, "无法保存上传文件")
			return
		}
		size, copyErr := io.Copy(file, part)
		closeErr := file.Close()
		if copyErr != nil {
			treeUploadError(w, copyErr)
			return
		}
		if closeErr != nil {
			writeError(w, 500, "无法保存上传文件")
			return
		}
		if err := part.Close(); err != nil {
			treeUploadError(w, err)
			return
		}
		seen[index] = true
		saved = append(saved, fileInfo{Name: entry.Path, Size: size, ModTime: time.Now(), Type: contentType(entry.Path), PreviewType: previewContentType(entry.Path)})
	}
	for index, entry := range manifest.Entries {
		if !entry.Directory && !seen[index] {
			writeError(w, 400, "上传文件不完整，请重新上传")
			return
		}
	}
	names, err := s.commitUploadTree(stage, destination, roots, allowed)
	if err != nil {
		var conflict *uploadConflict
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "目标位置已存在同名文件或文件夹", "conflicts": conflict.Names})
		} else {
			filesystemError(w, err)
		}
		return
	}
	relocate := func(name string) string {
		root, rest, hasRest := strings.Cut(name, "/")
		if hasRest {
			return path.Join(destination, names[root], rest)
		}
		return path.Join(destination, names[root])
	}
	for index := range saved {
		saved[index].Name = relocate(saved[index].Name)
	}
	for index := range directories {
		directories[index] = relocate(directories[index])
	}
	s.notifyFilesChanged()
	writeJSON(w, http.StatusCreated, map[string]any{"files": saved, "directories": directories})
}

type uploadConflict struct{ Names []string }

func (e *uploadConflict) Error() string { return "upload requires replacement confirmation" }

func (s *server) commitUploadTree(stage, destination string, roots []string, allowed map[string]bool) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := s.sharedPath(destination)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fs.ErrInvalid
	}
	var conflicts []string
	var existing []string
	for _, root := range roots {
		info, err := os.Lstat(filepath.Join(target, root))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fs.ErrPermission
		}
		existing = append(existing, root)
		if !allowed[root] {
			conflicts = append(conflicts, root)
		}
	}
	if len(conflicts) > 0 {
		return nil, &uploadConflict{Names: conflicts}
	}
	backup, err := os.MkdirTemp(s.dir, ".upload-backup-")
	if err != nil {
		return nil, err
	}
	// Preserve backups if rollback fails so the originals remain recoverable.
	cleanBackup := true
	defer func() {
		if cleanBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	names := make(map[string]string)
	var backedUp []string
	rollback := func(cause error) (map[string]string, error) {
		errs := []error{cause}
		for original := range names {
			if err := os.Rename(filepath.Join(target, original), filepath.Join(stage, original)); err != nil {
				errs = append(errs, err)
			}
		}
		for _, root := range backedUp {
			if err := os.Rename(filepath.Join(backup, root), filepath.Join(target, root)); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 1 {
			cleanBackup = false
			s.notifyFilesChanged()
		}
		return nil, errors.Join(errs...)
	}
	for _, root := range existing {
		if err := os.Rename(filepath.Join(target, root), filepath.Join(backup, root)); err != nil {
			return rollback(err)
		}
		backedUp = append(backedUp, root)
	}
	for _, root := range roots {
		if err := os.Rename(filepath.Join(stage, root), filepath.Join(target, root)); err != nil {
			return rollback(err)
		}
		names[root] = root
	}
	return names, nil
}
