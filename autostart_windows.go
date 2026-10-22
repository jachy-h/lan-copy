package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

const startupRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const startupValue = "LanCopy"

type windowsStartup struct {
	command    string
	configPath string
	spec       startupSpec
}

func newStartupBackend(spec startupSpec) (startupBackend, error) {
	path, err := startupConfigPath()
	if err != nil {
		return nil, err
	}
	command := syscall.EscapeArg(spec.Executable) + " --internal-autostart"
	return &windowsStartup{command: command, configPath: path, spec: spec}, nil
}

func readStartupRegistry(path string) ([]byte, uint32, error) {
	var key syscall.Handle
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, 0, err
	}
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, p, 0, syscall.KEY_QUERY_VALUE, &key)
	if err != nil {
		return nil, 0, err
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString(startupValue)
	var kind, size uint32
	if err = syscall.RegQueryValueEx(key, name, nil, &kind, nil, &size); err != nil {
		return nil, 0, err
	}
	if size > 65536 {
		return nil, 0, errors.New("启动项数据异常")
	}
	data := make([]byte, size+2)
	err = syscall.RegQueryValueEx(key, name, nil, &kind, &data[0], &size)
	if err != nil {
		return nil, kind, err
	}
	return data[:size], kind, nil
}

func (s *windowsStartup) Status() (bool, string, error) {
	data, kind, err := readStartupRegistry(startupRunKey)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	if kind != syscall.REG_SZ || syscall.UTF16ToString(units) != s.command {
		return false, "启动项与当前程序或参数不一致，请重新开启以更新", nil
	}
	saved, err := os.ReadFile(s.configPath)
	if err != nil {
		return false, "启动配置缺失或无法读取，请重新开启以修复", nil
	}
	expected, _ := json.Marshal(s.spec)
	if !bytes.Equal(saved, expected) {
		return false, "启动参数已变化，请重新开启以更新", nil
	}
	return true, "已注册登录启动；若被 Windows 禁用，请在系统设置的启动应用中允许", nil
}

func (s *windowsStartup) Set(enabled bool) error {
	if enabled {
		if len(utf16.Encode([]rune(s.command)))+1 > 260 {
			return errors.New("程序路径过长，无法注册 Windows 启动项，请缩短程序路径")
		}
		data, err := json.Marshal(s.spec)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(s.configPath), 0700); err != nil {
			return err
		}
		// Atomic replacement also protects a concurrent login from partial JSON.
		f, err := os.CreateTemp(filepath.Dir(s.configPath), ".startup-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(f.Name(), s.configPath); err != nil {
			return err
		}
	}
	if !enabled {
		_, _, err := readStartupRegistry(startupRunKey)
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	// Use the system tool by absolute path; never invoke a shell or interpolate
	// user paths into a script. reg.exe writes the current user's Run value.
	systemDir := os.Getenv("SystemRoot")
	if systemDir == "" {
		return errors.New("无法定位 Windows 系统目录")
	}
	args := []string{"delete", `HKCU\` + startupRunKey, "/v", startupValue, "/f"}
	if enabled {
		args = []string{"add", `HKCU\` + startupRunKey, "/v", startupValue, "/t", "REG_SZ", "/d", s.command, "/f"}
	}
	cmd := exec.Command(filepath.Join(systemDir, "System32", "reg.exe"), args...)
	detachUpdater(cmd)
	if data, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(data)))
	}
	return nil
}
