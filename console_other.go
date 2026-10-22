//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func setupConsole() {}

func hideStartupConsole() {}

func openBrowserOnLaunch() bool { return false }

func openBrowser(target string) error { return openDesktopPath(target) }

func openDesktopPath(target string) error {
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

func notifyAlreadyRunning(listen, dir string) bool { return false }

func waitForAnyKeyToCloseConsole() {}
