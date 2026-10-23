package httpserver

import (
	"net"
	"net/http"
)

func (s *Server) accessInfo(w http.ResponseWriter, r *http.Request) {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" {
		port = "80"
	}
	urls := s.probeURLs(r.Context(), LocalURLs(port))
	writeJSON(w, http.StatusOK, map[string]any{"urls": urls, "local": isLocalRequest(r)})
}
