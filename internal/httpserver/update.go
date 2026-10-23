package httpserver

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"

	"lan-copy/internal/updater"
)

func (s *Server) updateUnavailableReason() string { return s.updater.DisabledReason(s.updatePublicKey) }
func (s *Server) appInfo(w http.ResponseWriter, r *http.Request) {
	local := isLocalRequest(r)
	reason := s.updateUnavailableReason()
	enabled := reason == ""
	_, keyErr := updater.TrustedUpdateKey(s.updatePublicKey)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version, "os": runtime.GOOS, "arch": runtime.GOARCH, "local": local, "canShutdown": isLoopbackRequest(r), "updateEnabled": enabled, "updateDisabledReason": reason, "signedUpdates": keyErr == nil, "updateID": os.Getenv("LAN_COPY_UPDATE_ID")})
}
func validUpdateRequest(r *http.Request) bool {
	if r.Header.Get("X-Lan-Copy-Action") != "update" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
	}
	return true
}
func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	s.installUpdatePackage(w, r, false)
}

func (s *Server) installUpdatePackage(w http.ResponseWriter, r *http.Request, online bool) {
	if !validUpdateRequest(r) {
		writeError(w, http.StatusForbidden, "无效的更新请求，请从 Lan Copy 页面发起")
		return
	}
	if reason := s.updateUnavailableReason(); reason != "" {
		writeError(w, http.StatusServiceUnavailable, reason)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, updater.MaxSize)
	source := func() (io.ReadCloser, string, error) { return r.Body, "", nil }
	if online {
		source = func() (io.ReadCloser, string, error) { return s.releasePackage(r.Context()) }
	}
	manifest, id, err := s.updater.Install(source, runtime.GOOS, runtime.GOARCH, s.updatePublicKey)
	if err != nil {
		var installErr *updater.InstallError
		if !errors.As(err, &installErr) {
			writeError(w, 500, "无法处理更新包")
			return
		}
		switch installErr.Kind {
		case "busy":
			writeError(w, http.StatusConflict, "已有更新正在处理")
		case "stage":
			writeError(w, 500, "程序目录不可写，无法更新")
		case "archive":
			writeError(w, 500, "无法暂存更新包")
		case "source":
			writeError(w, http.StatusBadGateway, installErr.Err.Error())
		case "read":
			writeError(w, http.StatusBadRequest, "更新包读取失败或超过 128 MiB")
		case "invalid":
			writeError(w, http.StatusBadRequest, installErr.Err.Error())
		case "version":
			writeError(w, http.StatusBadRequest, "更新包版本与 Release 不一致")
		case "current":
			writeError(w, 500, "无法读取当前程序")
		case "same":
			writeError(w, http.StatusConflict, "该更新包与当前程序相同")
		case "token":
			writeError(w, 500, "无法创建更新标识")
		case "launch":
			writeError(w, 500, "无法启动更新辅助进程："+installErr.Err.Error())
		default:
			writeError(w, 500, "无法处理更新包")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting", "version": manifest.Version, "updateID": id})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	s.requestShutdown("update")
}
