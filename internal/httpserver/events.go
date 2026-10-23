package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (s *Server) changeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持实时更新")
		return
	}
	updates, unsubscribe := s.subscribeChanges()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := io.WriteString(w, ": connected\nretry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.appContext.Done():
			return
		case update := <-updates:
			data, err := json.Marshal(update)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: change\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type changeEvent struct {
	Version uint64 `json:"version"`
	Files   bool   `json:"files"`
	Texts   bool   `json:"texts"`
}

func (s *Server) subscribeChanges() (<-chan changeEvent, func()) {
	updates := make(chan changeEvent, 1)
	s.eventsMu.Lock()
	s.changeSubscribers[updates] = struct{}{}
	s.eventsMu.Unlock()
	return updates, func() {
		s.eventsMu.Lock()
		delete(s.changeSubscribers, updates)
		s.eventsMu.Unlock()
	}
}

func (s *Server) notifyFilesChanged() {
	s.notifyChange(true, false)
}

func (s *Server) notifyTextsChanged() {
	s.notifyChange(false, true)
}

func (s *Server) notifyChange(files, texts bool) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	s.changeVersion++
	update := changeEvent{Version: s.changeVersion, Files: files, Texts: texts}
	for subscriber := range s.changeSubscribers {
		select {
		case subscriber <- update:
		default:
			merged := update
			var pending changeEvent
			select {
			case pending = <-subscriber:
			default:
			}
			merged.Files = merged.Files || pending.Files
			merged.Texts = merged.Texts || pending.Texts
			if pending.Version > merged.Version {
				merged.Version = pending.Version
			}
			select {
			case subscriber <- merged:
			default:
			}
		}
	}
}
