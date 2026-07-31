package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxTextBytes = 64 << 10 // 64 KiB
	textStore    = ".lan-copy-texts.json"
)

type textSnippet struct {
	ID      string    `json:"id"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

func (s *server) listTexts(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	texts, err := s.readTextsLocked()
	s.mu.Unlock()
	if err != nil {
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

	s.mu.Lock()
	texts, err := s.readTextsLocked()
	if err == nil {
		texts = append(texts, item)
		err = s.writeTextsLocked(texts)
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存共享文本失败")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *server) deleteText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "文本编号无效")
		return
	}

	s.mu.Lock()
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
			s.mu.Unlock()
			writeError(w, http.StatusNotFound, "共享文本不存在")
			return
		}
		err = s.writeTextsLocked(kept)
	}
	s.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除共享文本失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) readTextsLocked() ([]textSnippet, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, textStore))
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
	data, err := json.Marshal(texts)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".lan-copy-texts-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	destination := filepath.Join(s.dir, textStore)
	if err := os.Rename(tmpName, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, destination)
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
