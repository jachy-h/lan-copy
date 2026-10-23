package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lan-copy/internal/filestore"
	"lan-copy/internal/textstore"
	"lan-copy/internal/updater"
)

const appDataDirectoryPrefix = ".lan-copy-app-data-"

type Components struct {
	Dir             string
	DataDir         string
	Files           *filestore.Store
	Texts           *textstore.Store
	UploadResources *filestore.Resources
	UploadPreflight *filestore.PreflightManager
	Releases        *updater.ReleaseService
}

// Prepare restores persistent state before HTTP requests can reach components.
func Prepare(dir string) (Components, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return Components{}, fmt.Errorf("resolve storage directory: %w", err)
	}
	dataDir := DataDirectory(absDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return Components{}, fmt.Errorf("create storage directory: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Components{}, fmt.Errorf("create application data directory: %w", err)
	}
	if err := migrateInternalData(absDir, dataDir); err != nil {
		return Components{}, fmt.Errorf("migrate application data: %w", err)
	}
	if err := textstore.Recover(dataDir); err != nil {
		return Components{}, fmt.Errorf("recover shared text store: %w", err)
	}
	if err := filestore.RecoverTransactions(absDir, dataDir); err != nil {
		return Components{}, fmt.Errorf("recover upload transactions: %w", err)
	}
	uploadConfig, err := filestore.LoadResourceConfig(dataDir)
	if err != nil {
		return Components{}, fmt.Errorf("load upload resource configuration: %w", err)
	}
	return Components{
		Dir: absDir, DataDir: dataDir,
		Files: filestore.New(absDir, dataDir), Texts: textstore.New(dataDir),
		UploadResources: filestore.NewResources(uploadConfig),
		UploadPreflight: filestore.NewPreflightManager(),
		Releases:        updater.NewReleaseService(),
	}, nil
}

// DataDirectory keeps private state beside the selected shared directory.
func DataDirectory(sharedDir string) string {
	abs, err := filepath.Abs(sharedDir)
	if err != nil {
		abs = filepath.Clean(sharedDir)
	}
	return filepath.Join(filepath.Dir(abs), appDataDirectoryPrefix+filepath.Base(abs))
}

// migrateInternalData moves state from older releases out of the shared folder.
// Rename preserves upload transaction state without exposing a partially copied
// transaction to startup recovery.
func migrateInternalData(sharedDir, dataDir string) error {
	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		return err
	}
	moved := false
	for _, entry := range entries {
		name := entry.Name()
		if name != textstore.FileName && name != textstore.BackupFileName && !strings.HasPrefix(name, ".lan-copy-texts-") && !strings.HasPrefix(name, filestore.TransactionPrefix) {
			continue
		}
		source := filepath.Join(sharedDir, name)
		destination := filepath.Join(dataDir, name)
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("both legacy and application data exist for %s", name)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("move legacy internal data %s: %w", name, err)
		}
		moved = true
	}
	if moved {
		if err := syncDirectory(sharedDir); err != nil {
			return err
		}
		if err := syncDirectory(dataDir); err != nil {
			return err
		}
	}
	return nil
}
