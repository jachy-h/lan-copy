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

// validActionRequest applies the CSRF-style checks used by mutating APIs: a
// custom header, no cross-site browser metadata and a same-origin Origin when
// present. It does not restrict the request to the host machine; callers that
// need that must combine it with isLocalRequest.
func validActionRequest(r *http.Request, action string) bool {
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

// validLocalActionRequest guards settings that can only be changed from the
// computer running Lan Copy.
func validLocalActionRequest(r *http.Request, action string) bool {
	return isLocalRequest(r) && validActionRequest(r, action)
}
