// Command plugin-example is a minimal Lan Copy plugin used by integration
// tests and the plugin protocol documentation. It has no third-party imports.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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
    "capabilities": "/capabilities",
    "events": "/events"
  },
  "capabilities": ["demo.echo", "demo.files"],
  "events": ["file.uploaded", "folder.*", "text.*"],
  "ui": {"path": "/"},
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
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		prefix := strings.TrimRight(r.Header.Get("X-Forwarded-Prefix"), "/")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, uiPage, prefix)
	})
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		token := os.Getenv("LAN_COPY_EVENT_TOKEN")
		timestamp := r.Header.Get("X-Lan-Copy-Timestamp")
		mac := hmac.New(sha256.New, []byte(token))
		_, _ = mac.Write([]byte(timestamp))
		_, _ = mac.Write([]byte("."))
		_, _ = mac.Write(body)
		expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if token == "" || !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Lan-Copy-Signature"))) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
			return
		}
		var event struct {
			ID   string          `json:"id"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &event); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event"})
			return
		}
		log.Printf("lifecycle event received: id=%s type=%s data=%s", event.ID, event.Type, event.Data)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": event.ID})
	})
	forward := func(w http.ResponseWriter, r *http.Request, endpoint string) {
		base := os.Getenv("LAN_COPY_API_URL")
		token := os.Getenv("LAN_COPY_API_TOKEN")
		if base == "" || token == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "LAN_COPY_API_URL/TOKEN not configured"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(base, "/")+endpoint, bytes.NewReader(body))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(response.Body, 2<<20))
	}
	mux.HandleFunc("POST /table-query", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, "/api/plugin-api/tables/query")
	})
	mux.HandleFunc("POST /table-update", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, "/api/plugin-api/tables/lock-and-update")
	})
	mux.HandleFunc("GET /download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "demo-plugin download\n")
	})
	// GET /api-check verifies that the injected LAN_COPY_API_URL is reachable
	// from inside the plugin container. It is used by Docker integration tests.
	mux.HandleFunc("GET /api-check", func(w http.ResponseWriter, _ *http.Request) {
		base := strings.TrimRight(os.Getenv("LAN_COPY_API_URL"), "/")
		result := map[string]any{"url": base, "reachable": false}
		if base == "" {
			result["error"] = "LAN_COPY_API_URL not configured"
			writeJSON(w, http.StatusOK, result)
			return
		}
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get(base + "/healthz")
		if err != nil {
			result["error"] = err.Error()
			writeJSON(w, http.StatusOK, result)
			return
		}
		defer response.Body.Close()
		result["status"] = response.StatusCode
		result["reachable"] = response.StatusCode == http.StatusOK
		writeJSON(w, http.StatusOK, result)
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

// uiPage is served at the container root. It reads X-Forwarded-Prefix so the
// same binary works when Lan Copy exposes it under /plugin-app/<id>/.
const uiPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Demo Plugin</title>
<style>
body{font:16px/1.6 system-ui,sans-serif;max-width:40rem;margin:10vh auto;padding:0 1.5rem;color:#1f2937}
code{background:#eef2f7;padding:.15rem .35rem;border-radius:4px}
button{font:inherit;padding:.45rem .9rem;border:1px solid #c7d2e0;border-radius:8px;background:#fff;cursor:pointer}
pre{background:#0f172a;color:#e2e8f0;padding:1rem;border-radius:8px;overflow:auto}
</style></head>
<body>
<h1>Demo Plugin</h1>
<p>这个页面由插件容器提供，通过 Lan Copy 的子路径代理打开。</p>
<p>代理前缀：<code>%[1]s</code></p>
<button id="check" type="button">调用插件接口</button>
<pre id="output">点击按钮验证同源请求</pre>
<script>
document.getElementById('check').addEventListener('click', async () => {
  const output = document.getElementById('output')
  try {
    const response = await fetch('%[1]s/version', { headers: { Accept: 'application/json' } })
    output.textContent = response.status + ' ' + await response.text()
  } catch (error) {
    output.textContent = String(error)
  }
})
</script>
</body></html>`
