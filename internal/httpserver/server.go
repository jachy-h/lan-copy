package httpserver

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"lan-copy/internal/desktop"
	"lan-copy/internal/filestore"
	"lan-copy/internal/printing"
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
	Dir, DataDir, Version, UpdatePublicKey string
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
}

// Server owns HTTP state; persistent state and its locks live in components.
type Server struct {
	printing          *printing.Service
	updater           *updater.Service
	releases          *updater.ReleaseService
	autostart         *desktop.Autostart
	version           string
	updatePublicKey   string
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
}

func New(config Config) *Server {
	appContext, cancelApp := context.WithCancel(context.Background())
	return &Server{
		printing: config.Printing,
		updater:  config.Updater, autostart: config.Autostart,
		version: config.Version, updatePublicKey: config.UpdatePublicKey,
		releases:          config.Releases,
		texts:             config.Texts,
		files:             config.Files,
		dir:               config.Dir,
		dataDir:           config.DataDir,
		maxUpload:         config.MaxUpload,
		uploadResources:   config.UploadResources,
		uploadPreflight:   config.UploadPreflight,
		web:               StaticFiles(config.Web),
		probeURLs:         ReachableLocalURLs,
		openPath:          openLocalPath,
		changeSubscribers: make(map[chan changeEvent]struct{}),
		shutdownRequested: make(chan struct{}),
		appContext:        appContext,
		cancelApp:         cancelApp,
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
