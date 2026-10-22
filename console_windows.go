//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"syscall"
	"unsafe"
)

const (
	stdInputHandle   = ^uintptr(9) // STD_INPUT_HANDLE = (DWORD)-10
	consoleBufferLen = 1
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procGetStdHandle       = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode     = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode     = kernel32.NewProc("SetConsoleMode")
	procReadConsole        = kernel32.NewProc("ReadConsoleW")
	procFreeConsole        = kernel32.NewProc("FreeConsole")
	procGetProcList        = kernel32.NewProc("GetConsoleProcessList")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
	shell32                = syscall.NewLazyDLL("shell32.dll")
	procShellExecute       = shell32.NewProc("ShellExecuteW")
)

const consoleCodePageUTF8 = 65001

func setupConsole() {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		procSetConsoleOutputCP.Call(consoleCodePageUTF8)
	}
}

func openBrowser(target string) error { return openDesktopPath(target) }

func openDesktopPath(target string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	url, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := procShellExecute.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(url)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("ShellExecuteW failed (code %d): %v", result, callErr)
	}
	return nil
}

func ownsExclusiveConsole() bool {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd == 0 {
		return false
	}
	var pids [2]uint32
	n, _, _ := procGetProcList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

func consoleInteractive() bool {
	hIn, _, _ := procGetStdHandle.Call(stdInputHandle)
	if hIn == 0 || hIn == ^uintptr(0) {
		return false
	}
	var mode uint32
	ret, _, _ := procGetConsoleMode.Call(hIn, uintptr(unsafe.Pointer(&mode)))
	return ret != 0
}

func openBrowserOnLaunch() bool {
	return ownsExclusiveConsole() && consoleInteractive()
}

func waitForAnyKey() {
	hIn, _, _ := procGetStdHandle.Call(stdInputHandle)
	var oldMode uint32
	procGetConsoleMode.Call(hIn, uintptr(unsafe.Pointer(&oldMode)))
	procSetConsoleMode.Call(hIn, 0)
	var buf [consoleBufferLen]uint16
	var read uint32
	procReadConsole.Call(
		hIn,
		uintptr(unsafe.Pointer(&buf[0])),
		consoleBufferLen,
		uintptr(unsafe.Pointer(&read)),
		0,
	)
	procSetConsoleMode.Call(hIn, uintptr(oldMode))
}

// waitForAnyKeyToCloseConsole 提示用户按任意键关闭控制台窗口，
// 窗口关闭后服务继续在后台运行（停止服务请使用网页右上角的“关闭软件”）。
func waitForAnyKeyToCloseConsole() {
	if !ownsExclusiveConsole() || !consoleInteractive() {
		return
	}
	fmt.Println()
	fmt.Println("本控制台窗口已无其他用途，可随时关闭；服务将继续在后台运行。如需停止服务，请在网页右上角点击“关闭软件”。")
	fmt.Println("按任意键关闭本窗口……")

	waitForAnyKey()
	procFreeConsole.Call()
	log.SetOutput(io.Discard)
}

// A repeated launch opens the existing service regardless of console ownership.
// Only a console created for this invocation may be detached.
func notifyAlreadyRunning(listen, _ string) bool {
	found, err := openRunningInstance(listen, openBrowser)
	if !found {
		return false
	}
	if err != nil {
		log.Printf("Lan Copy 已在运行，但无法自动打开浏览器：%v", err)
	} else {
		fmt.Println("Lan Copy 已在运行，已在浏览器打开已有页面。")
	}
	if err == nil && ownsExclusiveConsole() {
		procFreeConsole.Call()
		log.SetOutput(io.Discard)
	}
	return true
}

// Detach only a console created for this invocation.
func hideStartupConsole() {
	if ownsExclusiveConsole() {
		procFreeConsole.Call()
	}
	log.SetOutput(io.Discard)
}
