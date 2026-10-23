package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"lan-copy/internal/filestore"
)

var maxDirectorySearchEntries = 10000

type directoryEntry = filestore.Entry

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
	for i := range entries {
		if !entries[i].Directory {
			entries[i].PreviewType = previewContentType(entries[i].Path)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": name, "entries": entries})
}

func (s *Server) mutateDirectory(w http.ResponseWriter, r *http.Request) {
	var input filestore.Mutation
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件操作数据格式无效")
		return
	}
	action := r.PathValue("action")
	err := s.files.MutateDirectory(action, input, s.notifyFilesChanged)
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
	s.notifyFilesChanged()
	w.WriteHeader(http.StatusNoContent)
}

func errOrInvalid(err error) error {
	if err == nil {
		return fs.ErrInvalid
	}
	return err
}
