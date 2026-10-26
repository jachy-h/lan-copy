package httpserver

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"lan-copy/internal/desktop"
)

func (s *Server) listFiles(w http.ResponseWriter, _ *http.Request) {
	stored, err := s.files.ListFiles()
	if err != nil {
		writeError(w, 500, "无法读取文件列表")
		return
	}
	files := make([]fileInfo, 0, len(stored))
	for _, item := range stored {
		files = append(files, fileInfo{Name: item.Name, Size: item.Size, ModTime: item.ModTime, Type: contentType(item.Name), PreviewType: previewContentType(item.Name)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	s.serveSharedFile(w, r, false)
}

func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	s.serveSharedFile(w, r, true)
}

func (s *Server) serveSharedFile(w http.ResponseWriter, r *http.Request, preview bool) {
	name := r.PathValue("name")
	_, pathErr := s.files.Path(name)
	if pathErr != nil || name == "" {
		filesystemError(w, errOrInvalid(pathErr))
		return
	}
	file, info, err := s.files.OpenRegular(name)
	if err != nil {
		if errors.Is(err, fs.ErrInvalid) || errors.Is(err, fs.ErrPermission) {
			filesystemError(w, err)
		} else if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "文件不存在")
		} else {
			writeError(w, 500, "无法打开文件")
		}
		return
	}
	defer file.Close()
	disposition, mediaType := "attachment", contentType(name)
	if preview {
		mediaType = previewContentType(name)
		if mediaType == "" {
			writeError(w, http.StatusUnsupportedMediaType, "此文件暂不支持预览，请下载后打开")
			return
		}
		disposition = "inline"
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(path.Base(name)))
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (s *Server) handleLocalFileAction(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, "仅运行服务的本机可以执行此操作")
		return
	}
	action := r.PathValue("action")
	if action != "open" && action != "folder" {
		writeError(w, http.StatusNotFound, "未知的文件操作")
		return
	}
	if r.Header.Get("X-Lan-Copy-Action") != action {
		writeError(w, http.StatusForbidden, "无效的文件操作")
		return
	}
	name := r.PathValue("name")
	_, err := s.files.Path(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	resolved, _, err := s.files.RegularPath(name)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取文件")
		return
	}
	filePath, err := filepath.Abs(resolved)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法定位文件")
		return
	}
	if err := s.openPath(filePath, action); err != nil {
		writeError(w, http.StatusInternalServerError, "无法执行本机文件操作")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "opened"})
}

func isLocalRequest(r *http.Request) bool {
	requestIP := remoteIP(r)
	if requestIP == nil {
		return false
	}
	if requestIP.IsLoopback() {
		return true
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if local, ok := address.(*net.IPNet); ok && local.IP.Equal(requestIP) {
			return true
		}
	}
	return false
}

func isLoopbackRequest(r *http.Request) bool {
	requestIP := remoteIP(r)
	if requestIP == nil || !requestIP.IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	hostIP := net.ParseIP(strings.Trim(host, "[]"))
	return hostIP != nil && hostIP.IsLoopback()
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	requestIP := net.ParseIP(strings.Trim(host, "[]"))
	return requestIP
}

func openLocalPath(filePath, action string) error {
	target := filePath
	if action == "folder" {
		target = filepath.Dir(filePath)
	}
	return desktop.OpenPath(target)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, err := s.files.Path(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	err = s.files.DeleteFile(name)
	if err != nil {
		if errors.Is(err, fs.ErrInvalid) || errors.Is(err, fs.ErrPermission) {
			filesystemError(w, err)
		} else if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "文件不存在")
		} else {
			writeError(w, 500, "无法删除文件")
		}
		return
	}
	s.notifyFilesChanged()
	w.WriteHeader(http.StatusNoContent)
}
