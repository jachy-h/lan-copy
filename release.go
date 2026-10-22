package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const releaseAPI = "https://api.github.com/repos/jachy-h/lan-copy/releases/latest"
const releasePage = "https://github.com/jachy-h/lan-copy/releases"

type githubReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	Tag        string               `json:"tag_name"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}
type releaseService struct {
	mu      sync.Mutex
	client  *http.Client
	cached  githubRelease
	checked time.Time
}

func newReleaseService() *releaseService {
	return &releaseService{client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		host := r.URL.Hostname()
		if len(via) >= 10 || r.URL.Scheme != "https" || r.URL.User != nil || (host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
			return errors.New("更新下载重定向无效")
		}
		return nil
	}}}
}

var stableVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func compareVersions(a, b string) (int, error) {
	aa, bb := stableVersion.FindStringSubmatch(a), stableVersion.FindStringSubmatch(b)
	if aa == nil || bb == nil {
		return 0, errors.New("无法比较非正式版本号")
	}
	result := 0
	for i := 1; i <= 3; i++ {
		x, e := strconv.ParseUint(aa[i], 10, 64)
		if e != nil {
			return 0, e
		}
		y, e := strconv.ParseUint(bb[i], 10, 64)
		if e != nil {
			return 0, e
		}
		if result == 0 {
			if x > y {
				result = 1
			} else if x < y {
				result = -1
			}
		}
	}
	return result, nil
}
func (s *releaseService) latest(ctx context.Context) (githubRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.checked.IsZero() && time.Since(s.checked) < 5*time.Minute {
		return s.cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lan-Copy")
	resp, err := s.client.Do(req)
	if err != nil {
		return githubRelease{}, errors.New("无法连接 GitHub，请稍后重试或使用离线更新包")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return githubRelease{}, errors.New("尚无可用的正式 Release")
	}
	if resp.StatusCode != 200 {
		return githubRelease{}, fmt.Errorf("GitHub 检查失败（HTTP %d），请稍后重试", resp.StatusCode)
	}
	var release githubRelease
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return release, errors.New("GitHub 返回了无效版本信息")
	}
	if _, err = compareVersions(release.Tag, release.Tag); err != nil || release.Draft || release.Prerelease {
		return release, errors.New("未找到有效的正式发布版本")
	}
	s.cached = release
	s.checked = time.Now()
	return release, nil
}
func (r githubRelease) asset(goos, arch string) (string, error) {
	name := ""
	if goos == "windows" {
		name = "局域网快传-Windows-" + arch + ".zip"
	}
	if goos == "darwin" && arch == "arm64" {
		name = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
	}
	for _, a := range r.Assets {
		if name == "" || a.Name != name {
			continue
		}
		u, err := url.Parse(a.URL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || !strings.HasPrefix(u.Path, "/jachy-h/lan-copy/releases/download/"+r.Tag+"/") || a.Size <= 0 || a.Size > maxUpdateSize {
			return "", errors.New("发布包地址或大小无效")
		}
		return a.URL, nil
	}
	return "", errors.New("此版本没有适用于当前系统的更新包")
}
func (s *server) checkRelease(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	release, err := s.releases.latest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cmp, cmpErr := compareVersions(release.Tag, version)
	_, assetErr := release.asset(runtime.GOOS, runtime.GOARCH)
	reason := s.updateUnavailableReason()
	if assetErr != nil {
		reason = assetErr.Error()
	}
	if cmpErr != nil {
		reason = "开发版本，无法自动比较版本号；可前往查看正式版本"
	}
	writeJSON(w, 200, map[string]any{"version": strings.TrimPrefix(release.Tag, "v"), "comparable": cmpErr == nil, "available": cmpErr == nil && cmp > 0, "canInstall": cmpErr == nil && cmp > 0 && reason == "", "reason": reason, "pageURL": releasePage + "/tag/" + url.PathEscape(release.Tag)})
}
func (s *server) downloadUpdate(w http.ResponseWriter, r *http.Request) {
	s.installUpdatePackage(w, r, true)
}
func (s *server) releasePackage(ctx context.Context) (io.ReadCloser, string, error) {
	release, err := s.releases.latest(ctx)
	if err != nil {
		return nil, "", err
	}
	cmp, err := compareVersions(release.Tag, version)
	if err != nil || cmp <= 0 {
		return nil, "", errors.New("没有可安装的新版本")
	}
	address, err := release.asset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.releases.client.Do(req)
	if err != nil {
		return nil, "", errors.New("更新包下载失败，请重试或使用离线更新包")
	}
	if resp.StatusCode != 200 || resp.ContentLength > maxUpdateSize {
		resp.Body.Close()
		return nil, "", errors.New("更新包下载失败或超过 128 MiB")
	}
	return resp.Body, strings.TrimPrefix(release.Tag, "v"), nil
}
