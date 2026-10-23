package filestore

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var ErrStagingBudget = errors.New("upload staging budget exceeded")

type StageError struct {
	Kind string
	Err  error
}

func (e *StageError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Kind
}
func (e *StageError) Unwrap() error { return e.Err }

type Transaction struct {
	store       *Store
	resources   *Resources
	destination string
	roots       []string
	path        string
	stage       string
	id          string
	stagedBytes int64
	keep        bool
	files       map[string]bool
	written     map[string]bool
}

func (t *Transaction) ID() string { return t.id }

func (s *Store) BeginUpload(destination string, entries []TreeEntry, resources *Resources) (*Transaction, error) {
	if err := s.CheckDestination(destination); err != nil {
		return nil, err
	}
	dirs, roots, err := ValidateUploadTree(entries)
	if err != nil {
		return nil, err
	}
	transaction, err := os.MkdirTemp(s.dataDir, TransactionPrefix)
	if err != nil {
		return nil, &StageError{Kind: "temporary", Err: err}
	}
	id := strings.TrimPrefix(filepath.Base(transaction), TransactionPrefix)
	clean := true
	defer func() {
		if clean {
			_ = os.RemoveAll(transaction)
		}
	}()
	record, _ := json.Marshal(ReceiveRecord{ID: id, Destination: destination, State: "receiving"})
	if err := WriteDurableFile(filepath.Join(transaction, "receive.json"), record, 0600); err != nil {
		log.Printf("异常: 创建上传接收记录失败，事务=%s 目标=%q 错误=%v", id, destination, err)
		return nil, &StageError{Kind: "record", Err: err}
	}
	stage := filepath.Join(transaction, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, &StageError{Kind: "temporary", Err: err}
	}
	for _, directory := range dirs {
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0755); err != nil {
			return nil, &StageError{Kind: "directory", Err: err}
		}
	}
	files := make(map[string]bool)
	for _, entry := range entries {
		if !entry.Directory {
			files[entry.Path] = true
		}
	}
	clean = false
	return &Transaction{store: s, resources: resources, destination: destination, roots: roots, path: transaction, stage: stage, id: id, files: files, written: make(map[string]bool)}, nil
}

func (t *Transaction) StageFile(name string, source io.Reader) (int64, error) {
	if !t.files[name] || t.written[name] {
		return 0, fs.ErrInvalid
	}
	file, err := os.OpenFile(filepath.Join(t.stage, filepath.FromSlash(name)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, &StageError{Kind: "open", Err: err}
	}
	writer := &budgetWriter{writer: file, resources: t.resources, staged: &t.stagedBytes}
	size, copyErr := io.Copy(writer, source)
	fileSyncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return size, &StageError{Kind: "write", Err: copyErr}
	}
	if fileSyncErr != nil {
		return size, &StageError{Kind: "sync", Err: fileSyncErr}
	}
	if closeErr != nil {
		return size, &StageError{Kind: "close", Err: closeErr}
	}
	if err := syncDirectory(filepath.Dir(filepath.Join(t.stage, filepath.FromSlash(name)))); err != nil {
		return size, &StageError{Kind: "directory-sync", Err: err}
	}
	t.written[name] = true
	return size, nil
}

func (t *Transaction) Commit(allowed map[string]bool, onPartial func()) (map[string]string, error) {
	for name := range t.files {
		if !t.written[name] {
			return nil, fs.ErrInvalid
		}
	}
	for name := range allowed {
		found := false
		for _, root := range t.roots {
			if name == root {
				found = true
				break
			}
		}
		if !found {
			return nil, fs.ErrInvalid
		}
	}
	names, err := t.store.Commit(t.stage, t.destination, t.roots, allowed, onPartial)
	var recovery *RecoveryRequired
	if errors.As(err, &recovery) {
		t.keep = true
	}
	return names, err
}

func (t *Transaction) Close() {
	if t.keep {
		return
	}
	if t.resources != nil && t.stagedBytes > 0 {
		t.resources.Release(t.stagedBytes)
	}
	if err := os.RemoveAll(t.path); err != nil {
		log.Printf("异常: 清理上传临时目录失败，事务=%s 错误=%v", t.id, err)
	}
}

type budgetWriter struct {
	writer    io.Writer
	resources *Resources
	staged    *int64
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	limit := len(p)
	if w.resources != nil {
		reserved := w.resources.ReserveUpTo(int64(limit))
		if reserved == 0 {
			return 0, ErrStagingBudget
		}
		limit = int(reserved)
	}
	n, err := w.writer.Write(p[:limit])
	if w.resources != nil {
		if n < limit {
			w.resources.Release(int64(limit - n))
		}
		*w.staged += int64(n)
	}
	if err == nil && limit < len(p) {
		err = ErrStagingBudget
	}
	return n, err
}
