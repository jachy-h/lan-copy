package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultMaxUpload = int64(10 << 30) // 10 GiB

//go:embed web/*
var webFiles embed.FS

type fileInfo struct {
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modified"`
	Type        string    `json:"type"`
	PreviewType string    `json:"previewType"`
}

type server struct {
	updater           *updateService
	releases          *releaseService
	autostart         *autostartService
	dir               string
	maxUpload         int64
	fileMu            sync.Mutex
	textMu            sync.Mutex
	web               http.Handler
	probeURLs         func(context.Context, []string) []string
	openPath          func(string, string) error
	eventsMu          sync.Mutex
	fileSubscribers   map[chan struct{}]struct{}
	shutdownRequested chan struct{}
	shutdownOnce      sync.Once
	shutdownReason    string
	appContext        context.Context
	cancelApp         context.CancelFunc
	transfers         transferTracker
}

func newServer(dir string, maxUpload int64) (*server, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	if err := recoverTextStore(dir); err != nil {
		return nil, fmt.Errorf("recover shared text store: %w", err)
	}
	if err := recoverUploadTransactions(dir); err != nil {
		return nil, fmt.Errorf("recover upload transactions: %w", err)
	}
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	// Browser assets must not inherit machine-specific MIME mappings (notably
	// Windows registry entries that can classify .mjs modules as text/plain).
	files := http.FileServer(http.FS(webRoot))
	web := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	appContext, cancelApp := context.WithCancel(context.Background())
	return &server{
		releases:          newReleaseService(),
		dir:               dir,
		maxUpload:         maxUpload,
		web:               web,
		probeURLs:         reachableLocalURLs,
		openPath:          openLocalPath,
		fileSubscribers:   make(map[chan struct{}]struct{}),
		shutdownRequested: make(chan struct{}),
		appContext:        appContext,
		cancelApp:         cancelApp,
	}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/app", s.appInfo)
	mux.HandleFunc("GET /api/settings/autostart", s.autostartSettings)
	mux.HandleFunc("PUT /api/settings/autostart", s.autostartSettings)
	mux.HandleFunc("POST /api/update", s.installUpdate)
	mux.HandleFunc("GET /api/update/check", s.checkRelease)
	mux.HandleFunc("POST /api/update/download", s.downloadUpdate)
	mux.HandleFunc("GET /api/access", s.accessInfo)
	mux.HandleFunc("GET /api/events", s.fileEvents)
	mux.HandleFunc("GET /api/files", s.listFiles)
	mux.HandleFunc("GET /api/filesystem", s.listDirectory)
	mux.HandleFunc("POST /api/filesystem/{action}", s.mutateDirectory)
	mux.Handle("POST /api/upload-tree", s.trackTransfer(transferUpload, http.HandlerFunc(s.uploadTree)))
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
		// The offline start page is opened from file:// and needs to check readiness.
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
	mux.HandleFunc("GET /preview.html", func(w http.ResponseWriter, r *http.Request) {
		// PDF.js uses a same-origin worker, embedded font blobs and WASM decoders.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'wasm-unsafe-eval'; worker-src 'self'; font-src 'self' blob:; img-src 'self' data:; frame-ancestors 'none'")
		s.web.ServeHTTP(w, r)
	})
	mux.Handle("GET /", s.web)
	return securityHeaders(mux)
}

func (s *server) accessInfo(w http.ResponseWriter, r *http.Request) {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" {
		port = "80"
	}
	urls := s.probeURLs(r.Context(), localURLs(port))
	writeJSON(w, http.StatusOK, map[string]any{
		"urls":  urls,
		"local": isLocalRequest(r),
	})
}

func (s *server) fileEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持实时更新")
		return
	}
	updates, unsubscribe := s.subscribeFileEvents()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := io.WriteString(w, ": connected\nretry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.appContext.Done():
			return
		case <-updates:
			if _, err := io.WriteString(w, "event: files\ndata: refresh\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *server) subscribeFileEvents() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	s.eventsMu.Lock()
	s.fileSubscribers[updates] = struct{}{}
	s.eventsMu.Unlock()
	return updates, func() {
		s.eventsMu.Lock()
		delete(s.fileSubscribers, updates)
		s.eventsMu.Unlock()
	}
}

func (s *server) notifyFilesChanged() {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	for subscriber := range s.fileSubscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 请求")
		return
	}
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "仅运行 Lan Copy 的电脑可以关闭软件")
		return
	}
	if r.Header.Get("X-Lan-Copy-Action") != "shutdown" {
		writeError(w, http.StatusForbidden, "无效的关闭请求")
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
		Uploads      *int   `json:"uploads"`
		Downloads    *int   `json:"downloads"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil {
			writeError(w, http.StatusBadRequest, "关停确认数据无效")
			return
		}
	}
	if input.Confirmation != "" && input.Confirmation != shutdownConfirmation {
		writeError(w, http.StatusBadRequest, "确认信息不匹配")
		return
	}
	if (input.Uploads == nil) != (input.Downloads == nil) || (input.Uploads != nil && (*input.Uploads < 0 || *input.Downloads < 0)) {
		writeError(w, http.StatusBadRequest, "关停传输计数无效")
		return
	}
	var expected *transferCounts
	if input.Uploads != nil {
		expected = &transferCounts{Uploads: *input.Uploads, Downloads: *input.Downloads}
	}
	counts, started := s.transfers.beginShutdown(input.Confirmation == shutdownConfirmation, expected)
	if !started {
		message := "仍有文件传输正在进行。确认强制关闭可能中断这些传输。"
		if expected != nil && *expected != counts {
			message = "文件传输数量已变化，请核对最新数量后再次确认。"
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":                message,
			"uploads":              counts.Uploads,
			"downloads":            counts.Downloads,
			"requiresConfirmation": true,
		})
		return
	}
	s.requestShutdown("web")
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "shutting_down", "uploads": counts.Uploads, "downloads": counts.Downloads})
}

const shutdownConfirmation = "强制关闭"

func (s *server) shutdownStatus(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "仅 localhost 可以查看关停状态")
		return
	}
	counts, stopping := s.transfers.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"uploads":   counts.Uploads,
		"downloads": counts.Downloads,
		"stopping":  stopping,
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

func (s *server) listFiles(w http.ResponseWriter, _ *http.Request) {
	// Listing is a best-effort read. Do not hold the mutation lock during a
	// potentially slow recursive disk walk; an entry changed during the walk is
	// omitted (or reported as a walk error) and the client can refresh.
	files := make([]fileInfo, 0)
	err := filepath.WalkDir(s.dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil // An in-app mutation removed this entry during the snapshot.
			}
			return walkErr
		}
		if filePath == s.dir {
			return nil
		}
		relative, err := filepath.Rel(s.dir, filePath)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if _, err := s.sharedPath(name); err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, fileInfo{Name: name, Size: info.Size(), ModTime: info.ModTime(), Type: contentType(name), PreviewType: previewContentType(name)})
		}
		return nil
	})
	if err != nil {
		writeError(w, 500, "无法读取文件列表")
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *server) downloadFile(w http.ResponseWriter, r *http.Request) {
	s.serveSharedFile(w, r, false)
}

func (s *server) previewFile(w http.ResponseWriter, r *http.Request) {
	s.serveSharedFile(w, r, true)
}

func (s *server) serveSharedFile(w http.ResponseWriter, r *http.Request, preview bool) {
	name := r.PathValue("name")
	resolved, err := s.sharedPath(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	filePath := resolved
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开文件")
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开文件")
		return
	}
	defer file.Close()
	disposition, mediaType := "attachment", contentType(name)
	if preview {
		mediaType = previewContentType(name)
		if mediaType == "" {
			writeError(w, http.StatusUnsupportedMediaType, "此文件暂不支持预览，请下载后打开")
			return
		}
		disposition = "inline"
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+url.PathEscape(path.Base(name)))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (s *server) handleLocalFileAction(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, "仅运行服务的本机可以执行此操作")
		return
	}
	action := r.PathValue("action")
	if action != "open" && action != "folder" {
		writeError(w, http.StatusNotFound, "未知的文件操作")
		return
	}
	if r.Header.Get("X-Lan-Copy-Action") != action {
		writeError(w, http.StatusForbidden, "无效的文件操作")
		return
	}
	name := r.PathValue("name")
	resolved, err := s.sharedPath(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	filePath, err := filepath.Abs(resolved)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法定位文件")
		return
	}
	info, err := os.Stat(filePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取文件")
		return
	}
	if err := s.openPath(filePath, action); err != nil {
		writeError(w, http.StatusInternalServerError, "无法执行本机文件操作")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "opened"})
}

func isLocalRequest(r *http.Request) bool {
	requestIP := remoteIP(r)
	if requestIP == nil {
		return false
	}
	if requestIP.IsLoopback() {
		return true
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if local, ok := address.(*net.IPNet); ok && local.IP.Equal(requestIP) {
			return true
		}
	}
	return false
}

func isLoopbackRequest(r *http.Request) bool {
	requestIP := remoteIP(r)
	if requestIP == nil || !requestIP.IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	hostIP := net.ParseIP(strings.Trim(host, "[]"))
	return hostIP != nil && hostIP.IsLoopback()
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	requestIP := net.ParseIP(strings.Trim(host, "[]"))
	return requestIP
}

func openLocalPath(filePath, action string) error {
	target := filePath
	if action == "folder" {
		target = filepath.Dir(filePath)
	}
	return openDesktopPath(target)
}

func (s *server) deleteFile(w http.ResponseWriter, r *http.Request) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	name := r.PathValue("name")
	resolved, err := s.sharedPath(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	info, err := os.Stat(resolved)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法删除文件")
		return
	}
	if err := os.Remove(resolved); err != nil {
		writeError(w, http.StatusInternalServerError, "无法删除文件")
		return
	}
	s.notifyFilesChanged()
	w.WriteHeader(http.StatusNoContent)
}

func safeName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	if err := validateSharedComponent(name); err != nil {
		return "", errors.New("invalid file name")
	}
	return name, nil
}

const maxAvailableNameAttempts = 10000

func availableName(dir, name string) (string, error) {
	return availableNameWithLstat(dir, name, os.Lstat)
}

func availableNameWithLstat(dir, name string, lstat func(string) (os.FileInfo, error)) (string, error) {
	if _, err := lstat(filepath.Join(dir, name)); err == nil {
		// Try numbered names below.
	} else if errors.Is(err, os.ErrNotExist) {
		return name, nil
	} else {
		return "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; i <= maxAvailableNameAttempts; i++ {
		candidate := base + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := lstat(filepath.Join(dir, candidate)); err == nil {
			continue
		} else if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else {
			return "", err
		}
	}
	return "", fs.ErrExist
}

func contentType(name string) string {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); value != "" {
		return value
	}
	return "application/octet-stream"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func localURLs(port string) []string {
	if host, _, err := net.SplitHostPort(port); err == nil {
		port = strings.TrimPrefix(port, host+":")
	}
	addresses, _ := net.InterfaceAddrs()
	urls := make([]string, 0)
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ipNet.IP.String(), port))
	}
	sortLocalURLs(urls)
	return urls
}

func sortLocalURLs(urls []string) {
	sort.Slice(urls, func(i, j int) bool {
		leftPreferred := strings.HasPrefix(urls[i], "http://192.")
		rightPreferred := strings.HasPrefix(urls[j], "http://192.")
		if leftPreferred != rightPreferred {
			return leftPreferred
		}
		return urls[i] < urls[j]
	})
}

func reachableLocalURLs(ctx context.Context, urls []string) []string {
	const probeTimeout = 1200 * time.Millisecond
	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: probeTimeout}
	reachable := make([]bool, len(urls))
	var probes sync.WaitGroup

	for index, address := range urls {
		probes.Add(1)
		go func(resultIndex int, baseURL string) {
			defer probes.Done()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
			if err != nil {
				return
			}
			response, err := client.Do(request)
			if err != nil {
				return
			}
			_ = response.Body.Close()
			reachable[resultIndex] = response.StatusCode == http.StatusOK
		}(index, address)
	}
	probes.Wait()
	transport.CloseIdleConnections()

	result := make([]string, 0, len(urls))
	for index, address := range urls {
		if reachable[index] {
			result = append(result, address)
		}
	}
	return result
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--internal-autostart" {
		hideStartupConsole()
		args, err := startupLaunchArgs()
		if err != nil {
			log.Fatal(err)
		}
		os.Args = append(os.Args[:1], args...)
	}
	if len(os.Args) == 3 && os.Args[1] == "--internal-updater" {
		if err := runUpdateHelper(os.Args[2]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	listen := flag.String("listen", ":8080", "监听地址")
	dir := flag.String("dir", defaultStorageName(systemLanguage()), "文件存储目录")
	maxGB := flag.Int64("max-gb", 10, "单次上传总大小上限（GiB）")
	autostart := flag.Bool("autostart", false, "登录后静默启动（供系统启动项使用）")
	workingDir := flag.String("working-dir", "", "启动工作目录（供系统启动项使用）")
	flag.Parse()
	if *autostart {
		hideStartupConsole()
	}
	if *workingDir != "" {
		if err := os.Chdir(*workingDir); err != nil {
			log.Fatal(err)
		}
	}

	customDir := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "dir" {
			customDir = true
		}
	})
	setupConsole()
	// Acquire the listener before moving any legacy storage. A second launch must
	// never rename a directory that an already-running instance is using.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		if *autostart {
			if found, _ := openRunningInstance(*listen, func(string) error { return nil }); found {
				return
			}
		} else if notifyAlreadyRunning(*listen, *dir) {
			return
		}
		log.Fatal(err)
	}
	if !customDir {
		if err := migrateDefaultStorage(*dir); err != nil {
			log.Fatal(err)
		}
	}
	app, err := newServer(*dir, *maxGB<<30)
	if err != nil {
		log.Fatal(err)
	}
	app.updater = configureUpdater(listener.Addr().String(), *dir)
	app.autostart = configureAutostart(listener.Addr().String(), *dir, *maxGB)
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	log.Printf("Lan Copy 已启动，文件保存在 %s", *dir)
	log.Printf("本机访问: http://localhost:%s", port)
	for _, address := range localURLs(fmt.Sprintf(":%s", port)) {
		log.Printf("局域网访问: %s", address)
	}
	fmt.Printf("\n请在浏览器打开 http://localhost:%s 查看功能。\n", port)
	httpServer := &http.Server{
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stopSignals()
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()
	if !*autostart && os.Getenv("LAN_COPY_UPDATE_ID") == "" && openBrowserOnLaunch() {
		if err := openBrowser(fmt.Sprintf("http://localhost:%s", port)); err != nil {
			log.Printf("无法自动打开浏览器，请手动访问 http://localhost:%s（%v）", port, err)
		}
	}
	if !*autostart && os.Getenv("LAN_COPY_UPDATE_ID") == "" {
		go waitForAnyKeyToCloseConsole()
	}

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	case <-signalContext.Done():
		app.requestShutdown("system signal")
	case <-app.shutdownRequested:
	}
	log.Printf("正在停止 Lan Copy（%s）", app.shutdownReason)
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		log.Printf("优雅关闭失败: %v", err)
		counts, _ := app.transfers.snapshot()
		log.Printf("关闭期限到达；强制终止时仍有上传 %d 个、下载 %d 个", counts.Uploads, counts.Downloads)
		if closeErr := httpServer.Close(); closeErr != nil {
			log.Printf("强制关闭失败: %v", closeErr)
		}
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
