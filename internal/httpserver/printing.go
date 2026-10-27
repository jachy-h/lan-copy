package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lan-copy/internal/printing"
)

func (s *Server) printingInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	printers, err := s.printing.Printers()
	if errors.Is(err, printing.ErrUnavailable) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error(), "printers": []printing.Printer{}})
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "reason": "", "printers": printers})
}

func (s *Server) printJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.printing.Jobs()})
}

// A custom action header and same-origin checks protect physical print actions
// while permitting visitors using the host's LAN address.
func validPrintingAction(r *http.Request, action string) bool {
	if r.Header.Get("X-Lan-Copy-Action") != action {
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

func (s *Server) printTestPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validPrintingAction(r, "print-test") {
		writeError(w, http.StatusForbidden, "请从 Lan Copy 页面发起打印测试")
		return
	}
	select {
	case <-s.appContext.Done():
		writeError(w, http.StatusServiceUnavailable, "服务正在关闭")
		return
	default:
	}
	var input struct {
		Printer string `json:"printer"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil || strings.TrimSpace(input.Printer) == "" || strings.ContainsRune(input.Printer, 0) {
		writeError(w, http.StatusBadRequest, "请选择有效的打印机")
		return
	}
	job, err := s.printing.Test(input.Printer)
	switch {
	case errors.Is(err, printing.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, printing.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, job)
	}
}
