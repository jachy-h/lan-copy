package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type StartupSpec struct {
	Executable string
	Directory  string
	Args       []string
}

type Backend interface {
	Status() (bool, string, error)
	Set(bool) error
}

type Autostart struct {
	mu       sync.Mutex
	backend  Backend
	disabled string
}

type AutostartState struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func ConfigureAutostart(listen, dir string, maxGB int64) *Autostart {
	a := &Autostart{}
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
	spec := StartupSpec{exe, cwd, []string{"--autostart", "--working-dir", cwd, "-listen", listen, "-dir", dir, "-max-gb", fmt.Sprint(maxGB)}}
	a.backend, err = newStartupBackend(spec)
	if err != nil {
		a.disabled = err.Error()
	}
	return a
}

func (a *Autostart) stateLocked() AutostartState {
	if a == nil {
		return AutostartState{Reason: "当前启动方式不支持开机自启动"}
	}
	if a.disabled != "" {
		return AutostartState{Reason: a.disabled}
	}
	enabled, reason, err := a.backend.Status()
	if err != nil {
		return AutostartState{Reason: "无法读取系统启动项：" + err.Error()}
	}
	return AutostartState{Enabled: enabled, Available: true, Reason: reason}
}

func startupConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "LanCopy", "startup.json"), nil
}

func StartupLaunchArgs() ([]string, error) {
	path, err := startupConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec StartupSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	if len(spec.Args) == 0 || spec.Args[0] != "--autostart" {
		return nil, errors.New("无效的自启动配置，请手动启动并重新开启自启动")
	}
	return spec.Args, nil
}

var ErrAutostartUnavailable = errors.New("autostart unavailable")

func (a *Autostart) DisabledReason() string {
	if a == nil {
		return "当前启动方式不支持开机自启动"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.disabled
}

func NewAutostartWithBackend(backend Backend, disabled string) *Autostart {
	return &Autostart{backend: backend, disabled: disabled}
}

func (a *Autostart) State() AutostartState {
	if a == nil {
		return AutostartState{Reason: "当前启动方式不支持开机自启动"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked()
}

func (a *Autostart) Apply(enabled bool) (AutostartState, error) {
	if a == nil {
		return a.State(), ErrAutostartUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disabled != "" {
		return a.stateLocked(), ErrAutostartUnavailable
	}
	if err := a.backend.Set(enabled); err != nil {
		return AutostartState{}, err
	}
	return a.stateLocked(), nil
}
