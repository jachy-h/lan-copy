package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"lan-copy/internal/filestore"
)

const maxTreeManifest = 2 << 20

func (s *Server) preflightUploadTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, maxTreeManifest+1024)
	var input struct {
		Entries []filestore.TreeEntry `json:"entries"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, http.StatusBadRequest, "上传目录清单无效")
		return
	}
	_, roots, err := filestore.ValidateUploadTree(input.Entries)
	if err != nil {
		writeError(w, http.StatusBadRequest, "上传目录路径无效或数量超限")
		return
	}
	destination := r.URL.Query().Get("path")
	conflicts, err := s.files.Preflight(destination, roots)
	if err != nil {
		filesystemError(w, err)
		return
	}
	id, err := s.uploadPreflight.Create(destination, input.Entries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法创建上传会话")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": id, "conflicts": conflicts, "expiresInSeconds": 900})
}

func (s *Server) releaseUploadPreflight(w http.ResponseWriter, r *http.Request) {
	if s.uploadPreflight != nil {
		s.uploadPreflight.Release(r.PathValue("id"))
	}
	w.WriteHeader(http.StatusNoContent)
}

func treeUploadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUploadIdleTimeout) {
		writeError(w, http.StatusRequestTimeout, "上传连接空闲超时，请重新上传")
		return
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "文件总大小超过上传限制")
		return
	}
	writeError(w, http.StatusBadRequest, "上传数据不完整，请重新上传文件夹")
}

func (s *Server) uploadTree(w http.ResponseWriter, r *http.Request) {
	if s.uploadResources != nil {
		if !s.uploadResources.Begin() {
			writeError(w, http.StatusServiceUnavailable, "当前上传数量已达上限，请稍后重试")
			return
		}
		defer s.uploadResources.End()
	}
	destination := r.URL.Query().Get("path")
	// Check the destination before receiving data, then revalidate at commit time.
	err := s.files.CheckDestination(destination)
	if err != nil {
		filesystemError(w, err)
		return
	}
	if s.uploadResources != nil {
		config := s.uploadResources.Snapshot()
		r.Body = newIdleTimeoutBody(r.Body, time.Duration(config.IdleSeconds)*time.Second)
		defer r.Body.Close()
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
	var manifest filestore.TreeManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件夹目录清单无效")
		return
	}
	directories, roots, err := filestore.ValidateUploadTree(manifest.Entries)
	if err != nil {
		writeError(w, 400, "文件夹路径无效或数量超限（最多 10000 项、64 层目录）")
		return
	}
	if session := r.URL.Query().Get("session"); session != "" {
		if s.uploadPreflight == nil || !s.uploadPreflight.Consume(session, destination, manifest.Entries) {
			writeError(w, http.StatusConflict, "上传预检会话已失效，请重新检查目标")
			return
		}
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
	txn, err := s.files.BeginUpload(destination, manifest.Entries, s.uploadResources)
	if err != nil {
		var stageErr *filestore.StageError
		if errors.As(err, &stageErr) {
			switch stageErr.Kind {
			case "record":
				writeError(w, 500, "无法初始化上传事务")
			case "directory":
				writeError(w, 500, "无法创建上传目录")
			default:
				writeError(w, 500, "无法创建上传临时目录")
			}
		} else {
			filesystemError(w, err)
		}
		return
	}
	defer txn.Close()
	transactionID := txn.ID()
	seen := make(map[int]bool)
	saved := make([]fileInfo, 0)
	for {
		part, err = reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Printf("异常: 上传请求失败，事务=%s 目标=%q 错误=%v", transactionID, destination, err)
			treeUploadError(w, err)
			return
		}
		index, parseErr := strconv.Atoi(strings.TrimPrefix(part.FormName(), "file-"))
		if parseErr != nil || index < 0 || index >= len(manifest.Entries) || part.FormName() != fmt.Sprintf("file-%d", index) || part.FileName() == "" || seen[index] || manifest.Entries[index].Directory {
			writeError(w, 400, "上传文件与目录清单不匹配")
			return
		}
		entry := manifest.Entries[index]
		size, stageErr := txn.StageFile(entry.Path, part)
		if stageErr != nil {
			var detail *filestore.StageError
			if errors.As(stageErr, &detail) {
				switch detail.Kind {
				case "write":
					if errors.Is(detail.Err, filestore.ErrStagingBudget) {
						writeError(w, http.StatusInsufficientStorage, "所有并发上传的暂存空间已达上限")
						return
					}
					log.Printf("异常: 写入上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, detail.Err)
					treeUploadError(w, detail.Err)
					return
				case "sync":
					log.Printf("异常: 同步上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, detail.Err)
				case "close":
					log.Printf("异常: 关闭上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, detail.Err)
				case "directory-sync":
					log.Printf("异常: 同步上传暂存目录失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, detail.Err)
				}
			}
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
	names, err := txn.Commit(allowed, s.notifyFilesChanged)
	if err != nil {
		var conflict *filestore.Conflict
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
