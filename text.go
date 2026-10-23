package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxTextBytes      = 64 << 10 // 64 KiB
	textStore         = ".lan-copy-texts.json"
	textStoreBackup   = ".lan-copy-texts.backup"
	maxTextCount      = 1000
	maxTextStoreBytes = 16 << 20
)

type textSnippet struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

func (s *server) listTexts(w http.ResponseWriter, _ *http.Request) {
	s.textMu.Lock()
	texts, err := s.readTextsLocked()
	s.textMu.Unlock()
	if err != nil {
		log.Printf("异常: 读取共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "无法读取共享文本")
		return
	}
	sort.Slice(texts, func(i, j int) bool { return texts[i].Created.After(texts[j].Created) })
	writeJSON(w, http.StatusOK, map[string]any{"texts": texts})
}

func (s *server) createText(w http.ResponseWriter, r *http.Request) {
	// JSON may escape one input byte into several wire bytes (for example, newlines).
	r.Body = http.MaxBytesReader(w, r.Body, maxTextBytes*6+1024)
	var input struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "文本超过 64 KiB 限制")
		} else {
			writeError(w, http.StatusBadRequest, "文本数据格式无效")
		}
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "文本数据格式无效")
		return
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, "请输入要共享的文本")
		return
	}
	if len([]byte(input.Content)) > maxTextBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文本超过 64 KiB 限制")
		return
	}

	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "创建共享文本失败")
		return
	}
	item := textSnippet{ID: hex.EncodeToString(idBytes), Content: input.Content, Created: time.Now()}

	s.textMu.Lock()
	texts, err := s.readTextsLocked()
	if err == nil {
		if len(texts) >= maxTextCount {
			s.textMu.Unlock()
			writeError(w, http.StatusRequestEntityTooLarge, "共享文本数量已达上限")
			return
		}
		texts = append(texts, item)
		if encoded, marshalErr := json.Marshal(texts); marshalErr == nil && len(encoded) > maxTextStoreBytes {
			s.textMu.Unlock()
			writeError(w, http.StatusRequestEntityTooLarge, "共享文本存储已达上限")
			return
		}
		err = s.writeTextsLocked(texts)
	}
	s.textMu.Unlock()
	if err != nil {
		log.Printf("异常: 保存共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "保存共享文本失败")
		return
	}
	s.notifyTextsChanged()
	writeJSON(w, http.StatusCreated, item)
}

func (s *server) deleteText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "文本编号无效")
		return
	}

	s.textMu.Lock()
	texts, err := s.readTextsLocked()
	if err == nil {
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
			s.textMu.Unlock()
			writeError(w, http.StatusNotFound, "共享文本不存在")
			return
		}
		err = s.writeTextsLocked(kept)
	}
	s.textMu.Unlock()
	if err != nil {
		log.Printf("异常: 删除共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "删除共享文本失败")
		return
	}
	s.notifyTextsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) readTextsLocked() ([]textSnippet, error) {
	data, err := os.ReadFile(filepath.Join(s.dataDir, textStore))
	if errors.Is(err, os.ErrNotExist) {
		return []textSnippet{}, nil
	}
	if err != nil {
		return nil, err
	}
	var texts []textSnippet
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil, err
	}
	return texts, nil
}

func (s *server) writeTextsLocked(texts []textSnippet) error {
	return writeTextsWithOps(s.dataDir, texts, defaultTextStoreOps())
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

func writeTextsWithOps(dir string, texts []textSnippet, ops textStoreOps) error {
	data, err := json.Marshal(texts)
	if err != nil {
		return err
	}
	if len(texts) > maxTextCount || len(data) > maxTextStoreBytes {
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
	destination := filepath.Join(dir, textStore)
	backup := filepath.Join(dir, textStoreBackup)
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

func recoverTextStore(dir string) error {
	destination := filepath.Join(dir, textStore)
	backup := filepath.Join(dir, textStoreBackup)
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

func validTextID(id string) bool {
	if len(id) != 16 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}
