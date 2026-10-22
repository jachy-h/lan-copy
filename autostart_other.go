//go:build !windows && !darwin

package main

import "errors"

func newStartupBackend(startupSpec) (startupBackend, error) {
	return nil, errors.New("当前仅支持 Windows 和 macOS 的开机自启动")
}
