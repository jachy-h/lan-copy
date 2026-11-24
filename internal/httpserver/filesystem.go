package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"lan-copy/internal/filestore"
)

var maxDirectorySearchEntries = 10000

type directoryEntry struct {
	filestore.Entry
	Table bool `json:"table,omitempty"`
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

func (s *Server) listDirectory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	entries, err := s.files.ListDirectory(r.Context(), name, r.URL.Query().Get("q"), maxDirectorySearchEntries)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if errors.Is(err, filestore.ErrSearchLimit) {
			writeError(w, http.StatusRequestEntityTooLarge, "搜索范围过大，请缩小搜索范围")
			return
		}
		filesystemError(w, err)
		return
	}
	result := make([]directoryEntry, len(entries))
	for i, entry := range entries {
		if !entry.Directory {
			entry.PreviewType = previewContentType(entry.Path)
		}
		result[i] = directoryEntry{Entry: entry}
		if !entry.Directory && s.tables != nil && strings.EqualFold(path.Ext(entry.Path), ".csv") && s.tables.IsMarked(entry.Path) {
			result[i].Table = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": name, "entries": result})
}

func (s *Server) mutateDirectory(w http.ResponseWriter, r *http.Request) {
	var input filestore.Mutation
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件操作数据格式无效")
		return
	}
	action := r.PathValue("action")
	result, err := s.files.MutateDirectory(action, input, s.notifyFilesChanged)
	if err != nil {
		switch {
		case errors.Is(err, filestore.ErrRootMutation):
			if action == "delete" {
				writeError(w, 400, "不能删除共享根目录")
			} else {
				writeError(w, 400, "不能移动或重命名共享根目录")
			}
		case errors.Is(err, filestore.ErrTargetNotDirectory):
			writeError(w, 400, "目标必须是文件夹")
		case errors.Is(err, filestore.ErrSelfMove):
			writeError(w, 400, "不能移动到原位置或自身子目录")
		case errors.Is(err, filestore.ErrNonEmpty):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "此文件夹包含内容，确认后将删除全部文件和子文件夹", "requiresConfirmation": true})
		case errors.Is(err, filestore.ErrUnknownAction):
			writeError(w, 404, "未知的文件操作")
		default:
			filesystemError(w, err)
		}
		return
	}
	s.syncTableMarks(result)
	s.notifyFilesChanged()
	s.publishMutation(result)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) syncTableMarks(result filestore.MutationResult) {
	if s.tables == nil {
		return
	}
	var err error
	switch result.Action {
	case "delete":
		err = s.tables.RemoveMarks(result.Path, result.Directory)
		if !result.Directory {
			if layoutErr := s.tables.RemoveLayout(result.Path); layoutErr != nil {
				log.Printf("删除表格列偏好失败: path=%s: %v", result.Path, layoutErr)
			}
		}
	case "move", "rename":
		err = s.tables.RelocateMarks(result.PreviousPath, result.Path, result.Directory)
		if !result.Directory {
			if layoutErr := s.tables.RelocateLayout(result.PreviousPath, result.Path); layoutErr != nil {
				log.Printf("移动表格列偏好失败: %s -> %s: %v", result.PreviousPath, result.Path, layoutErr)
			}
		}
	}
	if err != nil {
		log.Printf("同步表格标记失败: action=%s path=%s: %v", result.Action, result.Path, err)
	}
}

func errOrInvalid(err error) error {
	if err == nil {
		return fs.ErrInvalid
	}
	return err
}
