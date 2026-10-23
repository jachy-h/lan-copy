//go:build windows

package desktop

import "syscall"

func SystemLanguage() string {
	language, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage").Call()
	if language&0x3ff == 0x04 {
		return "zh"
	}
	if language != 0 {
		return "en"
	}
	return EnvironmentLanguage()
}
