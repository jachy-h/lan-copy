package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"lan-copy/internal/desktop"
	"lan-copy/internal/httpserver"
	"lan-copy/internal/printing"
	"lan-copy/internal/updater"
)

func Run(webFiles fs.FS, version, updatePublicKey string) {
	if len(os.Args) == 2 && os.Args[1] == "--internal-autostart" {
		desktop.HideStartupConsole()
		args, err := desktop.StartupLaunchArgs()
		if err != nil {
			log.Fatal(err)
		}
		os.Args = append(os.Args[:1], args...)
	}
	if len(os.Args) == 3 && os.Args[1] == "--internal-updater" {
		if err := updater.RunHelper(os.Args[2]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	listen := flag.String("listen", ":8080", "监听地址")
	dir := flag.String("dir", DefaultStorageName(desktop.SystemLanguage()), "文件存储目录")
	maxGB := flag.Int64("max-gb", 10, "单次上传总大小上限（GiB）")
	autostart := flag.Bool("autostart", false, "登录后静默启动（供系统启动项使用）")
	workingDir := flag.String("working-dir", "", "启动工作目录（供系统启动项使用）")
	flag.Parse()
	if *autostart {
		desktop.HideStartupConsole()
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
	desktop.SetupConsole()
	// Acquire the listener before moving any legacy storage. A second launch must
	// never rename a directory that an already-running instance is using.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		if *autostart {
			if found, _ := OpenRunningInstance(*listen, func(string) error { return nil }); found {
				return
			}
		} else if desktop.NotifyAlreadyRunning(func() (bool, error) { return OpenRunningInstance(*listen, desktop.OpenBrowser) }) {
			return
		}
		log.Fatal(err)
	}
	if !customDir {
		if err := MigrateDefaultStorage(*dir); err != nil {
			log.Fatal(err)
		}
	}
	components, err := Prepare(*dir)
	if err != nil {
		log.Fatal(err)
	}
	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	updateService := updater.Configure(listener.Addr().String(), *dir)
	updateService.CleanupHistory()
	printService, err := printing.New(components.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	service := httpserver.New(httpserver.Config{
		Dir: components.Dir, DataDir: components.DataDir,
		Files: components.Files, Texts: components.Texts,
		UploadResources: components.UploadResources, UploadPreflight: components.UploadPreflight,
		Releases: components.Releases, Web: webRoot,
		Version: version, UpdatePublicKey: updatePublicKey, MaxUpload: *maxGB << 30,
		Updater:   updateService,
		Printing:  printService,
		Autostart: desktop.ConfigureAutostart(listener.Addr().String(), *dir, *maxGB),
	})
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	log.Printf("Lan Copy 已启动，文件保存在 %s", *dir)
	log.Printf("本机访问: http://localhost:%s", port)
	for _, address := range httpserver.LocalURLs(fmt.Sprintf(":%s", port)) {
		log.Printf("局域网访问: %s", address)
	}
	fmt.Printf("\n请在浏览器打开 http://localhost:%s 查看功能。\n", port)
	httpServer := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), ShutdownSignals()...)
	defer stopSignals()
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()
	if !*autostart && os.Getenv("LAN_COPY_UPDATE_ID") == "" && desktop.OpenBrowserOnLaunch() {
		if err := desktop.OpenBrowser(fmt.Sprintf("http://localhost:%s", port)); err != nil {
			log.Printf("无法自动打开浏览器，请手动访问 http://localhost:%s（%v）", port, err)
		}
	}
	if !*autostart && os.Getenv("LAN_COPY_UPDATE_ID") == "" {
		go desktop.WaitForAnyKeyToCloseConsole()
	}

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	case <-signalContext.Done():
		service.RequestShutdown("system signal")
	case <-service.ShutdownRequested():
	}
	log.Printf("正在停止 Lan Copy（%s）", service.ShutdownReason())
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		log.Printf("优雅关闭失败: %v", err)
		uploads, downloads := service.TransferCounts()
		log.Printf("关闭期限到达；强制终止时仍有上传 %d 个、下载 %d 个", uploads, downloads)
		if closeErr := httpServer.Close(); closeErr != nil {
			log.Printf("强制关闭失败: %v", closeErr)
		}
	}
	if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
