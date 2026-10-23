package filestore

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type Conflict struct{ Names []string }

func (e *Conflict) Error() string { return "upload requires replacement confirmation" }

func (s *Store) Commit(stage, destination string, roots []string, allowed map[string]bool, onPartial func()) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(roots) == 0 {
		return nil, fs.ErrInvalid
	}
	for _, root := range roots {
		if err := ValidateComponent(root); err != nil {
			return nil, fs.ErrInvalid
		}
	}
	target, err := s.Path(destination)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fs.ErrInvalid
	}
	originalRoots := append([]string(nil), roots...)
	var conflicts []string
	var existing []string
	rootNames := make(map[string]string, len(roots))
	finalRoots := make([]string, 0, len(roots))
	for _, root := range originalRoots {
		info, err := os.Lstat(filepath.Join(target, root))
		if errors.Is(err, fs.ErrNotExist) {
			rootNames[root] = root
			finalRoots = append(finalRoots, root)
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fs.ErrPermission
		}
		if allowed[root] {
			existing = append(existing, root)
			rootNames[root] = root
			finalRoots = append(finalRoots, root)
			continue
		}
		stagedInfo, err := os.Lstat(filepath.Join(stage, root))
		if err != nil {
			return nil, err
		}
		if stagedInfo.Mode().IsRegular() {
			name, err := AvailableNameWithLstat(target, root, func(candidate string) (os.FileInfo, error) {
				info, err := os.Lstat(candidate)
				if err == nil || !errors.Is(err, os.ErrNotExist) {
					return info, err
				}
				return os.Lstat(filepath.Join(stage, filepath.Base(candidate)))
			})
			if err != nil {
				return nil, err
			}
			if err := os.Rename(filepath.Join(stage, root), filepath.Join(stage, name)); err != nil {
				return nil, err
			}
			rootNames[root] = name
			finalRoots = append(finalRoots, name)
			continue
		}
		conflicts = append(conflicts, root)
	}
	if len(conflicts) > 0 {
		return nil, &Conflict{Names: conflicts}
	}
	transaction := filepath.Dir(stage)
	backup := filepath.Join(transaction, "backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		return nil, err
	}
	record := TransactionRecord{Destination: destination, Roots: append([]string(nil), finalRoots...), Replaced: append([]string(nil), existing...)}
	recordBytes, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	recordFile, err := os.OpenFile(filepath.Join(transaction, "transaction.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := recordFile.Write(recordBytes); err != nil {
		_ = recordFile.Close()
		return nil, err
	}
	if err := recordFile.Sync(); err != nil {
		_ = recordFile.Close()
		return nil, err
	}
	if err := recordFile.Close(); err != nil {
		return nil, err
	}
	if err := syncDirectory(transaction); err != nil {
		return nil, err
	}
	// Preserve backups if rollback fails so the originals remain recoverable.
	cleanBackup := true
	defer func() {
		if cleanBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	names := make(map[string]string)
	var backedUp []string
	rollback := func(cause error) (map[string]string, error) {
		errs := []error{cause}
		for _, final := range names {
			if err := os.Rename(filepath.Join(target, final), filepath.Join(stage, final)); err != nil {
				errs = append(errs, err)
			}
		}
		for _, root := range backedUp {
			if err := os.Rename(filepath.Join(backup, root), filepath.Join(target, root)); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 1 {
			cleanBackup = false
			if onPartial != nil {
				onPartial()
			}
			return nil, &RecoveryRequired{cause: errors.Join(errs...)}
		}
		return nil, cause
	}
	for _, root := range existing {
		if err := os.Rename(filepath.Join(target, root), filepath.Join(backup, root)); err != nil {
			return rollback(err)
		}
		backedUp = append(backedUp, root)
	}
	for _, root := range finalRoots {
		if err := os.Rename(filepath.Join(stage, root), filepath.Join(target, root)); err != nil {
			return rollback(err)
		}
		for original, final := range rootNames {
			if final == root {
				names[original] = final
			}
		}
	}
	markerTemp, err := os.CreateTemp(transaction, ".committed-*")
	if err != nil {
		return rollback(err)
	}
	markerTempPath := markerTemp.Name()
	if _, err := io.WriteString(markerTemp, CommitMarker); err != nil {
		_ = markerTemp.Close()
		_ = os.Remove(markerTempPath)
		return rollback(err)
	}
	if err := markerTemp.Sync(); err != nil {
		_ = markerTemp.Close()
		_ = os.Remove(markerTempPath)
		return rollback(err)
	}
	if err := markerTemp.Close(); err != nil {
		_ = os.Remove(markerTempPath)
		return rollback(err)
	}
	if err := os.Rename(markerTempPath, filepath.Join(transaction, "committed")); err != nil {
		_ = os.Remove(markerTempPath)
		return rollback(err)
	}
	if err := syncDirectory(transaction); err != nil {
		return rollback(err)
	}
	return names, nil
}
