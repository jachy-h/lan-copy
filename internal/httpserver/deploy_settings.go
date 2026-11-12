package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"lan-copy/internal/deploytoken"
)

func validDeployTokenRequest(r *http.Request) bool {
	if r.Header.Get("X-Lan-Copy-Action") != "deploy-token" {
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

func (s *Server) deployTokenSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.deployAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "自动部署设置不可用")
		return
	}
	switch r.Method {
	case http.MethodPut:
		if !validDeployTokenRequest(r) {
			writeError(w, http.StatusForbidden, "无效的设置请求，请从 Lan Copy 页面发起")
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "部署令牌格式无效")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "部署令牌格式无效")
			return
		}
		if err := s.deployAuth.Save(input.Token); err != nil {
			if errors.Is(err, deploytoken.ErrInvalidToken) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "无法保存部署令牌")
			return
		}
	case http.MethodDelete:
		if !validDeployTokenRequest(r) {
			writeError(w, http.StatusForbidden, "无效的设置请求，请从 Lan Copy 页面发起")
			return
		}
		if err := s.deployAuth.Clear(); err != nil {
			writeError(w, http.StatusInternalServerError, "无法删除部署令牌")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.deployAuth.Status())
}
