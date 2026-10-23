//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

func SetupConsole() {}

func HideStartupConsole() {}

func OpenBrowserOnLaunch() bool { return false }

func OpenBrowser(target string) error { return OpenPath(target) }

func OpenPath(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "linux":
		command = exec.Command("xdg-open", target)
	default:
		return nil
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func NotifyAlreadyRunning(func() (bool, error)) bool { return false }

func WaitForAnyKeyToCloseConsole() {}
