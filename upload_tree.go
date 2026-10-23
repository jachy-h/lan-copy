package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxTreeEntries = 10000
const maxTreeManifest = 2 << 20
const uploadTransactionPrefix = ".upload-txn-"
const uploadCommitMarker = "lan-copy-upload-committed\n"

type uploadTransactionRecord struct {
	Destination string   `json:"destination"`
	Roots       []string `json:"roots"`
	Replaced    []string `json:"replaced"`
}

type uploadReceiveRecord struct {
	ID          string `json:"id"`
	Destination string `json:"destination"`
	State       string `json:"state"`
}

type uploadRecoveryRequired struct{ cause error }

func (e *uploadRecoveryRequired) Error() string {
	return "upload rollback requires startup recovery: " + e.cause.Error()
}
func (e *uploadRecoveryRequired) Unwrap() error { return e.cause }

func writeDurableFile(name string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

// recoverUploadTransactions rolls back commits interrupted by process exit.
// The transaction directory shares the storage filesystem, so each individual
// rename is atomic and its state can be inferred from stage/backup/target.
func recoverUploadTransactions(dir string) error {
	return recoverUploadTransactionsAt(dir, dir)
}

func recoverUploadTransactionsAt(sharedDir, dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	resolver := &server{dir: sharedDir}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), uploadTransactionPrefix) {
			continue
		}
		transaction := filepath.Join(dataDir, entry.Name())
		var receive uploadReceiveRecord
		if data, readErr := os.ReadFile(filepath.Join(transaction, "receive.json")); readErr == nil {
			if err := json.Unmarshal(data, &receive); err != nil || receive.ID == "" || receive.State != "receiving" {
				log.Printf("异常: 上传接收记录无效，事务目录=%s 错误=%v", entry.Name(), err)
				receive = uploadReceiveRecord{}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read upload receive record %s: %w", entry.Name(), readErr)
		}
		if marker, err := os.ReadFile(filepath.Join(transaction, "committed")); err == nil && string(marker) == uploadCommitMarker {
			if err := os.RemoveAll(transaction); err != nil {
				return fmt.Errorf("clean committed upload transaction %s: %w", entry.Name(), err)
			}
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recordPath := filepath.Join(transaction, "transaction.json")
		data, readErr := os.ReadFile(recordPath)
		if errors.Is(readErr, os.ErrNotExist) {
			// No target mutation can happen before the durable record exists.
			id, targetName := receive.ID, receive.Destination
			if id == "" {
				id = strings.TrimPrefix(entry.Name(), uploadTransactionPrefix)
			}
			log.Printf("异常: 检测到中断上传，事务=%s 目标=%q 状态=%s；临时数据将清理", id, targetName, receive.State)
			if err := os.RemoveAll(transaction); err != nil {
				log.Printf("异常: 清理中断上传失败，事务目录=%s 错误=%v", entry.Name(), err)
				return err
			}
			continue
		}
		if readErr != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), readErr)
		}
		var record uploadTransactionRecord
		if err := json.Unmarshal(data, &record); err != nil || len(record.Roots) == 0 {
			return fmt.Errorf("invalid upload transaction record %s", entry.Name())
		}
		target, err := resolver.sharedPath(record.Destination)
		if err != nil {
			return fmt.Errorf("resolve upload transaction target %s: %w", entry.Name(), err)
		}
		stage := filepath.Join(transaction, "stage")
		backup := filepath.Join(transaction, "backup")
		replaced := make(map[string]bool, len(record.Replaced))
		for _, root := range record.Replaced {
			if err := validateSharedComponent(root); err != nil {
				return fmt.Errorf("invalid replaced root in %s", entry.Name())
			}
			replaced[root] = true
		}
		for i := len(record.Roots) - 1; i >= 0; i-- {
			root := record.Roots[i]
			if err := validateSharedComponent(root); err != nil {
				return fmt.Errorf("invalid root in %s", entry.Name())
			}
			targetRoot := filepath.Join(target, root)
			backupRoot := filepath.Join(backup, root)
			if _, err := os.Lstat(backupRoot); err == nil {
				if err := os.RemoveAll(targetRoot); err != nil {
					return fmt.Errorf("remove interrupted upload %s: %w", root, err)
				}
				if err := os.Rename(backupRoot, targetRoot); err != nil {
					return fmt.Errorf("restore replaced upload %s: %w", root, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			} else if !replaced[root] {
				if _, stageErr := os.Lstat(filepath.Join(stage, root)); errors.Is(stageErr, os.ErrNotExist) {
					if err := os.RemoveAll(targetRoot); err != nil {
						return fmt.Errorf("remove interrupted upload %s: %w", root, err)
					}
				} else if stageErr != nil {
					return stageErr
				}
			}
		}
		if err := os.RemoveAll(transaction); err != nil {
			log.Printf("异常: 清理已恢复上传事务失败，事务目录=%s 错误=%v", entry.Name(), err)
			return fmt.Errorf("remove recovered upload transaction %s: %w", entry.Name(), err)
		}
		log.Printf("异常恢复: 已回滚未完成上传，事务目录=%s", entry.Name())
	}
	return nil
}

type uploadTreeEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
}

type uploadTreeManifest struct {
	Entries []uploadTreeEntry `json:"entries"`
	Replace []string          `json:"replace,omitempty"`
}

// All paths are relative to a private staging directory, never to the host.
// Reject case aliases too, so the same manifest works on Windows and macOS.
func validateUploadTree(entries []uploadTreeEntry) ([]string, []string, error) {
	if len(entries) == 0 || len(entries) > maxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	explicit := make(map[string]bool)
	canonical := make(map[string]string)
	directories := make(map[string]bool)
	roots := make(map[string]bool)
	for _, entry := range entries {
		if len(entry.Path) > 4096 || entry.Path == "" || path.Clean(entry.Path) != entry.Path || strings.Contains(entry.Path, "\\") || strings.HasPrefix(entry.Path, "/") {
			return nil, nil, fs.ErrInvalid
		}
		components := strings.Split(entry.Path, "/")
		if len(components) > 64 {
			return nil, nil, fs.ErrInvalid
		}
		for index, component := range components {
			if err := validateSharedComponent(component); err != nil {
				return nil, nil, fs.ErrInvalid
			}
			prefix := strings.Join(components[:index+1], "/")
			key := strings.ToLower(prefix)
			if existing, ok := canonical[key]; ok && existing != prefix {
				return nil, nil, fs.ErrInvalid
			}
			canonical[key] = prefix
			if index < len(components)-1 {
				directories[prefix] = true
			}
		}
		if _, exists := explicit[entry.Path]; exists {
			return nil, nil, fs.ErrInvalid
		}
		explicit[entry.Path] = entry.Directory
		if entry.Directory {
			directories[entry.Path] = true
		}
		roots[components[0]] = true
	}
	for directory := range directories {
		if isDirectory, exists := explicit[directory]; exists && !isDirectory {
			return nil, nil, fs.ErrInvalid
		}
	}
	// Implicit parent directories also count toward the operation limit.
	if len(canonical) > maxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	dirs := make([]string, 0, len(directories))
	top := make([]string, 0, len(roots))
	for directory := range directories {
		dirs = append(dirs, directory)
	}
	for root := range roots {
		top = append(top, root)
	}
	sort.Strings(dirs)
	sort.Strings(top)
	return dirs, top, nil
}

func treeUploadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "文件总大小超过上传限制")
		return
	}
	writeError(w, http.StatusBadRequest, "上传数据不完整，请重新上传文件夹")
}

func (s *server) uploadTree(w http.ResponseWriter, r *http.Request) {
	destination := r.URL.Query().Get("path")
	// Check the destination before receiving data, then revalidate at commit time.
	s.fileMu.Lock()
	target, err := s.sharedPath(destination)
	if err == nil {
		var info fs.FileInfo
		info, err = os.Stat(target)
		if err == nil && !info.IsDir() {
			err = fs.ErrInvalid
		}
	}
	s.fileMu.Unlock()
	if err != nil {
		filesystemError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "请选择要上传的文件或文件夹")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		treeUploadError(w, err)
		return
	}
	if part.FormName() != "manifest" || part.FileName() != "" {
		writeError(w, 400, "缺少文件夹目录清单")
		return
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(part, maxTreeManifest+1))
	if err != nil {
		treeUploadError(w, err)
		return
	}
	if len(manifestBytes) > maxTreeManifest {
		writeError(w, 413, "文件夹目录清单过大")
		return
	}
	var manifest uploadTreeManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "文件夹目录清单无效")
		return
	}
	directories, roots, err := validateUploadTree(manifest.Entries)
	if err != nil {
		writeError(w, 400, "文件夹路径无效或数量超限（最多 10000 项、64 层目录）")
		return
	}
	allowed := make(map[string]bool)
	for _, root := range manifest.Replace {
		found := false
		for _, candidate := range roots {
			if candidate == root {
				found = true
				break
			}
		}
		if !found {
			writeError(w, 400, "替换文件名无效")
			return
		}
		allowed[root] = true
	}
	_ = part.Close()
	transaction, err := os.MkdirTemp(s.dataDir, uploadTransactionPrefix)
	if err != nil {
		writeError(w, 500, "无法创建上传临时目录")
		return
	}
	transactionID := strings.TrimPrefix(filepath.Base(transaction), uploadTransactionPrefix)
	receiveRecord, _ := json.Marshal(uploadReceiveRecord{ID: transactionID, Destination: destination, State: "receiving"})
	if err := writeDurableFile(filepath.Join(transaction, "receive.json"), receiveRecord, 0o600); err != nil {
		log.Printf("异常: 创建上传接收记录失败，事务=%s 目标=%q 错误=%v", transactionID, destination, err)
		_ = os.RemoveAll(transaction)
		writeError(w, http.StatusInternalServerError, "无法初始化上传事务")
		return
	}
	keepTransaction := false
	defer func() {
		if !keepTransaction {
			if err := os.RemoveAll(transaction); err != nil {
				log.Printf("异常: 清理上传临时目录失败，事务=%s 错误=%v", transactionID, err)
			}
		}
	}()
	stage := filepath.Join(transaction, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		writeError(w, 500, "无法创建上传临时目录")
		return
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0755); err != nil {
			writeError(w, 500, "无法创建上传目录")
			return
		}
	}
	seen := make(map[int]bool)
	saved := make([]fileInfo, 0)
	for {
		part, err = reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Printf("异常: 上传请求失败，事务=%s 目标=%q 错误=%v", transactionID, destination, err)
			treeUploadError(w, err)
			return
		}
		index, parseErr := strconv.Atoi(strings.TrimPrefix(part.FormName(), "file-"))
		if parseErr != nil || index < 0 || index >= len(manifest.Entries) || part.FormName() != fmt.Sprintf("file-%d", index) || part.FileName() == "" || seen[index] || manifest.Entries[index].Directory {
			writeError(w, 400, "上传文件与目录清单不匹配")
			return
		}
		entry := manifest.Entries[index]
		file, err := os.OpenFile(filepath.Join(stage, filepath.FromSlash(entry.Path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			writeError(w, 500, "无法保存上传文件")
			return
		}
		size, copyErr := io.Copy(file, part)
		fileSyncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil {
			log.Printf("异常: 写入上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, copyErr)
			treeUploadError(w, copyErr)
			return
		}
		if fileSyncErr != nil {
			log.Printf("异常: 同步上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, fileSyncErr)
			writeError(w, http.StatusInternalServerError, "无法保存上传文件")
			return
		}
		if closeErr != nil {
			log.Printf("异常: 关闭上传临时文件失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, closeErr)
			writeError(w, 500, "无法保存上传文件")
			return
		}
		if err := syncDirectory(filepath.Dir(filepath.Join(stage, filepath.FromSlash(entry.Path)))); err != nil {
			log.Printf("异常: 同步上传暂存目录失败，事务=%s 文件=%q 错误=%v", transactionID, entry.Path, err)
			writeError(w, http.StatusInternalServerError, "无法保存上传文件")
			return
		}
		if err := part.Close(); err != nil {
			treeUploadError(w, err)
			return
		}
		seen[index] = true
		saved = append(saved, fileInfo{Name: entry.Path, Size: size, ModTime: time.Now(), Type: contentType(entry.Path), PreviewType: previewContentType(entry.Path)})
	}
	for index, entry := range manifest.Entries {
		if !entry.Directory && !seen[index] {
			writeError(w, 400, "上传文件不完整，请重新上传")
			return
		}
	}
	names, err := s.commitUploadTree(stage, destination, roots, allowed)
	if err != nil {
		var recoveryErr *uploadRecoveryRequired
		if errors.As(err, &recoveryErr) {
			keepTransaction = true
		}
		var conflict *uploadConflict
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "目标位置已存在同名文件或文件夹", "conflicts": conflict.Names})
		} else {
			filesystemError(w, err)
		}
		return
	}
	relocate := func(name string) string {
		root, rest, hasRest := strings.Cut(name, "/")
		if hasRest {
			return path.Join(destination, names[root], rest)
		}
		return path.Join(destination, names[root])
	}
	for index := range saved {
		saved[index].Name = relocate(saved[index].Name)
	}
	for index := range directories {
		directories[index] = relocate(directories[index])
	}
	s.notifyFilesChanged()
	writeJSON(w, http.StatusCreated, map[string]any{"files": saved, "directories": directories})
}

type uploadConflict struct{ Names []string }

func (e *uploadConflict) Error() string { return "upload requires replacement confirmation" }

func (s *server) commitUploadTree(stage, destination string, roots []string, allowed map[string]bool) (map[string]string, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	target, err := s.sharedPath(destination)
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
			name, err := availableNameWithLstat(target, root, func(candidate string) (os.FileInfo, error) {
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
		return nil, &uploadConflict{Names: conflicts}
	}
	transaction := filepath.Dir(stage)
	backup := filepath.Join(transaction, "backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		return nil, err
	}
	record := uploadTransactionRecord{Destination: destination, Roots: append([]string(nil), finalRoots...), Replaced: append([]string(nil), existing...)}
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
			s.notifyFilesChanged()
			return nil, &uploadRecoveryRequired{cause: errors.Join(errs...)}
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
	if _, err := io.WriteString(markerTemp, uploadCommitMarker); err != nil {
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
