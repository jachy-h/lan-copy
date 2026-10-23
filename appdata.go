package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appDataDirectoryPrefix = ".lan-copy-app-data-"

// appDataDirectory keeps private state beside the selected shared directory.
func appDataDirectory(sharedDir string) string {
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
		if name != textStore && name != textStoreBackup && !strings.HasPrefix(name, ".lan-copy-texts-") && !strings.HasPrefix(name, uploadTransactionPrefix) {
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
