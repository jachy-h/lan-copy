package httpserver

import (
	"context"
	"net/http"
	"time"

	"lan-copy/internal/plugin"
	"lan-copy/internal/processstats"
)

const pluginUsageTimeout = 3 * time.Second

type systemUsageResponse struct {
	App     processstats.Sample `json:"app"`
	Plugins plugin.Usage        `json:"plugins"`
}

// systemUsage reports Lan Copy's own process and running plugin containers.
// Aggregates are read-only and contain no plugin configuration or file names.
func (s *Server) systemUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var app processstats.Sample
	if s.system != nil {
		app = s.system.Sample()
	}
	plugins := plugin.Usage{Items: []plugin.UsageItem{}, SampledAt: time.Now()}
	if s.plugins != nil {
		ctx, cancel := context.WithTimeout(r.Context(), pluginUsageTimeout)
		defer cancel()
		plugins = s.plugins.Usage(ctx)
	}
	writeJSON(w, http.StatusOK, systemUsageResponse{App: app, Plugins: plugins})
}
