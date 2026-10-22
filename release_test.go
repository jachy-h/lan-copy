package main

import (
	"bytes"
	"context"
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
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeReleaseService(t *testing.T, tag string, archive []byte) (*releaseService, *int) {
	t.Helper()
	name := "局域网快传-Windows-" + runtime.GOARCH + ".zip"
	if runtime.GOOS == "darwin" {
		name = "Lan-Copy-macOS-Apple-Silicon.tar.gz"
	}
	r := githubRelease{Tag: tag}
	r.Assets = append(r.Assets, githubReleaseAsset{name, "https://github.com/jachy-h/lan-copy/releases/download/" + tag + "/package", int64(len(archive))})
	raw, _ := json.Marshal(r)
	calls := 0
	s := newReleaseService()
	s.client.Transport = releaseTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body := archive
		if req.URL.String() == releaseAPI {
			body = raw
		} else if req.URL.Host != "github.com" {
			t.Fatalf("unexpected URL %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), ContentLength: int64(len(body))}, nil
	})
	return s, &calls
}
func TestCompareReleaseVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
		bad  bool
	}{
		{"v1.10.0", "1.9.9", 1, false}, {"0.1.10", "v0.1.10", 0, false}, {"1.0.0", "2.0.0", -1, false},
		{"dev", "1.0.0", 0, true}, {"1.0.0-beta", "1.0.0", 0, true}, {"01.2.3", "1.2.3", 0, true},
	} {
		got, err := compareVersions(tc.a, tc.b)
		if got != tc.want || (err != nil) != tc.bad {
			t.Fatalf("%s vs %s: %d, %v", tc.a, tc.b, got, err)
		}
	}
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
	w := httptest.NewRecorder()
	s.checkRelease(w, httptest.NewRequest("GET", "/api/update/check", nil))
	if !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatal(w.Body.String())
	}
}
func TestReleaseAssetValidation(t *testing.T) {
	r := githubRelease{Tag: "v1.0.0"}
	for _, address := range []string{"http://github.com/jachy-h/lan-copy/releases/download/v1.0.0/p.zip", "https://evil.example/p.zip", "https://github.com/other/repo/releases/download/v1.0.0/p.zip", "https://github.com/jachy-h/lan-copy/releases/download/v0.9.0/p.zip"} {
		r.Assets = []githubReleaseAsset{{"局域网快传-Windows-amd64.zip", address, 10}}
		if _, err := r.asset("windows", "amd64"); err == nil {
			t.Fatal("accepted", address)
		}
	}
	if _, err := r.asset("linux", "amd64"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	s := newReleaseService()
	for _, address := range []string{"http://github.com/file", "https://evil.example/file", "https://github.com.evil.example/file"} {
		req := httptest.NewRequest("GET", address, nil)
		if s.client.CheckRedirect(req, nil) == nil {
			t.Fatal("accepted redirect", address)
		}
	}
}
func TestReleaseFailures(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
	}{{404, ""}, {403, ""}, {200, `{"tag_name":"v1.0.0","prerelease":true}`}, {200, `{`}} {
		s := newReleaseService()
		s.client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		if _, err := s.latest(context.Background()); err == nil {
			t.Fatal("accepted", tc)
		}
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
			s.updater = &updateService{executable: target, launch: func(_ string, p updatePlan) error {
				launched = true
				data, e := os.ReadFile(p.Candidate)
				if e != nil || !bytes.Equal(data, binary) {
					t.Fatal("invalid candidate")
				}
				return nil
			}}
			signing := priv
			if !tc.signed {
				signing = nil
			}
			archive := zipUpdateTest(t, packageUpdateTest(t, binary, tc.manifest, runtime.GOOS, runtime.GOARCH, signing))
			s.releases, _ = fakeReleaseService(t, "v1.1.0", archive)
			req := httptest.NewRequest("POST", "/api/update/download", nil)
			req.Header.Set("X-Lan-Copy-Action", "update")
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, req)
			if w.Code != tc.want || launched != (tc.want == 202) {
				t.Fatal(w.Code, w.Body.String(), launched)
			}
			if tc.want != 202 && s.updater.busy {
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

// Run against the actual CI archives before uploading them as Release assets.
func TestPublishedReleasePackages(t *testing.T) {
	dir := os.Getenv("LAN_COPY_RELEASE_DIR")
	if dir == "" {
		t.Skip("set LAN_COPY_RELEASE_DIR to verify packaged Release assets")
	}
	expected := os.Getenv("LAN_COPY_RELEASE_VERSION")
	if _, err := compareVersions(expected, expected); err != nil {
		t.Fatal("missing or invalid LAN_COPY_RELEASE_VERSION")
	}
	for _, tc := range []struct{ name, goos, arch string }{
		{"局域网快传-Windows-amd64.zip", "windows", "amd64"},
		{"Lan-Copy-macOS-Apple-Silicon.tar.gz", "darwin", "arm64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := unpackUpdate(filepath.Join(dir, tc.name), filepath.Join(t.TempDir(), "candidate"), tc.goos, tc.arch, os.Getenv("UPDATE_PUBLIC_KEY"))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Version != expected {
				t.Fatalf("archive version %s, expected %s", manifest.Version, expected)
			}
			r := githubRelease{Tag: "v" + expected}
			r.Assets = append(r.Assets, githubReleaseAsset{tc.name, "https://github.com/jachy-h/lan-copy/releases/download/v" + expected + "/" + tc.name, 1})
			if _, err := r.asset(tc.goos, tc.arch); err != nil {
				t.Fatal("online updater cannot select asset:", err)
			}
		})
	}
}
