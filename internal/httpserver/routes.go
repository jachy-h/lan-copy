package httpserver

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/app", s.appInfo)
	mux.HandleFunc("GET /api/settings/autostart", s.autostartSettings)
	mux.HandleFunc("PUT /api/settings/autostart", s.autostartSettings)
	mux.HandleFunc("GET /api/settings/upload-resources", s.uploadResourceSettings)
	mux.HandleFunc("PUT /api/settings/upload-resources", s.uploadResourceSettings)
	mux.HandleFunc("POST /api/update", s.installUpdate)
	mux.HandleFunc("GET /api/update/check", s.checkRelease)
	mux.HandleFunc("POST /api/update/download", s.downloadUpdate)
	mux.HandleFunc("GET /api/access", s.accessInfo)
	mux.HandleFunc("GET /api/events", s.changeEvents)
	mux.HandleFunc("GET /api/files", s.listFiles)
	mux.HandleFunc("GET /api/filesystem", s.listDirectory)
	mux.HandleFunc("POST /api/filesystem/{action}", s.mutateDirectory)
	mux.Handle("POST /api/upload-tree", s.trackTransfer(transferUpload, http.HandlerFunc(s.uploadTree)))
	mux.HandleFunc("POST /api/upload-tree/preflight", s.preflightUploadTree)
	mux.HandleFunc("DELETE /api/upload-tree/session/{id}", s.releaseUploadPreflight)
	mux.HandleFunc("POST /api/files/{name}/{action}", s.handleLocalFileAction)
	mux.HandleFunc("DELETE /api/files/{name}", s.deleteFile)
	mux.Handle("GET /files/{name}", s.trackTransfer(transferDownload, http.HandlerFunc(s.downloadFile)))
	mux.Handle("GET /preview-files/{name}", s.trackTransfer(transferDownload, http.HandlerFunc(s.previewFile)))
	mux.HandleFunc("GET /thumbnails/{name}", s.thumbnailFile)
	mux.HandleFunc("GET /api/texts", s.listTexts)
	mux.HandleFunc("POST /api/texts", s.createText)
	mux.HandleFunc("DELETE /api/texts/{id}", s.deleteText)
	mux.HandleFunc("GET /api/shutdown/status", s.shutdownStatus)
	mux.HandleFunc("GET /api/shutdown", s.handleShutdown)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Lan-Copy-Service", "lan-copy")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /filesystem.html", func(w http.ResponseWriter, r *http.Request) {
		target := "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("GET /transfer.html", func(w http.ResponseWriter, r *http.Request) {
		target := "/transfer-text"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET /transfer-text", func(w http.ResponseWriter, r *http.Request) {
		page := r.Clone(r.Context())
		page.URL.Path = "/transfer.html"
		page.URL.RawPath = ""
		s.web.ServeHTTP(w, page)
	})
	mux.HandleFunc("GET /preview.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'wasm-unsafe-eval'; worker-src 'self'; font-src 'self' blob: data:; img-src 'self' data:; frame-ancestors 'none'")
		s.web.ServeHTTP(w, r)
	})
	mux.Handle("GET /", s.web)
	return SecurityHeaders(mux)
}

var hashedFrontendAsset = regexp.MustCompile(`^/_frontend/assets/[^/]+-[A-Za-z0-9_-]{8}\.[^/]+$`)

func StaticFiles(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fixed URLs must revalidate after a binary upgrade. Only existing content-
		// addressed assets may be cached permanently; never cache missing old chunks.
		w.Header().Set("Cache-Control", "no-cache")
		if hashedFrontendAsset.MatchString(r.URL.Path) {
			if info, err := fs.Stat(root, strings.TrimPrefix(path.Clean(r.URL.Path), "/")); err == nil && !info.IsDir() {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		switch strings.ToLower(path.Ext(r.URL.Path)) {
		case ".js", ".mjs":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".wasm":
			w.Header().Set("Content-Type", "application/wasm")
		}
		files.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; font-src 'self' blob: data:; worker-src 'self'")
		next.ServeHTTP(w, r)
	})
}
