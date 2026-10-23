package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"lan-copy/internal/desktop"
)

func validAutostartRequest(r *http.Request) bool {
	if !isLocalRequest(r) || r.Header.Get("X-Lan-Copy-Action") != "autostart" {
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

func (s *Server) autostartSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut && !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, "请在运行 Lan Copy 的电脑上修改开机自启动")
		return
	}
	if r.Method == http.MethodPut && !validAutostartRequest(r) {
		writeError(w, http.StatusForbidden, "无效的设置请求，请从本机 Lan Copy 页面发起")
		return
	}
	a := s.autostart
	if r.Method == http.MethodPut && a.DisabledReason() != "" {
		writeError(w, http.StatusServiceUnavailable, a.DisabledReason())
		return
	}
	var state desktop.AutostartState
	if r.Method == http.MethodPut {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil {
			writeError(w, http.StatusBadRequest, "无效的自启动设置")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "无效的自启动设置")
			return
		}
		var err error
		state, err = a.Apply(*input.Enabled)
		if errors.Is(err, desktop.ErrAutostartUnavailable) {
			writeError(w, http.StatusServiceUnavailable, state.Reason)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "无法修改系统启动项："+err.Error())
			return
		}
	} else {
		state = a.State()
	}
	if !isLocalRequest(r) {
		state.Available = false
		if state.Reason == "" {
			if state.Enabled {
				state.Reason = "服务主机已开启开机自启动。"
			} else {
				state.Reason = "服务主机未开启开机自启动。"
			}
		}
		state.Reason += "仅运行 Lan Copy 的电脑可以修改。"
	}
	writeJSON(w, http.StatusOK, state)
}
