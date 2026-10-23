package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const MaxSize = int64(128 << 20)

type Manifest struct {
	App     string `json:"app"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Binary  string `json:"binary"`
	SHA256  string `json:"sha256"`
}
type Service struct {
	mu         sync.Mutex
	busy       bool
	executable string
	args       []string
	cwd        string
	healthURL  string
	disabled   string
	launch     func(string, Plan) error
}
type Plan struct {
	Target    string   `json:"target"`
	Candidate string   `json:"candidate"`
	ParentPID int      `json:"parentPID"`
	Args      []string `json:"args"`
	Cwd       string   `json:"cwd"`
	HealthURL string   `json:"healthURL"`
	ID        string   `json:"id"`
}

func TrustedUpdateKey(encoded string) (ed25519.PublicKey, error) {
	if encoded == "" {
		return nil, errors.New("尚未配置可信更新公钥，自动更新已禁用")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("更新公钥配置无效，自动更新已禁用")
	}
	return ed25519.PublicKey(key), nil
}

func Unpack(archivePath, candidate, goos, arch, publicKey string) (Manifest, error) {
	var m Manifest
	key, err := TrustedUpdateKey(publicKey)
	if err != nil {
		return m, err
	}
	entries := map[string][]byte{}
	total := int64(0)
	add := func(name string, size int64, regular bool, reader io.Reader) error {
		clean := path.Clean(name)
		if strings.Contains(name, "\\") || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("更新包包含非法路径")
		}
		if !regular {
			return errors.New("更新包不允许链接或特殊文件")
		}
		total += size
		if size < 0 || total > MaxSize {
			return errors.New("更新包解压后超过 128 MiB")
		}
		base := path.Base(clean)
		if base != "update.json" && base != "update.sig" && base != "lan-copy" && base != "lan-copy.exe" {
			return nil
		}
		if _, exists := entries[base]; exists {
			return errors.New("更新包包含重复文件")
		}
		limit := MaxSize
		if base == "update.json" || base == "update.sig" {
			limit = 16 << 10
		}
		if size > limit {
			return errors.New("更新清单过大")
		}
		data, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil || int64(len(data)) != size {
			return errors.New("更新包文件不完整")
		}
		entries[base] = data
		return nil
	}
	if z, err := zip.OpenReader(archivePath); err == nil {
		defer z.Close()
		if len(z.File) > 256 {
			return m, errors.New("更新包文件过多")
		}
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if f.UncompressedSize64 > uint64(MaxSize) {
				return m, errors.New("更新包文件过大")
			}
			reader, err := f.Open()
			if err != nil {
				return m, err
			}
			err = add(f.Name, int64(f.UncompressedSize64), f.Mode().IsRegular(), reader)
			_ = reader.Close()
			if err != nil {
				return m, err
			}
		}
	} else {
		f, err := os.Open(archivePath)
		if err != nil {
			return m, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return m, errors.New("请选择有效的 .zip 或 .tar.gz 更新包")
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, MaxSize+(1<<20)))
		for count := 0; ; count++ {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return m, errors.New("更新包已损坏")
			}
			if count >= 256 {
				return m, errors.New("更新包文件过多")
			}
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if err := add(h.Name, h.Size, h.Typeflag == tar.TypeReg, tr); err != nil {
				return m, err
			}
		}
	}
	raw := entries["update.json"]
	sig, sigErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(entries["update.sig"])))
	if sigErr != nil || !ed25519.Verify(key, raw, sig) {
		return m, errors.New("更新包签名无效，请使用可信发布包")
	}

	if err := json.Unmarshal(raw, &m); err != nil || m.App != "lan-copy" || strings.TrimSpace(m.Version) == "" || len(m.Version) > 100 {
		return m, errors.New("缺少有效 update.json，请使用支持自动更新的新发布包")
	}
	expected := "lan-copy"
	if goos == "windows" {
		expected += ".exe"
	}
	if m.OS != goos || m.Arch != arch || m.Binary != expected {
		return m, fmt.Errorf("更新包与当前系统不匹配，需要 %s/%s", goos, arch)
	}
	binary, ok := entries[expected]
	sum := sha256.Sum256(binary)
	if !ok || hex.EncodeToString(sum[:]) != m.SHA256 {
		return m, errors.New("程序校验失败，更新包可能已损坏")
	}
	if err := os.WriteFile(candidate, binary, 0700); err != nil {
		return m, err
	}
	info, err := buildinfo.ReadFile(candidate)
	if err != nil || info.Path != "lan-copy" {
		return m, errors.New("更新包不是 Lan Copy 可执行程序")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != goos || settings["GOARCH"] != arch {
		return m, errors.New("可执行程序的平台与清单不一致")
	}
	return m, nil
}
func hashFile(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Configure(listen, dir string) *Service {
	u := &Service{launch: launchUpdateHelper}
	exe, err := os.Executable()
	if err != nil {
		u.disabled = "无法定位当前程序"
		return u
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		u.disabled = "无法定位当前程序"
		return u
	}
	u.executable = exe
	if strings.Contains(filepath.ToSlash(exe), "/go-build") || os.Getenv("LAN_COPY_DISABLE_UPDATE") == "1" {
		u.disabled = "开发运行或容器环境不支持自动更新"
	}
	u.cwd, err = os.Getwd()
	if err != nil {
		u.disabled = "无法读取工作目录"
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		u.disabled = "无法读取共享目录"
	}
	u.args = append(append([]string{}, os.Args[1:]...), "-listen", listen, "-dir", dir)
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		u.disabled = "无法确定重启地址"
		return u
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	u.healthURL = "http://" + net.JoinHostPort(host, port) + "/api/app"
	return u
}
func launchUpdateHelper(stage string, p Plan) error {
	// Run a copy of the current trusted binary, never an uploaded helper.
	helper := filepath.Join(stage, "updater")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	source, err := os.ReadFile(p.Target)
	if err != nil {
		return err
	}
	if err = os.WriteFile(helper, source, 0700); err != nil {
		return err
	}
	config := filepath.Join(stage, "plan.json")
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = os.WriteFile(config, data, 0600); err != nil {
		return err
	}
	cmd := exec.Command(helper, "--internal-updater", config)
	cmd.Dir = p.Cwd
	DetachUpdater(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(stage, "ready")); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return errors.New("辅助进程未就绪")
}
func updateChild(p Plan, id string) *exec.Cmd {
	cmd := exec.Command(p.Target, p.Args...)
	cmd.Dir = p.Cwd
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "LAN_COPY_UPDATE_ID=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "LAN_COPY_UPDATE_ID="+id)
	DetachUpdater(cmd)
	return cmd
}
func healthyUpdate(address, id string, timeout time.Duration) bool {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(address)
		if err == nil {
			var result struct {
				ID string `json:"updateID"`
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result)
			_ = resp.Body.Close()
			if err == nil && resp.StatusCode == 200 && result.ID == id {
				return true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}
func RunHelper(config string) error {
	raw, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	var p Plan
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	stage := filepath.Dir(config)
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(p.Target), "lan-copy-update.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	record := func(message string) { _, _ = fmt.Fprintln(logFile, time.Now().Format(time.RFC3339), message) }
	if err = os.WriteFile(filepath.Join(stage, "ready"), []byte("ready"), 0600); err != nil {
		return err
	}
	if err = waitUpdateParent(p.ParentPID, 45*time.Second); err != nil {
		record(err.Error())
		return err
	}
	backup := filepath.Join(stage, "previous")
	if err = os.Rename(p.Target, backup); err != nil {
		record("备份失败：" + err.Error())
		return err
	}
	restore := func() error {
		_ = os.Remove(p.Target)
		if err := os.Rename(backup, p.Target); err != nil {
			record("恢复失败，旧程序位于 " + backup + "：" + err.Error())
			return err
		}
		old := updateChild(p, "rollback-"+p.ID)
		if err := old.Start(); err != nil {
			record("旧版本重新启动失败：" + err.Error())
			return err
		}
		go func() { _ = old.Wait() }()
		record("已恢复旧版本")
		return nil
	}
	if err = os.Rename(p.Candidate, p.Target); err != nil {
		record("替换失败：" + err.Error())
		_ = restore()
		return err
	}
	child := updateChild(p, p.ID)
	if err = child.Start(); err != nil {
		record("启动失败：" + err.Error())
		_ = restore()
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	if !healthyUpdate(p.HealthURL, p.ID, 25*time.Second) {
		_ = child.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			record("新进程无法停止，保留备份 " + backup)
			return errors.New("无法停止新进程")
		}
		record("新版本启动检查失败")
		_ = restore()
		return errors.New("新版本启动失败，已尝试恢复")
	}
	record("更新成功")
	_ = os.Remove(backup)
	// On Windows the running helper cannot delete itself. Its directory is kept
	// for diagnostics; no untrusted files are ever executed from uploaded paths.
	_ = os.Remove(p.Candidate)
	_ = os.Remove(filepath.Join(stage, "package"))
	_ = os.Remove(config)
	_ = os.Remove(filepath.Join(stage, "ready"))
	if runtime.GOOS != "windows" {
		_ = os.RemoveAll(stage)
	}
	return nil
}

type Config struct {
	Executable string
	Args       []string
	Cwd        string
	HealthURL  string
	Disabled   string
	Launch     func(string, Plan) error
	Busy       bool
}

func New(config Config) *Service {
	return &Service{executable: config.Executable, args: config.Args, cwd: config.Cwd, healthURL: config.HealthURL, disabled: config.Disabled, launch: config.Launch, busy: config.Busy}
}

func (u *Service) DisabledReason(publicKey string) string {
	if u == nil {
		return "当前启动方式不支持自动更新"
	}
	if u.disabled != "" {
		return u.disabled
	}
	if _, err := TrustedUpdateKey(publicKey); err != nil {
		return err.Error()
	}
	return ""
}

func (u *Service) Busy() bool {
	if u == nil {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.busy
}

type InstallError struct {
	Kind string
	Err  error
}

func (e *InstallError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Kind
}

// Install serializes updates and keeps the stage after the helper starts.
// The caller supplies the archive source so HTTP request parsing stays outside.
func (u *Service) Install(source func() (io.ReadCloser, string, error), goos, arch, publicKey string) (Manifest, string, error) {
	var zero Manifest
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return zero, "", &InstallError{Kind: "busy"}
	}
	u.busy = true
	u.mu.Unlock()
	committed := false
	defer func() {
		if !committed {
			u.mu.Lock()
			u.busy = false
			u.mu.Unlock()
		}
	}()
	stage, err := os.MkdirTemp(filepath.Dir(u.executable), ".lan-copy-update-")
	if err != nil {
		return zero, "", &InstallError{Kind: "stage", Err: err}
	}
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	archive, err := os.OpenFile(filepath.Join(stage, "package"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return zero, "", &InstallError{Kind: "archive", Err: err}
	}
	body, expectedVersion, err := source()
	if err != nil {
		_ = archive.Close()
		return zero, "", &InstallError{Kind: "source", Err: err}
	}
	defer body.Close()
	size, copyErr := io.Copy(archive, io.LimitReader(body, MaxSize+1))
	closeErr := archive.Close()
	if size > MaxSize || copyErr != nil || closeErr != nil {
		return zero, "", &InstallError{Kind: "read", Err: errors.Join(copyErr, closeErr)}
	}
	candidate := filepath.Join(stage, "candidate")
	manifest, err := Unpack(filepath.Join(stage, "package"), candidate, goos, arch, publicKey)
	if err != nil {
		return zero, "", &InstallError{Kind: "invalid", Err: err}
	}
	if expectedVersion != "" && manifest.Version != expectedVersion {
		return zero, "", &InstallError{Kind: "version"}
	}
	currentHash, err := hashFile(u.executable)
	if err != nil {
		return zero, "", &InstallError{Kind: "current", Err: err}
	}
	if currentHash == manifest.SHA256 {
		return zero, "", &InstallError{Kind: "same"}
	}
	token := make([]byte, 24)
	if _, err = rand.Read(token); err != nil {
		return zero, "", &InstallError{Kind: "token", Err: err}
	}
	plan := Plan{Target: u.executable, Candidate: candidate, ParentPID: os.Getpid(), Args: u.args, Cwd: u.cwd, HealthURL: u.healthURL, ID: hex.EncodeToString(token)}
	if err := u.launch(stage, plan); err != nil {
		return zero, "", &InstallError{Kind: "launch", Err: err}
	}
	committed = true
	return manifest, plan.ID, nil
}
