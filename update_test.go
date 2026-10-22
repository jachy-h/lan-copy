package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildUpdateTestBinary(t *testing.T, dest, ver, sourceDir string, keys ...string) []byte {
	t.Helper()
	flags := "-X main.version=" + ver
	if len(keys) > 0 {
		flags += " -X main.updatePublicKey=" + keys[0]
	}
	cmd := exec.Command("go", "build", "-ldflags", flags, "-o", dest, ".")
	if sourceDir != "" {
		cmd.Dir = sourceDir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func packageUpdateTest(t *testing.T, binary []byte, ver, goos, arch string, signing ed25519.PrivateKey) map[string][]byte {
	t.Helper()
	name := "lan-copy"
	if goos == "windows" {
		name += ".exe"
	}
	hash := sha256.Sum256(binary)
	m := updateManifest{App: "lan-copy", Version: ver, OS: goos, Arch: arch, Binary: name, SHA256: hex.EncodeToString(hash[:])}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{"release/update.json": raw, "release/" + name: binary}
	if signing != nil {
		entries["release/update.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(signing, raw)))
	}
	return entries
}
func zipUpdateTest(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestUpdatePackages(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "build")
	binary := buildUpdateTestBinary(t, exe, "test-new", "")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	originalKey := updatePublicKey
	updatePublicKey = key
	t.Cleanup(func() { updatePublicKey = originalKey })
	for _, tc := range []struct {
		name   string
		change func(map[string][]byte)
		key    string
		want   string
		noKey  bool
	}{
		{name: "valid signed", key: key},
		{name: "unconfigured trust key", noKey: true, want: "公钥"},
		{name: "wrong signer", key: base64.StdEncoding.EncodeToString(make([]byte, 32)), want: "签名"},
		{name: "missing signature", key: key, change: func(e map[string][]byte) { delete(e, "release/update.sig") }, want: "签名"},
		{name: "tampered binary", change: func(e map[string][]byte) {
			e["release/lan-copy"] = []byte("bad")
			e["release/lan-copy.exe"] = []byte("bad")
		}, want: "校验"},
		{name: "non executable", change: func(e map[string][]byte) {
			var m updateManifest
			_ = json.Unmarshal(e["release/update.json"], &m)
			data := []byte("not an executable")
			sum := sha256.Sum256(data)
			m.SHA256 = hex.EncodeToString(sum[:])
			e["release/"+m.Binary] = data
			e["release/update.json"], _ = json.Marshal(m)
		}, want: "可执行程序"},
		{name: "signed manifest tampering", key: key, change: func(e map[string][]byte) { e["release/update.json"] = append(e["release/update.json"], ' ') }, want: "签名"},
		{name: "missing manifest", change: func(e map[string][]byte) { delete(e, "release/update.json") }, want: "签名"},
		{name: "wrong platform", change: func(e map[string][]byte) {
			var m updateManifest
			_ = json.Unmarshal(e["release/update.json"], &m)
			m.Arch = "wrong"
			e["release/update.json"], _ = json.Marshal(m)
		}, want: "不匹配"},
		{name: "traversal", change: func(e map[string][]byte) { e["../update.json"] = []byte("bad") }, want: "非法路径"},
		{name: "duplicate", change: func(e map[string][]byte) { e["other/update.json"] = e["release/update.json"] }, want: "重复"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.key == "" && !tc.noKey {
				tc.key = key
			}
			entries := packageUpdateTest(t, binary, "test-new", runtime.GOOS, runtime.GOARCH, priv)
			if tc.change != nil {
				tc.change(entries)
			}
			if tc.name == "non executable" || tc.name == "wrong platform" {
				entries["release/update.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, entries["release/update.json"])))
			}
			file := filepath.Join(t.TempDir(), "update.zip")
			if err := os.WriteFile(file, zipUpdateTest(t, entries), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := unpackUpdate(file, filepath.Join(t.TempDir(), "candidate"), runtime.GOOS, runtime.GOARCH, tc.key)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	t.Run("helper launch failure keeps service alive", func(t *testing.T) {
		app, err := newServer(t.TempDir(), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "lan-copy")
		_ = os.WriteFile(target, []byte("previous"), 0700)
		var stage string
		app.updater = &updateService{executable: target, launch: func(dir string, p updatePlan) error { stage = dir; return fmt.Errorf("launch failed") }}
		req := httptest.NewRequest("POST", "http://localhost:8080/api/update", bytes.NewReader(zipUpdateTest(t, packageUpdateTest(t, binary, "test-new", runtime.GOOS, runtime.GOARCH, priv))))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Lan-Copy-Action", "update")
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, req)
		if w.Code != 500 || app.updater.busy {
			t.Fatalf("unexpected failure status: %d busy %v", w.Code, app.updater.busy)
		}
		if stage == "" {
			t.Fatal("helper was not called")
		}
		if _, err := os.Stat(stage); !os.IsNotExist(err) {
			t.Fatalf("staging was not removed: %v", err)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "previous" {
			t.Fatal("old executable modified")
		}
		select {
		case <-app.shutdownRequested:
			t.Fatal("failed launch stopped service")
		default:
		}
	})
	t.Run("tar gzip", func(t *testing.T) {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		for name, data := range packageUpdateTest(t, binary, "test-new", runtime.GOOS, runtime.GOARCH, priv) {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		_ = tw.Close()
		_ = gz.Close()
		file := filepath.Join(t.TempDir(), "update.tar.gz")
		_ = os.WriteFile(file, b.Bytes(), 0600)
		if _, err := unpackUpdate(file, filepath.Join(t.TempDir(), "candidate"), runtime.GOOS, runtime.GOARCH, key); err != nil {
			t.Fatal(err)
		}
	})
}
func TestUpdateAccess(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	originalKey := updatePublicKey
	updatePublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { updatePublicKey = originalKey })
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	app.updater = &updateService{}
	for _, tc := range []struct {
		name, remote, host, origin, site, action string
		want                                     int
	}{
		{"LAN device", "192.168.1.9:2000", "192.168.1.2:8080", "http://192.168.1.2:8080", "same-origin", "update", 400},
		{"no action header", "127.0.0.1:2000", "localhost:8080", "", "", "", 403},
		{"cross origin", "127.0.0.1:2000", "localhost:8080", "http://evil.example", "cross-site", "update", 403},
		{"local hostname", "192.168.1.9:2000", "lan-copy.local:8080", "http://lan-copy.local:8080", "same-origin", "update", 400},
		{"IPv6 LAN", "[fd00::9]:2000", "[fd00::2]:8080", "http://[fd00::2]:8080", "same-origin", "update", 400},
		{"valid local malformed package", "127.0.0.1:2000", "localhost:8080", "http://localhost:8080", "same-origin", "update", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app.updater.executable = filepath.Join(t.TempDir(), "lan-copy")
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/update", strings.NewReader("bad package"))
			r.RemoteAddr = tc.remote
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("X-Lan-Copy-Action", tc.action)
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			select {
			case <-app.shutdownRequested:
				t.Fatal("invalid update stopped service")
			default:
			}
		})
	}
	app.updater.busy = true
	r := httptest.NewRequest("POST", "http://localhost:8080/api/update", nil)
	r.RemoteAddr = "127.0.0.1:3000"
	r.Header.Set("X-Lan-Copy-Action", "update")
	w := httptest.NewRecorder()
	app.routes().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("busy status: %d", w.Code)
	}
}
func readUpdateInfo(client *http.Client, address string) map[string]any {
	resp, err := client.Get(address + "/api/app")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var info map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&info)
	return info
}
func TestUpdateProcessLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and restarts real processes")
	}
	dir := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	original := filepath.Join(dir, "original"+suffix)
	oldBinary := buildUpdateTestBinary(t, original, "integration-old", "", key)
	newer := buildUpdateTestBinary(t, filepath.Join(dir, "new"+suffix), "integration-new", "", key)
	brokenDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(brokenDir, "go.mod"), []byte("module lan-copy\n\ngo 1.22\n"), 0600)
	_ = os.WriteFile(filepath.Join(brokenDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0600)
	broken := buildUpdateTestBinary(t, filepath.Join(dir, "broken"+suffix), "broken", brokenDir)
	for _, tc := range []struct {
		name   string
		binary []byte
		want   string
	}{{"success", newer, "integration-new"}, {"rollback", broken, "integration-old"}} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			exe := filepath.Join(work, "lan-copy"+suffix)
			if err := os.WriteFile(exe, oldBinary, 0700); err != nil {
				t.Fatal(err)
			}
			storage := filepath.Join(work, "data")
			_ = os.Mkdir(storage, 0700)
			_ = os.WriteFile(filepath.Join(storage, "keep.txt"), []byte("keep me"), 0600)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			client := &http.Client{Timeout: 2 * time.Second}
			base := "http://" + address
			cmd := exec.Command(exe, "-listen", address, "-dir", storage)
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "LAN_COPY_DISABLE_UPDATE=0", "LAN_COPY_UPDATE_ID=")
			detachUpdater(cmd)
			output, err := os.OpenFile(filepath.Join(work, "server.log"), os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			cmd.Stdout = output
			cmd.Stderr = output
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			stop := func() {
				r, _ := http.NewRequest("POST", base+"/api/shutdown", nil)
				r.Header.Set("X-Lan-Copy-Action", "shutdown")
				if resp, err := client.Do(r); err == nil {
					_ = resp.Body.Close()
				}
			}
			defer stop()
			defer func() { _ = cmd.Process.Kill() }()
			deadline := time.Now().Add(10 * time.Second)
			for readUpdateInfo(client, base) == nil && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if info := readUpdateInfo(client, base); info == nil || info["updateEnabled"] != true {
				t.Fatalf("not ready: %v", info)
			}
			textResp, err := client.Post(base+"/api/texts", "application/json", strings.NewReader(`{"content":"survives update"}`))
			if err != nil {
				t.Fatal(err)
			}
			_ = textResp.Body.Close()
			if textResp.StatusCode != 201 {
				t.Fatalf("text create: %d", textResp.StatusCode)
			}
			req, _ := http.NewRequest("POST", base+"/api/update", bytes.NewReader(zipUpdateTest(t, packageUpdateTest(t, tc.binary, "integration-new", runtime.GOOS, runtime.GOARCH, priv))))
			req.Header.Set("X-Lan-Copy-Action", "update")
			client.Timeout = 15 * time.Second
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			client.Timeout = 2 * time.Second
			if resp.StatusCode != 202 {
				t.Fatalf("update: %d %s", resp.StatusCode, body)
			}
			var result map[string]string
			_ = json.Unmarshal(body, &result)
			select {
			case err = <-exited:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("old process did not exit")
			}
			expectedID := result["updateID"]
			if tc.name == "rollback" {
				expectedID = "rollback-" + expectedID
			}
			deadline = time.Now().Add(45 * time.Second)
			var info map[string]any
			for time.Now().Before(deadline) {
				info = readUpdateInfo(client, base)
				if info != nil && info["updateID"] == expectedID {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if info == nil || info["updateID"] != expectedID || info["version"] != tc.want {
				log, _ := os.ReadFile(filepath.Join(work, "lan-copy-update.log"))
				t.Fatalf("unexpected restarted app: %v, log %s", info, log)
			}
			saved, err := os.ReadFile(filepath.Join(storage, "keep.txt"))
			if err != nil || string(saved) != "keep me" {
				t.Fatalf("shared data changed: %s %v", saved, err)
			}
			textResp, err = client.Get(base + "/api/texts")
			if err != nil {
				t.Fatal(err)
			}
			textData, _ := io.ReadAll(textResp.Body)
			_ = textResp.Body.Close()
			if !bytes.Contains(textData, []byte("survives update")) {
				t.Fatalf("text lost after update: %s", textData)
			}
			stop()
			deadline = time.Now().Add(8 * time.Second)
			for readUpdateInfo(client, base) != nil && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if readUpdateInfo(client, base) != nil {
				t.Fatal("restarted service did not stop")
			}
			// Let the helper close its executable/log handles before TempDir cleanup.
			time.Sleep(250 * time.Millisecond)
		})
	}
}

func TestLANUpdateRequiresSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	originalKey := updatePublicKey
	t.Cleanup(func() { updatePublicKey = originalKey })
	binary := buildUpdateTestBinary(t, filepath.Join(t.TempDir(), "build"), "lan-signed", "", key)
	for _, tc := range []struct {
		name, host, remote, key string
		change                  func(map[string][]byte)
		want                    int
	}{
		{name: "signed IPv4 LAN update", host: "192.168.1.2:8080", remote: "192.168.1.9:2000", key: key, want: 202},
		{name: "signed IPv6 LAN update", host: "[fd00::2]:8080", remote: "[fd00::9]:2000", key: key, want: 202},
		{name: "signed local hostname update", host: "lan-copy.local:8080", remote: "192.168.1.9:2000", key: key, want: 202},
		{name: "unsigned package", key: key, change: func(e map[string][]byte) { delete(e, "release/update.sig") }, want: 400},
		{name: "wrong signing key", key: key, change: func(e map[string][]byte) {
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			e["release/update.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, e["release/update.json"])))
		}, want: 400},
		{name: "missing configured key", want: 503},
		{name: "invalid configured key", key: "invalid", want: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updatePublicKey = tc.key
			app, err := newServer(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "lan-copy")
			if err = os.WriteFile(target, []byte("old executable"), 0700); err != nil {
				t.Fatal(err)
			}
			var launched bool
			app.updater = &updateService{executable: target, launch: func(stage string, p updatePlan) error {
				launched = true
				t.Cleanup(func() { _ = os.RemoveAll(stage) })
				return nil
			}}
			entries := packageUpdateTest(t, binary, "lan-signed", runtime.GOOS, runtime.GOARCH, priv)
			if tc.change != nil {
				tc.change(entries)
			}
			if tc.host == "" {
				tc.host = "192.168.1.2:8080"
			}
			if tc.remote == "" {
				tc.remote = "192.168.1.9:2000"
			}
			infoRequest := httptest.NewRequest("GET", "http://"+tc.host+"/api/app", nil)
			infoRequest.RemoteAddr = tc.remote
			infoResponse := httptest.NewRecorder()
			app.routes().ServeHTTP(infoResponse, infoRequest)
			var info map[string]any
			_ = json.Unmarshal(infoResponse.Body.Bytes(), &info)
			if info["updateEnabled"] != (tc.want != 503) {
				t.Fatalf("unexpected availability: %v", info)
			}
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/update", bytes.NewReader(zipUpdateTest(t, entries)))
			r.RemoteAddr = tc.remote
			r.Header.Set("Origin", "http://"+tc.host)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("X-Lan-Copy-Action", "update")
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d %s", tc.want, w.Code, w.Body.String())
			}
			if launched != (tc.want == 202) {
				t.Fatalf("helper launched: %v", launched)
			}
			select {
			case <-app.shutdownRequested:
				if tc.want != 202 {
					t.Fatal("rejected update stopped service")
				}
			default:
				if tc.want == 202 {
					t.Fatal("accepted update did not request restart")
				}
			}
		})
	}
}
