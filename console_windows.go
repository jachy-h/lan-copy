//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
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
)

const consoleCodePageUTF8 = 65001

func setupConsole() {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		procSetConsoleOutputCP.Call(consoleCodePageUTF8)
	}
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

// notifyAlreadyRunning 在端口被占用时探测是否已有 Lan Copy 实例在后台运行；
// 如果是，则显示与首次启动相同的提示界面，按任意键后直接退出。
func notifyAlreadyRunning(listen, dir string) bool {
	if !ownsExclusiveConsole() {
		return false
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		return false
	}

	fmt.Printf("Lan Copy 已在后台运行，文件保存在 %s\n", dir)
	fmt.Printf("本机访问: http://localhost:%s\n", port)
	for _, address := range localURLs(fmt.Sprintf(":%s", port)) {
		fmt.Printf("局域网访问: %s\n", address)
	}
	fmt.Printf("\n请在浏览器打开 http://localhost:%s 查看功能。\n", port)
	fmt.Println()
	fmt.Println("本控制台窗口已无其他用途，可随时关闭。如需停止服务，请在网页右上角点击“关闭软件”。")
	fmt.Println("按任意键关闭本窗口……")

	waitForAnyKey()
	return true
}
