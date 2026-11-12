package httpserver

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"lan-copy/internal/textstore"
)

func (s *Server) listTexts(w http.ResponseWriter, _ *http.Request) {
	texts, err := s.texts.List()
	if err != nil {
		log.Printf("异常: 读取共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "无法读取内容")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"texts": texts})
}

func (s *Server) createText(w http.ResponseWriter, r *http.Request) {
	// JSON may escape one input byte into several wire bytes (for example, newlines).
	r.Body = http.MaxBytesReader(w, r.Body, textstore.MaxTextBytes+64<<10)
	var input struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "文本超过 100,000 字限制")
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
		writeError(w, http.StatusBadRequest, "请输入要保存的文本")
		return
	}
	if utf8.RuneCountInString(input.Content) > textstore.MaxTextChars || len([]byte(input.Content)) > textstore.MaxTextBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文本超过 100,000 字限制")
		return
	}

	item, err := s.texts.Create(input.Content)
	if errors.Is(err, textstore.ErrCountLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "内容数量已达上限")
		return
	}
	if errors.Is(err, textstore.ErrStoreLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "内容存储已达上限")
		return
	}
	if err != nil {
		log.Printf("异常: 保存共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "保存内容失败")
		return
	}
	s.notifyTextsChanged()
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "文本编号无效")
		return
	}
	if err := s.texts.Delete(id); err != nil {
		if errors.Is(err, textstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "内容不存在")
			return
		}
		log.Printf("异常: 删除共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "删除内容失败")
		return
	}
	s.notifyTextsChanged()
	w.WriteHeader(http.StatusNoContent)
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
