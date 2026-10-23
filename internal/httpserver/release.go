package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"lan-copy/internal/updater"
)

func (s *Server) checkRelease(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	release, err := s.releases.Latest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cmp, cmpErr := updater.CompareVersions(release.Tag, s.version)
	_, assetErr := release.Asset(runtime.GOOS, runtime.GOARCH)
	reason := s.updateUnavailableReason()
	if assetErr != nil {
		reason = assetErr.Error()
	}
	if cmpErr != nil {
		reason = "开发版本，无法自动比较版本号；可前往查看正式版本"
	}
	writeJSON(w, 200, map[string]any{"version": strings.TrimPrefix(release.Tag, "v"), "comparable": cmpErr == nil, "available": cmpErr == nil && cmp > 0, "canInstall": cmpErr == nil && cmp > 0 && reason == "", "reason": reason, "pageURL": updater.ReleasePage + "/tag/" + url.PathEscape(release.Tag)})
}
func (s *Server) downloadUpdate(w http.ResponseWriter, r *http.Request) {
	s.installUpdatePackage(w, r, true)
}
func (s *Server) releasePackage(ctx context.Context) (io.ReadCloser, string, error) {
	release, err := s.releases.Latest(ctx)
	if err != nil {
		return nil, "", err
	}
	cmp, err := updater.CompareVersions(release.Tag, s.version)
	if err != nil || cmp <= 0 {
		return nil, "", errors.New("没有可安装的新版本")
	}
	address, err := release.Asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, "", err
	}
	body, err := s.releases.FetchPackage(ctx, address)
	if err != nil {
		return nil, "", err
	}
	return body, strings.TrimPrefix(release.Tag, "v"), nil
}
