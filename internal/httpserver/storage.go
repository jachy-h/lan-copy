package httpserver

import "net/http"

func (s *Server) storageUsage(w http.ResponseWriter, _ *http.Request) {
	usage, err := s.files.Usage()
	if err != nil {
		filesystemError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
