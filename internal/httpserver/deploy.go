package httpserver

import (
	"net/http"
)

// deployUpdate accepts the same signed archive as the browser update endpoint.
func (s *Server) deployUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.deployAuth == nil || !s.deployAuth.Configured() {
		writeError(w, http.StatusServiceUnavailable, "自动部署未启用，请在设置页保存部署令牌")
		return
	}
	if !s.deployAuth.Match(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "部署令牌无效")
		return
	}
	s.applyUpdatePackage(w, r, false)
}
