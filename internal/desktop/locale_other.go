//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
	"strings"
)

func SystemLanguage() string {
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			languages := strings.FieldsFunc(string(output), func(r rune) bool {
				return r == '(' || r == ')' || r == '"' || r == ',' || r == '\n' || r == ' ' || r == '\t'
			})
			if len(languages) > 0 {
				return languages[0]
			}
		}
	}
	return EnvironmentLanguage()
}
