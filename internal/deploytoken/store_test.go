package deploytoken

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreSaveLoadMatchAndClear(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if store.Configured() || store.Status().Source != "none" || store.Match("Bearer secret") {
		t.Fatal("new store should be unconfigured")
	}
	if err := store.Save("secret"); err != nil {
		t.Fatal(err)
	}
	if !store.Configured() || store.Status() != (Status{Configured: true, Source: "file"}) {
		t.Fatalf("unexpected saved status: %+v", store.Status())
	}
	if !store.Match("Bearer secret") || store.Match("Bearer wrong") || store.Match("secret") {
		t.Fatal("saved store did not enforce the bearer token")
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "sha256") {
		t.Fatalf("token file must contain only a digest: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, FileName))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("token permissions = %o", info.Mode().Perm())
		}
	}

	reloaded, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Match("Bearer secret") || reloaded.Status().Source != "file" {
		t.Fatal("saved token did not survive a restart")
	}
	if err := reloaded.Clear(); err != nil {
		t.Fatal(err)
	}
	if reloaded.Configured() || reloaded.Match("Bearer secret") {
		t.Fatal("cleared token is still active")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token file still exists: %v", err)
	}
}

func TestStoreUsesEnvironmentUntilAFileTokenIsSaved(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, "environment-token")
	if err != nil {
		t.Fatal(err)
	}
	if store.Status() != (Status{Configured: true, Source: "environment"}) || !store.Match("Bearer environment-token") {
		t.Fatal("environment token was not loaded")
	}
	if err := store.Save("file-token"); err != nil {
		t.Fatal(err)
	}
	if store.Status().Source != "file" || !store.Match("Bearer file-token") || store.Match("Bearer environment-token") {
		t.Fatal("file token should override the environment token")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if store.Status().Source != "environment" || !store.Match("Bearer environment-token") {
		t.Fatal("environment token did not return after clearing the file token")
	}
}

func TestStoreRejectsInvalidTokensAndState(t *testing.T) {
	for _, token := range []string{"", "has space", "line\nbreak", "中文", strings.Repeat("a", maxTokenLength+1)} {
		if Valid(token) {
			t.Fatalf("accepted invalid token %q", token)
		}
	}
	for _, token := range []string{"a", "secret", strings.Repeat("a", maxTokenLength), "!#$%&'()*+-./:<=>?@[]^_`{|}~"} {
		if !Valid(token) {
			t.Fatalf("rejected valid token %q", token)
		}
	}
	if _, err := New(t.TempDir(), "has space"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid environment token error = %v", err)
	}
	dir := t.TempDir()
	store, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("has space"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid save error = %v", err)
	}
	for _, content := range []string{"not json", `{"version":2,"sha256":"00"}`, `{"version":1,"sha256":"xyz"}`, `{"version":1,"sha256":"0000"}`} {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(dir, ""); err == nil {
			t.Fatalf("accepted invalid token state %q", content)
		}
	}
}
