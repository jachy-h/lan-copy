package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"lan-copy/internal/textstore"
)

func (s *Server) fragmentSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut && !validLocalActionRequest(r, "fragments") {
		writeError(w, http.StatusForbidden, "请在运行 Lan Copy 的电脑上从本机设置页面修改碎片到期时间")
		return
	}
	if s.texts == nil {
		writeError(w, http.StatusServiceUnavailable, "碎片设置不可用")
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			ExpiryDays int `json:"expiryDays"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "碎片设置格式无效")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "碎片设置格式无效")
			return
		}
		settings := textstore.Settings{ExpiryDays: input.ExpiryDays}
		if !settings.Valid() {
			writeError(w, http.StatusBadRequest, "到期时间请设置为 1–365 天")
			return
		}
		if err := s.texts.Configure(settings); err != nil {
			if errors.Is(err, textstore.ErrInvalidSettings) {
				writeError(w, http.StatusBadRequest, "到期时间请设置为 1–365 天")
				return
			}
			writeError(w, http.StatusInternalServerError, "无法保存碎片设置")
			return
		}
	}
	settings := s.texts.Settings()
	response := map[string]any{
		"expiryDays":        settings.ExpiryDays,
		"defaultExpiryDays": textstore.DefaultExpiryDays,
		"minExpiryDays":     textstore.MinExpiryDays,
		"maxExpiryDays":     textstore.MaxExpiryDays,
	}
	if !isLocalRequest(r) {
		response["editable"] = false
		response["reason"] = "仅运行 Lan Copy 的电脑可以修改。"
	} else {
		response["editable"] = true
	}
	writeJSON(w, http.StatusOK, response)
}
