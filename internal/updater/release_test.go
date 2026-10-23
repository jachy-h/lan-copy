package updater

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCompareReleaseVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
		bad  bool
	}{
		{"v1.10.0", "1.9.9", 1, false}, {"0.1.10", "v0.1.10", 0, false}, {"1.0.0", "2.0.0", -1, false},
		{"dev", "1.0.0", 0, true}, {"1.0.0-beta", "1.0.0", 0, true}, {"01.2.3", "1.2.3", 0, true},
	} {
		got, err := CompareVersions(tc.a, tc.b)
		if got != tc.want || (err != nil) != tc.bad {
			t.Fatalf("%s vs %s: %d, %v", tc.a, tc.b, got, err)
		}
	}
}

func TestReleaseAssetValidation(t *testing.T) {
	r := Release{Tag: "v1.0.0"}
	for _, address := range []string{"http://github.com/jachy-h/lan-copy/releases/download/v1.0.0/p.zip", "https://evil.example/p.zip", "https://github.com/other/repo/releases/download/v1.0.0/p.zip", "https://github.com/jachy-h/lan-copy/releases/download/v0.9.0/p.zip"} {
		r.Assets = []ReleaseAsset{{"局域网快传-Windows-amd64.zip", address, 10}}
		if _, err := r.Asset("windows", "amd64"); err == nil {
			t.Fatal("accepted", address)
		}
	}
	if _, err := r.Asset("linux", "amd64"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	s := NewReleaseService()
	for _, address := range []string{"http://github.com/file", "https://evil.example/file", "https://github.com.evil.example/file"} {
		req := httptest.NewRequest("GET", address, nil)
		if s.client.CheckRedirect(req, nil) == nil {
			t.Fatal("accepted redirect", address)
		}
	}
}

func TestReleaseWindowsAssetNames(t *testing.T) {
	for _, name := range []string{
		"Lan-Copy-Windows-amd64.zip",
		"局域网快传-Windows-amd64.zip",
		"-Windows-amd64.zip",
	} {
		t.Run(name, func(t *testing.T) {
			address := "https://github.com/jachy-h/lan-copy/releases/download/v1.0.0/" + name
			r := Release{Tag: "v1.0.0", Assets: []ReleaseAsset{{Name: name, URL: address, Size: 10}}}
			got, err := r.Asset("windows", "amd64")
			if err != nil || got != address {
				t.Fatalf("asset %q: %q, %v", name, got, err)
			}
		})
	}
}
func TestReleaseFailures(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
	}{{404, ""}, {403, ""}, {200, `{"tag_name":"v1.0.0","prerelease":true}`}, {200, `{`}} {
		s := NewReleaseService()
		s.client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		if _, err := s.Latest(context.Background()); err == nil {
			t.Fatal("accepted", tc)
		}
	}
}
