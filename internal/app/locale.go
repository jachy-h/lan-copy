package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func DefaultStorageName(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "zh" || strings.HasPrefix(language, "zh-") || strings.HasPrefix(language, "zh_") {
		return "共享文件夹-lancopy"
	}
	return "shared-folder-lancopy"
}

// Preserve existing shares when adopting the localized default directory name.
// Explicit -dir paths are never renamed.
func MigrateDefaultStorage(destination string) error {
	return migrateStorageDirectory("lan-copy-data", destination)
}

func migrateStorageDirectory(legacy, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(legacy); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Rename(legacy, destination); err != nil {
		return fmt.Errorf("迁移原共享文件夹失败: %w", err)
	}
	return nil
}
