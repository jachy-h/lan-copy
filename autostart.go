package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type startupSpec struct {
	Executable string
	Directory  string
	Args       []string
}

type startupBackend interface {
	Status() (bool, string, error)
	Set(bool) error
}

type autostartService struct {
	mu       sync.Mutex
	backend  startupBackend
	disabled string
}

type autostartState struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func configureAutostart(listen, dir string, maxGB int64) *autostartService {
	a := &autostartService{}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		a.disabled = "无法定位当前程序"
		return a
	}
	if strings.Contains(filepath.ToSlash(exe), "/go-build") || os.Getenv("LAN_COPY_DISABLE_AUTOSTART") == "1" {
		a.disabled = "开发运行或容器环境不支持开机自启动"
		return a
	}
	cwd, err := os.Getwd()
	if err != nil {
		a.disabled = "无法读取工作目录"
		return a
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		a.disabled = "无法确定共享目录"
		return a
	}
	spec := startupSpec{exe, cwd, []string{"--autostart", "--working-dir", cwd, "-listen", listen, "-dir", dir, "-max-gb", fmt.Sprint(maxGB)}}
	a.backend, err = newStartupBackend(spec)
	if err != nil {
		a.disabled = err.Error()
	}
	return a
}

func (a *autostartService) state() autostartState {
	if a == nil {
		return autostartState{Reason: "当前启动方式不支持开机自启动"}
	}
	if a.disabled != "" {
		return autostartState{Reason: a.disabled}
	}
	enabled, reason, err := a.backend.Status()
	if err != nil {
		return autostartState{Reason: "无法读取系统启动项：" + err.Error()}
	}
	return autostartState{Enabled: enabled, Available: true, Reason: reason}
}

func validAutostartRequest(r *http.Request) bool {
	if !isLocalRequest(r) || r.Header.Get("X-Lan-Copy-Action") != "autostart" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
	}
	return true
}

func (s *server) autostartSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut && !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, "请在运行 Lan Copy 的电脑上修改开机自启动")
		return
	}
	if r.Method == http.MethodPut && !validAutostartRequest(r) {
		writeError(w, http.StatusForbidden, "无效的设置请求，请从本机 Lan Copy 页面发起")
		return
	}
	a := s.autostart
	if a == nil {
		if r.Method == http.MethodPut {
			writeError(w, http.StatusServiceUnavailable, a.state().Reason)
			return
		}
		writeJSON(w, http.StatusOK, a.state())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == http.MethodPut {
		if a.disabled != "" {
			writeError(w, http.StatusServiceUnavailable, a.disabled)
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil {
			writeError(w, http.StatusBadRequest, "无效的自启动设置")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "无效的自启动设置")
			return
		}
		if err := a.backend.Set(*input.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "无法修改系统启动项："+err.Error())
			return
		}
	}
	state := a.state()
	if !isLocalRequest(r) {
		state.Available = false
		if state.Reason == "" {
			if state.Enabled {
				state.Reason = "服务主机已开启开机自启动。"
			} else {
				state.Reason = "服务主机未开启开机自启动。"
			}
		}
		state.Reason += "仅运行 Lan Copy 的电脑可以修改。"
	}
	writeJSON(w, http.StatusOK, state)
}

// A short Windows Run command avoids its 260-character limit. The full launch
// parameters live in the current user's configuration directory.
func startupConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "LanCopy", "startup.json"), nil
}

func startupLaunchArgs() ([]string, error) {
	path, err := startupConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec startupSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	if len(spec.Args) == 0 || spec.Args[0] != "--autostart" {
		return nil, errors.New("无效的自启动配置，请手动启动并重新开启自启动")
	}
	return spec.Args, nil
}
