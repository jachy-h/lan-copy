package httpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// validLocalActionRequest guards settings that can only be changed from the
// computer running Lan Copy. The custom header prevents simple cross-site
// form submissions; Origin and Sec-Fetch-Site keep browser callers honest.
func validLocalActionRequest(r *http.Request, action string) bool {
	if !isLocalRequest(r) || r.Header.Get("X-Lan-Copy-Action") != action {
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
