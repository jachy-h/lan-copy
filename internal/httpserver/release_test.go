package httpserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lan-copy/internal/updater"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeReleaseService(t *testing.T, tag string, archive []byte) (*updater.ReleaseService, *int) {
	t.Helper()
	name := "局域网快传-Windows-" + runtime.GOARCH + ".zip"
	if runtime.GOOS == "darwin" {
		name = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
	}
	r := updater.Release{Tag: tag}
	r.Assets = append(r.Assets, updater.ReleaseAsset{Name: name, URL: "https://github.com/jachy-h/lan-copy/releases/download/" + tag + "/package", Size: int64(len(archive))})
	raw, _ := json.Marshal(r)
	calls := 0
	s := updater.NewReleaseService()
	s.SetTransport(releaseTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body := archive
		if req.URL.String() == updater.ReleaseAPI {
			body = raw
		} else if req.URL.Host != "github.com" {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), ContentLength: int64(len(body))}, nil
	}))
	return s, &calls
}
func TestReleaseCheckAndCache(t *testing.T) {
	old := version
	version = "1.9.9"
	defer func() { version = old }()
	s, _ := newServer(t.TempDir(), defaultMaxUpload)
	var calls *int
	s.releases, calls = fakeReleaseService(t, "v1.10.0", []byte("archive"))
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, httptest.NewRequest("GET", "/api/update/check", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) || !strings.Contains(w.Body.String(), `"canInstall":false`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if *calls != 1 {
		t.Fatalf("cache: %d calls", *calls)
	}
	version = "2.0.0"
	s.version = version
	w := httptest.NewRecorder()
	s.checkRelease(w, httptest.NewRequest("GET", "/api/update/check", nil))
	if !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatal(w.Body.String())
	}
}
func TestOnlineUpdateUsesSignedInstaller(t *testing.T) {
	if runtime.GOOS != "windows" && !(runtime.GOOS == "darwin" && runtime.GOARCH == "arm64") {
		t.Skip("online release packages target Windows and macOS arm64")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	oldKey, oldVersion := updatePublicKey, version
	updatePublicKey = base64.StdEncoding.EncodeToString(pub)
	version = "1.0.0"
	defer func() { updatePublicKey = oldKey; version = oldVersion }()
	binary := buildUpdateTestBinary(t, filepath.Join(t.TempDir(), "candidate"), "1.1.0", "")
	for _, tc := range []struct {
		name, manifest string
		signed         bool
		want           int
	}{
		{"valid", "1.1.0", true, 202}, {"unsigned", "1.1.0", false, 400}, {"version mismatch", "1.2.0", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newServer(t.TempDir(), defaultMaxUpload)
			target := filepath.Join(t.TempDir(), "lan-copy")
			os.WriteFile(target, []byte("old program"), 0700)
			launched := false
			s.updater = updater.New(updater.Config{Executable: target, Launch: func(_ string, p updater.Plan) error {
				launched = true
				data, e := os.ReadFile(p.Candidate)
				if e != nil || !bytes.Equal(data, binary) {
					t.Fatal("invalid candidate")
				}
				return nil
			}})
			signing := priv
			if !tc.signed {
				signing = nil
			}
			archive := zipUpdateTest(t, packageUpdateTest(t, binary, tc.manifest, runtime.GOOS, runtime.GOARCH, signing))
			s.releases, _ = fakeReleaseService(t, "v1.1.0", archive)
			req := httptest.NewRequest("POST", "http://192.168.1.2:8080/api/update/download", nil)
			req.RemoteAddr = "192.168.1.9:2000"
			req.Header.Set("Origin", "http://192.168.1.2:8080")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("X-Lan-Copy-Action", "update")
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, req)
			if w.Code != tc.want || launched != (tc.want == 202) {
				t.Fatal(w.Code, w.Body.String(), launched)
			}
			if tc.want != 202 && s.updater.Busy() {
				t.Fatal("installer left busy")
			}
		})
	}
}
func TestOnlineUpdateRejectsCrossSite(t *testing.T) {
	s, _ := newServer(t.TempDir(), defaultMaxUpload)
	req := httptest.NewRequest("POST", "/api/update/download", nil)
	req.Header.Set("X-Lan-Copy-Action", "update")
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestOnlineUpdateAllowsLANGuest(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := updatePublicKey
	updatePublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { updatePublicKey = oldKey })
	s, err := newServer(t.TempDir(), defaultMaxUpload)
	if err != nil {
		t.Fatal(err)
	}
	s.updater = updater.New(updater.Config{Busy: true})
	req := httptest.NewRequest("POST", "http://192.168.1.2:8080/api/update/download", nil)
	req.RemoteAddr = "192.168.1.9:2000"
	req.Header.Set("Origin", "http://192.168.1.2:8080")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Lan-Copy-Action", "update")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("LAN guest update: got %d %s", w.Code, w.Body.String())
	}
}

// Run against the actual CI archives before uploading them as Release assets.
func TestPublishedReleasePackages(t *testing.T) {
	dir := os.Getenv("LAN_COPY_RELEASE_DIR")
	if dir == "" {
		t.Skip("set LAN_COPY_RELEASE_DIR to verify packaged Release assets")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(testModuleRoot(), dir)
	}
	expected := os.Getenv("LAN_COPY_RELEASE_VERSION")
	if _, err := updater.CompareVersions(expected, expected); err != nil {
		t.Fatal("missing or invalid LAN_COPY_RELEASE_VERSION")
	}
	for _, tc := range []struct{ name, goos, arch string }{
		{"局域网快传-Windows-amd64.zip", "windows", "amd64"},
		{"Lan-Copy-macOS-Apple-Silicon.tar.gz", "darwin", "arm64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := updater.Unpack(filepath.Join(dir, tc.name), filepath.Join(t.TempDir(), "candidate"), tc.goos, tc.arch, os.Getenv("UPDATE_PUBLIC_KEY"))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Version != expected {
				t.Fatalf("archive version %s, expected %s", manifest.Version, expected)
			}
			r := updater.Release{Tag: "v" + expected}
			r.Assets = append(r.Assets, updater.ReleaseAsset{Name: tc.name, URL: "https://github.com/jachy-h/lan-copy/releases/download/v" + expected + "/" + tc.name, Size: 1})
			if _, err := r.Asset(tc.goos, tc.arch); err != nil {
				t.Fatal("online updater cannot select asset:", err)
			}
		})
	}
}
