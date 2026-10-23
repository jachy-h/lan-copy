//go:build !windows && !darwin

package desktop

import "errors"

func newStartupBackend(StartupSpec) (Backend, error) {
	return nil, errors.New("当前仅支持 Windows 和 macOS 的开机自启动")
}
