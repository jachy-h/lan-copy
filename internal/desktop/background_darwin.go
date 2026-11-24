//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	backgroundChildArg     = "--internal-background"
	backgroundReadyEnv     = "LAN_COPY_READY_FD"
	backgroundLogEnv       = "LAN_COPY_LOG_FILE"
	backgroundLogMaxBytes  = 8 << 20
	backgroundStartTimeout = 15 * time.Second
	backgroundTailLines    = 20
	backgroundTailBytes    = 64 << 10
)

// IsBackgroundChild strips the internal flag and reports whether this process
// was started by StartBackground. It must run before flag parsing.
func IsBackgroundChild() bool {
	if len(os.Args) != 2 || os.Args[1] != backgroundChildArg {
		return false
	}
	os.Args = os.Args[:1]
	return true
}

// ShouldStartBackground reports whether this process should re-exec itself as
// a detached background server. Only the Homebrew command name "lancopy",
// launched without arguments from a terminal, qualifies; the release binary
// "lan-copy", go run and every explicit flag keep the foreground behavior.
func ShouldStartBackground() bool {
	return backgroundEligible(os.Args, isTerminal(os.Stdout), os.Getenv(backgroundReadyEnv))
}

func backgroundEligible(args []string, terminal bool, childEnv string) bool {
	if childEnv != "" || !terminal || len(args) != 1 {
		return false
	}
	return filepath.Base(args[0]) == "lancopy"
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}

// StartBackground launches a detached copy of this binary, waits for its
// startup summary and prints it. The child keeps running after this returns.
func StartBackground() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前程序: %w", err)
	}
	logPath, err := BackgroundLogPath()
	if err != nil {
		return err
	}
	if err := rotateLog(logPath, backgroundLogMaxBytes); err != nil {
		return fmt.Errorf("无法轮转日志文件: %w", err)
	}
	fmt.Println("正在后台启动 Lan Copy…")
	_, lines, err := startBackground(executable, logPath, backgroundStartTimeout)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	fmt.Printf("日志文件: %s\n", logPath)
	return nil
}

// BackgroundLogPath returns the log file used by the detached server.
func BackgroundLogPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(backgroundLogEnv)); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定日志目录: %w", err)
	}
	return filepath.Join(home, "Library", "Logs", "lancopy", "lancopy.log"), nil
}

// ReadyWriter returns the pipe the background parent uses to receive startup
// lines. It is nil for every other launch, so foreground output is unchanged.
func ReadyWriter() io.WriteCloser {
	return readyWriterFor(os.Getenv(backgroundReadyEnv))
}

func readyWriterFor(value string) io.WriteCloser {
	if value == "" {
		return nil
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil
	}
	return os.NewFile(uintptr(fd), "lan-copy-ready")
}

func startBackground(executable, logPath string, timeout time.Duration) (*exec.Cmd, []string, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("无法创建日志目录: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("无法打开日志文件: %w", err)
	}
	defer logFile.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("无法打开 %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("无法创建就绪管道: %w", err)
	}
	defer readyRead.Close()

	command := exec.Command(executable, backgroundChildArg)
	command.Stdin = devNull
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = append(os.Environ(), backgroundReadyEnv+"=3")
	command.ExtraFiles = []*os.File{readyWrite}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		readyWrite.Close()
		return nil, nil, fmt.Errorf("无法在后台启动 Lan Copy: %w", err)
	}
	readyWrite.Close()

	lines, timedOut := readReadyLines(readyRead, timeout)
	if timedOut {
		_ = command.Process.Kill()
		_ = command.Wait()
		return command, nil, fmt.Errorf("Lan Copy 未在 %s 内完成启动（日志: %s）\n%s", timeout, logPath, tailText(logPath, backgroundTailLines))
	}
	if len(lines) == 0 {
		// The child closed the readiness pipe without a summary: it failed or
		// exited before startup. Kill first so a stuck child cannot block Wait.
		_ = command.Process.Kill()
		_ = command.Wait()
		return command, nil, fmt.Errorf("Lan Copy 启动失败（日志: %s）\n%s", logPath, tailText(logPath, backgroundTailLines))
	}
	return command, lines, nil
}

func readReadyLines(reader io.Reader, timeout time.Duration) ([]string, bool) {
	results := make(chan []string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		results <- splitReadyLines(string(data))
	}()
	select {
	case lines := <-results:
		return lines, false
	case <-time.After(timeout):
		return nil, true
	}
}

func splitReadyLines(output string) []string {
	lines := make([]string, 0, 6)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func rotateLog(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	return os.Rename(path, path+".1")
}

func tailText(path string, count int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - backgroundTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
