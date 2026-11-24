package httpserver

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"lan-copy/internal/deploytoken"
	"lan-copy/internal/desktop"
	"lan-copy/internal/filestore"
	"lan-copy/internal/plugin"
	"lan-copy/internal/printing"
	"lan-copy/internal/processstats"
	"lan-copy/internal/tablestore"
	"lan-copy/internal/textstore"
	"lan-copy/internal/updater"
)

const defaultMaxUpload = int64(10 << 30) // 10 GiB

type fileInfo struct {
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modified"`
	Type        string    `json:"type"`
	PreviewType string    `json:"previewType"`
}

type Config struct {
	AirDev                                 bool
	Dir, DataDir, Version, UpdatePublicKey string
	DeployAuth                             *deploytoken.Store
	MaxUpload                              int64
	Web                                    fs.FS
	Files                                  *filestore.Store
	Texts                                  *textstore.Store
	UploadResources                        *filestore.Resources
	UploadPreflight                        *filestore.PreflightManager
	Releases                               *updater.ReleaseService
	Updater                                *updater.Service
	Autostart                              *desktop.Autostart
	Printing                               *printing.Service
	Plugins                                *plugin.Manager
	Tables                                 *tablestore.Store
	System                                 *processstats.Sampler
}

// Server owns HTTP state; persistent state and its locks live in components.
type Server struct {
	airDev            bool
	printing          *printing.Service
	plugins           *plugin.Manager
	tables            *tablestore.Store
	updater           *updater.Service
	releases          *updater.ReleaseService
	autostart         *desktop.Autostart
	version           string
	updatePublicKey   string
	deployAuth        *deploytoken.Store
	dir               string
	dataDir           string
	maxUpload         int64
	uploadResources   *filestore.Resources
	uploadPreflight   *filestore.PreflightManager
	files             *filestore.Store
	texts             *textstore.Store
	web               http.Handler
	probeURLs         func(context.Context, []string) []string
	openPath          func(string, string) error
	eventsMu          sync.Mutex
	changeSubscribers map[chan changeEvent]struct{}
	changeVersion     uint64
	shutdownRequested chan struct{}
	shutdownOnce      sync.Once
	shutdownReason    string
	appContext        context.Context
	cancelApp         context.CancelFunc
	transfers         transferTracker
	system            *processstats.Sampler

	// pluginProxyTransport serves plugin browser interfaces with long-lived
	// streaming semantics instead of the plugin API call timeouts.
	pluginProxyTransport *http.Transport

	// publishPluginEvent overrides lifecycle publishing in tests. Production
	// leaves it nil and goes through the plugin manager.
	publishPluginEvent func(string, any)

	// runPluginUploadHooks overrides synchronous upload-hook invocation in
	// tests. Production leaves it nil and goes through the plugin manager.
	runPluginUploadHooks func(context.Context, plugin.FileUploadedData) []plugin.UploadHookDialog

	// pluginUITarget overrides plugin target resolution in proxy tests.
	pluginUITarget func(string) (plugin.ProxyTarget, error)
}

func New(config Config) *Server {
	appContext, cancelApp := context.WithCancel(context.Background())
	system := config.System
	if system == nil {
		system = processstats.New()
	}
	return &Server{
		airDev:     config.AirDev,
		deployAuth: config.DeployAuth,
		printing:   config.Printing,
		plugins:    config.Plugins,
		tables:     config.Tables,
		updater:    config.Updater, autostart: config.Autostart,
		version: config.Version, updatePublicKey: config.UpdatePublicKey,
		releases:             config.Releases,
		texts:                config.Texts,
		files:                config.Files,
		dir:                  config.Dir,
		dataDir:              config.DataDir,
		maxUpload:            config.MaxUpload,
		uploadResources:      config.UploadResources,
		uploadPreflight:      config.UploadPreflight,
		web:                  StaticFiles(config.Web),
		probeURLs:            ReachableLocalURLs,
		openPath:             openLocalPath,
		changeSubscribers:    make(map[chan changeEvent]struct{}),
		shutdownRequested:    make(chan struct{}),
		appContext:           appContext,
		cancelApp:            cancelApp,
		system:               system,
		pluginProxyTransport: newPluginProxyTransport(),
	}
}

func (s *Server) Handler() http.Handler { return s.routes() }
func (s *Server) Configure(update *updater.Service, autostart *desktop.Autostart) {
	s.updater, s.autostart = update, autostart
}
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownRequested }
func (s *Server) RequestShutdown(reason string)      { s.requestShutdown(reason) }
func (s *Server) ShutdownReason() string             { return s.shutdownReason }
func (s *Server) TransferCounts() (int, int) {
	counts, _ := s.transfers.snapshot()
	return counts.Uploads, counts.Downloads
}
