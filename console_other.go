//go:build !windows

package main

func setupConsole() {}

func notifyAlreadyRunning(listen, dir string) bool { return false }

func waitForAnyKeyToCloseConsole() {}
