// Command plugin-example is a minimal Lan Copy plugin used by integration
// tests and the plugin protocol documentation. It has no third-party imports.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

const version = "1.0.0"

const manifest = `{
  "schemaVersion": 1,
  "id": "demo-plugin",
  "name": "Demo Plugin",
  "version": "1.0.0",
  "description": "Minimal Lan Copy plugin protocol example",
  "protocolVersion": "1.0",
  "runtime": "http",
  "category": "example",
  "containerPort": 8080,
  "endpoints": {
    "health": "/health",
    "manifest": "/manifest",
    "version": "/version",
    "capabilities": "/capabilities"
  },
  "capabilities": ["demo.echo", "demo.files"],
  "defaults": {
    "cpuLimit": 0.2,
    "memoryMB": 64,
    "pidsLimit": 64,
    "requestTimeoutSeconds": 15,
    "startupTimeoutSeconds": 30
  }
}`

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})
	mux.HandleFunc("GET /manifest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, manifest)
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"version": version, "build": "example", "runtime": "go",
			"os": os.Getenv("HOST_OS"),
		})
	})
	mux.HandleFunc("GET /capabilities", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"capabilities": []string{"demo.echo", "demo.files"}})
	})
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		var input any
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "pong", "received": input, "secret": os.Getenv("DEMO_SECRET") != "",
		})
	})
	mux.HandleFunc("GET /download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "demo-plugin download\n")
	})
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		reader, err := r.MultipartReader()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart required"})
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file required"})
			return
		}
		defer part.Close()
		size, err := io.Copy(io.Discard, part)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": part.FileName(), "size": size})
	})
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("demo plugin listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
