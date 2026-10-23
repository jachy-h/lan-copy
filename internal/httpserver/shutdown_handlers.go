package httpserver

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 请求")
		return
	}
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "仅运行 Lan Copy 的电脑可以关闭软件")
		return
	}
	if r.Header.Get("X-Lan-Copy-Action") != "shutdown" {
		writeError(w, http.StatusForbidden, "无效的关闭请求")
		return
	}
	var input struct {
		Confirmation string `json:"confirmation"`
		Uploads      *int   `json:"uploads"`
		Downloads    *int   `json:"downloads"`
	}
	if r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil {
			writeError(w, http.StatusBadRequest, "关停确认数据无效")
			return
		}
	}
	if input.Confirmation != "" && input.Confirmation != shutdownConfirmation {
		writeError(w, http.StatusBadRequest, "确认信息不匹配")
		return
	}
	if (input.Uploads == nil) != (input.Downloads == nil) || (input.Uploads != nil && (*input.Uploads < 0 || *input.Downloads < 0)) {
		writeError(w, http.StatusBadRequest, "关停传输计数无效")
		return
	}
	var expected *transferCounts
	if input.Uploads != nil {
		expected = &transferCounts{Uploads: *input.Uploads, Downloads: *input.Downloads}
	}
	counts, started := s.transfers.beginShutdown(input.Confirmation == shutdownConfirmation, expected)
	if !started {
		message := "仍有文件传输正在进行。确认强制关闭可能中断这些传输。"
		if expected != nil && *expected != counts {
			message = "文件传输数量已变化，请核对最新数量后再次确认。"
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":                message,
			"uploads":              counts.Uploads,
			"downloads":            counts.Downloads,
			"requiresConfirmation": true,
		})
		return
	}
	s.requestShutdown("web")
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "shutting_down", "uploads": counts.Uploads, "downloads": counts.Downloads})
}

const shutdownConfirmation = "强制关闭"

func (s *Server) shutdownStatus(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "仅 localhost 可以查看关停状态")
		return
	}
	counts, stopping := s.transfers.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"uploads":   counts.Uploads,
		"downloads": counts.Downloads,
		"stopping":  stopping,
	})
}
