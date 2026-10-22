package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
)

const startupLabel = "com.lan-copy.autostart"

type macStartup struct {
	path string
	spec startupSpec
}

func newStartupBackend(spec startupSpec) (startupBackend, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &macStartup{filepath.Join(home, "Library", "LaunchAgents", startupLabel+".plist"), spec}, nil
}

func xmlText(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func (m *macStartup) plist() []byte {
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + startupLabel + `</string><key>ProgramArguments</key><array>`)
	for _, arg := range append([]string{m.spec.Executable}, m.spec.Args...) {
		out.WriteString("<string>" + xmlText(arg) + "</string>")
	}
	out.WriteString(`</array><key>WorkingDirectory</key><string>` + xmlText(m.spec.Directory) + `</string><key>RunAtLoad</key><true/><key>KeepAlive</key><false/></dict></plist>`)
	return out.Bytes()
}

func (m *macStartup) Status() (bool, string, error) {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if !bytes.Equal(data, m.plist()) {
		return false, "启动项与当前程序或参数不一致，请重新开启以更新", nil
	}
	return true, "已注册登录启动；若被 macOS 禁用，请在系统设置的登录项中允许", nil
}

func (m *macStartup) Set(enabled bool) error {
	if !enabled {
		err := os.Remove(m.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	// Replace atomically. Do not bootstrap/bootout the running service: changes
	// apply at the next login and must not interrupt transfers or updates.
	f, err := os.CreateTemp(filepath.Dir(m.path), ".lan-copy-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(m.plist()); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), m.path)
}
