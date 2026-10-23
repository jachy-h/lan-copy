package textstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxTextBytes   = 64 << 10
	FileName       = ".lan-copy-texts.json"
	BackupFileName = ".lan-copy-texts.backup"
	MaxCount       = 1000
	MaxStoreBytes  = 16 << 20
)

var (
	ErrEmpty      = errors.New("empty text")
	ErrTextLimit  = errors.New("text size limit")
	ErrCountLimit = errors.New("text count limit")
	ErrStoreLimit = errors.New("text store size limit")
	ErrNotFound   = errors.New("text not found")
)

type Snippet struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) List() ([]Snippet, error) {
	s.mu.Lock()
	texts, err := s.readLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	sort.Slice(texts, func(i, j int) bool { return texts[i].Created.After(texts[j].Created) })
	return texts, nil
}

func (s *Store) Create(content string) (Snippet, error) {
	if strings.TrimSpace(content) == "" {
		return Snippet{}, ErrEmpty
	}
	if len([]byte(content)) > MaxTextBytes {
		return Snippet{}, ErrTextLimit
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return Snippet{}, err
	}
	item := Snippet{ID: hex.EncodeToString(idBytes), Content: content, Created: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	texts, err := s.readLocked()
	if err != nil {
		return Snippet{}, err
	}
	if len(texts) >= MaxCount {
		return Snippet{}, ErrCountLimit
	}
	texts = append(texts, item)
	encoded, err := json.Marshal(texts)
	if err != nil {
		return Snippet{}, err
	}
	if len(encoded) > MaxStoreBytes {
		return Snippet{}, ErrStoreLimit
	}
	if err := writeTextsWithOps(s.dir, texts, defaultTextStoreOps()); err != nil {
		return Snippet{}, err
	}
	return item, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	texts, err := s.readLocked()
	if err != nil {
		return err
	}
	found := false
	kept := texts[:0]
	for _, item := range texts {
		if item.ID == id {
			found = true
			continue
		}
		kept = append(kept, item)
	}
	if !found {
		return ErrNotFound
	}
	return writeTextsWithOps(s.dir, kept, defaultTextStoreOps())
}

func (s *Store) readLocked() ([]Snippet, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return []Snippet{}, nil
	}
	if err != nil {
		return nil, err
	}
	var texts []Snippet
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil, err
	}
	return texts, nil
}

type textStoreOps struct {
	write    func(*os.File, []byte) (int, error)
	syncFile func(*os.File) error
	close    func(*os.File) error
	rename   func(string, string) error
	remove   func(string) error
	syncDir  func(string) error
}

func defaultTextStoreOps() textStoreOps {
	return textStoreOps{write: (*os.File).Write, syncFile: (*os.File).Sync, close: (*os.File).Close, rename: os.Rename, remove: os.Remove, syncDir: syncDirectory}
}

func writeTextsWithOps(dir string, texts []Snippet, ops textStoreOps) error {
	data, err := json.Marshal(texts)
	if err != nil {
		return err
	}
	if len(texts) > MaxCount || len(data) > MaxStoreBytes {
		return fs.ErrInvalid
	}
	tmp, err := os.CreateTemp(dir, ".lan-copy-texts-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer ops.remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if _, err := ops.write(tmp, data); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if err := ops.syncFile(tmp); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if err := ops.close(tmp); err != nil {
		return err
	}
	destination := filepath.Join(dir, FileName)
	backup := filepath.Join(dir, BackupFileName)
	// Always preserve the old file until the new directory entry is synced.
	if err := ops.remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldExists := false
	if _, err := os.Lstat(destination); err == nil {
		if err := ops.rename(destination, backup); err != nil {
			return err
		}
		oldExists = true
		if err := ops.syncDir(dir); err != nil {
			return errors.Join(err, ops.rename(backup, destination), ops.syncDir(dir))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ops.rename(tmpName, destination); err != nil {
		if oldExists {
			return errors.Join(err, ops.rename(backup, destination), ops.syncDir(dir))
		}
		return err
	}
	if err := ops.syncDir(dir); err != nil {
		rollbackErr := ops.remove(destination)
		if oldExists {
			rollbackErr = errors.Join(rollbackErr, ops.rename(backup, destination))
		}
		rollbackErr = errors.Join(rollbackErr, ops.syncDir(dir))
		return errors.Join(err, rollbackErr)
	}
	if oldExists {
		if err := ops.remove(backup); err != nil {
			return err // New complete store is installed; backup is safe to clean on startup.
		}
		if err := ops.syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func Recover(dir string) error {
	destination := filepath.Join(dir, FileName)
	backup := filepath.Join(dir, BackupFileName)
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(dir); err != nil {
			return err
		}
		return cleanupInterruptedTextWrites(dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(backup); err == nil {
		if err := os.Rename(backup, destination); err != nil {
			return err
		}
		log.Printf("异常恢复: 已从备份恢复共享文本库")
		if err := syncDirectory(dir); err != nil {
			return err
		}
		return cleanupInterruptedTextWrites(dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return cleanupInterruptedTextWrites(dir)
}

func cleanupInterruptedTextWrites(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".lan-copy-texts-") {
			continue
		}
		log.Printf("异常: 发现未完成的文本库临时文件，名称=%s；将清理", entry.Name())
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("异常: 清理文本库临时文件失败，名称=%s 错误=%v", entry.Name(), err)
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(dir)
	}
	return nil
}
