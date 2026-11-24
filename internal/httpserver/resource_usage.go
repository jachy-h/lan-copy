package httpserver

import (
	"net/http"

	"lan-copy/internal/filestore"
)

type resourceUsageResponse struct {
	Storage struct {
		Bytes int64                 `json:"bytes"`
		Files int                   `json:"files"`
		Types []filestore.TypeUsage `json:"types"`
	} `json:"storage"`
	Uploads struct {
		Active int `json:"active"`
		Limit  int `json:"limit"`
	} `json:"uploads"`
	Staging struct {
		UsedBytes  int64 `json:"usedBytes"`
		LimitBytes int64 `json:"limitBytes"`
	} `json:"staging"`
	Downloads struct {
		Active int `json:"active"`
	} `json:"downloads"`
}

// resourceUsage reports aggregate occupancy without file names or transfer
// details. LAN visitors may read it because the settings page already exposes
// the read-only upload limits to them.
func (s *Server) resourceUsage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.files == nil {
		writeError(w, http.StatusServiceUnavailable, "资源统计不可用")
		return
	}
	storage, err := s.files.Usage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法统计共享存储")
		return
	}
	var result resourceUsageResponse
	result.Storage.Bytes = storage.Bytes
	result.Storage.Files = storage.Files
	result.Storage.Types = storage.Types
	if s.uploadResources != nil {
		config := s.uploadResources.Snapshot()
		usage := s.uploadResources.Usage()
		result.Uploads.Active = usage.ActiveUploads
		result.Uploads.Limit = config.Concurrent
		result.Staging.UsedBytes = usage.StagedBytes
		result.Staging.LimitBytes = config.StagingGiB * (1 << 30)
	}
	counts, _ := s.transfers.snapshot()
	result.Downloads.Active = counts.Downloads
	writeJSON(w, http.StatusOK, result)
}
