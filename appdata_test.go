package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppDataDirectoryIsSiblingOfSharedDirectory(t *testing.T) {
	parent := t.TempDir()
	sharedDir := filepath.Join(parent, "shared")
	want := filepath.Join(parent, appDataDirectoryPrefix+"shared")
	if got := appDataDirectory(sharedDir); got != want {
		t.Fatalf("application data directory = %q, want sibling %q", got, want)
	}
}

func TestStartupMigratesLegacyInternalDataAndRecoversIt(t *testing.T) {
	parent := t.TempDir()
	sharedDir := filepath.Join(parent, "shared")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	legacyTexts := []byte(`[{"id":"0011223344556677","content":"migrated text","created":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(filepath.Join(sharedDir, textStoreBackup), legacyTexts, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, ".lan-copy-texts-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction := filepath.Join(sharedDir, uploadTransactionPrefix+"interrupted")
	for _, dir := range []string{
		filepath.Join(sharedDir, "incoming"),
		filepath.Join(transaction, "stage"),
		filepath.Join(transaction, "backup"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "incoming", "replace.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transaction, "backup", "replace.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(uploadTransactionRecord{
		Destination: "incoming",
		Roots:       []string{"replace.txt"},
		Replaced:    []string{"replace.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transaction, "transaction.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := newServer(sharedDir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if app.dataDir != filepath.Join(parent, appDataDirectoryPrefix+"shared") {
		t.Fatalf("server data directory = %q", app.dataDir)
	}
	texts, err := app.readTextsLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || texts[0].Content != "migrated text" {
		t.Fatalf("migrated texts = %+v", texts)
	}
	got, err := os.ReadFile(filepath.Join(sharedDir, "incoming", "replace.txt"))
	if err != nil || string(got) != "original" {
		t.Fatalf("recovered replacement = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(app.dataDir, textStore)); err != nil {
		t.Fatalf("recovered text store is not in application data directory: %v", err)
	}
	for _, name := range []string{textStoreBackup, ".lan-copy-texts-interrupted", uploadTransactionPrefix + "interrupted"} {
		if _, err := os.Lstat(filepath.Join(sharedDir, name)); !os.IsNotExist(err) {
			t.Errorf("legacy internal item %q remains in shared directory: %v", name, err)
		}
	}
}

func TestStartupRejectsConflictingLegacyAndApplicationData(t *testing.T) {
	parent := t.TempDir()
	sharedDir := filepath.Join(parent, "shared")
	dataDir := appDataDirectory(sharedDir)
	for _, dir := range []string{sharedDir, dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	legacy := filepath.Join(sharedDir, textStore)
	current := filepath.Join(dataDir, textStore)
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newServer(sharedDir, 1<<20); err == nil {
		t.Fatal("expected conflicting internal data to stop startup")
	}
	for name, want := range map[string]string{legacy: "legacy", current: "current"} {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != want {
			t.Errorf("data at %q = %q, %v; want %q", name, got, err, want)
		}
	}
}
