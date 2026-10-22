package main

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMacStartupRegistration(t *testing.T) {
	root := t.TempDir()
	spec := startupSpec{filepath.Join(root, `应用 & "工具"`, "lan-copy"), root, []string{"--autostart", "--working-dir", root, "-dir", filepath.Join(root, "共享 & 文件"), "-listen", ":9876"}}
	backend := &macStartup{filepath.Join(root, "LaunchAgents", startupLabel+".plist"), spec}
	if enabled, _, err := backend.Status(); enabled || err != nil {
		t.Fatalf("initial: %v %v", enabled, err)
	}
	for i := 0; i < 2; i++ {
		if err := backend.Set(true); err != nil {
			t.Fatal(err)
		}
		if enabled, _, err := backend.Status(); !enabled || err != nil {
			t.Fatalf("enabled: %v %v", enabled, err)
		}
	}
	f, err := os.Open(backend.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoder := xml.NewDecoder(f)
	var stringsInPlist []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				t.Fatal(err)
			}
			stringsInPlist = append(stringsInPlist, value)
		}
	}
	expected := append([]string{startupLabel, spec.Executable}, spec.Args...)
	expected = append(expected, root)
	if !reflect.DeepEqual(stringsInPlist, expected) {
		t.Fatalf("paths/arguments changed: %q", stringsInPlist)
	}
	changed := *backend
	changed.spec.Args = []string{"--autostart", "-listen", ":1234"}
	if enabled, reason, err := changed.Status(); enabled || reason == "" || err != nil {
		t.Fatalf("stale: %v %s %v", enabled, reason, err)
	}
	for i := 0; i < 2; i++ {
		if err := backend.Set(false); err != nil {
			t.Fatal(err)
		}
	}
	if enabled, _, err := backend.Status(); enabled || err != nil {
		t.Fatalf("disabled: %v %v", enabled, err)
	}
	// Deletion and registration in this test touch only the temp directory.
}
