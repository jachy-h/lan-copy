package httpserver

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"lan-copy/internal/filestore"
	"lan-copy/internal/textstore"
	"lan-copy/internal/updater"
)

// HTTP tests assemble the same components without importing app, which owns
// production startup and depends on this package.
type server = Server

var version = "dev"
var updatePublicKey string

func testModuleRoot() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot locate HTTP test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func newServer(dir string, maxUpload int64) (*Server, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(filepath.Dir(absDir), ".lan-copy-app-data-"+filepath.Base(absDir))
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != textstore.FileName && name != textstore.BackupFileName && !strings.HasPrefix(name, ".lan-copy-texts-") && !strings.HasPrefix(name, filestore.TransactionPrefix) {
			continue
		}
		if err := os.Rename(filepath.Join(absDir, name), filepath.Join(dataDir, name)); err != nil {
			return nil, err
		}
	}
	if err := textstore.Recover(dataDir); err != nil {
		return nil, err
	}
	if err := filestore.RecoverTransactions(absDir, dataDir); err != nil {
		return nil, err
	}
	uploadConfig, err := filestore.LoadResourceConfig(dataDir)
	if err != nil {
		return nil, err
	}
	webRoot, err := fs.Sub(os.DirFS(testModuleRoot()), "web")
	if err != nil {
		return nil, err
	}
	return New(Config{
		Dir: absDir, DataDir: dataDir, MaxUpload: maxUpload,
		Files: filestore.New(absDir, dataDir), Texts: textstore.New(dataDir),
		UploadResources: filestore.NewResources(uploadConfig), UploadPreflight: filestore.NewPreflightManager(),
		Releases: updater.NewReleaseService(), Web: webRoot,
		Version: version, UpdatePublicKey: updatePublicKey,
	}), nil
}
