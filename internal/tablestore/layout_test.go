package tablestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeLayoutReordersKeepsNewAndDropsMissing(t *testing.T) {
	layout := Layout{
		Version: 1,
		Order:   []string{"status", "id", "missing", "status"},
		Hidden:  []string{"name", "missing", "id", "name"},
	}
	normalized := NormalizeLayout(layout, []string{"id", "name", "status", "created"})
	if got := normalized.Order; len(got) != 4 || got[0] != "status" || got[1] != "id" || got[2] != "name" || got[3] != "created" {
		t.Fatalf("order = %v", got)
	}
	if got := normalized.Hidden; len(got) != 2 || got[0] != "name" || got[1] != "id" {
		t.Fatalf("hidden = %v", got)
	}
	if normalized.Version != 1 {
		t.Fatalf("version = %d", normalized.Version)
	}
}

func TestLayoutSaveAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := New(filestoreStoreForTest(t, dir))
	ctx := context.Background()
	if _, err := store.Create(ctx, "data/orders.csv", []string{"id", "name", "status"}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.SaveLayout(ctx, "data/orders.csv", Layout{
		Version: 1,
		Order:   []string{"status", "id", "name"},
		Hidden:  []string{"name", "id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Order) != 3 || saved.Order[0] != "status" {
		t.Fatalf("saved = %+v", saved)
	}
	sidecar := filepath.Join(dir, "data", "orders.csv.hidden.metadata.json")
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	read, err := store.ReadLayout("data/orders.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Order) != 3 || read.Order[2] != "name" || len(read.Hidden) != 2 || read.Hidden[0] != "name" || read.Hidden[1] != "id" {
		t.Fatalf("read = %+v", read)
	}
}

func TestLayoutReadToleratesBrokenMetadata(t *testing.T) {
	dir := t.TempDir()
	store := New(filestoreStoreForTest(t, dir))
	ctx := context.Background()
	if _, err := store.Create(ctx, "orders.csv", []string{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "orders.csv.hidden.metadata.json")
	for name, data := range map[string]string{
		"invalid json":  "{",
		"unknown":       `{"version":99,"order":["name"]}`,
		"large payload": string(make([]byte, MaxLayoutBytes+1)),
	} {
		if err := os.WriteFile(sidecar, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		layout, err := store.ReadLayout("orders.csv")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(layout.Order) != 0 || len(layout.Hidden) != 0 {
			t.Fatalf("%s: layout = %+v", name, layout)
		}
	}
}

func TestLayoutFollowsRenameAndDelete(t *testing.T) {
	dir := t.TempDir()
	store := New(filestoreStoreForTest(t, dir))
	ctx := context.Background()
	if _, err := store.Create(ctx, "data/orders.csv", []string{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveLayout(ctx, "data/orders.csv", Layout{Version: 1, Order: []string{"name", "id"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RelocateLayout("data/orders.csv", "data/sales.csv"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "orders.csv.hidden.metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("old sidecar still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "sales.csv.hidden.metadata.json")); err != nil {
		t.Fatalf("new sidecar missing: %v", err)
	}
	if err := store.RelocateLayout("data/sales.csv", "data/notes.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "sales.csv.hidden.metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("non-csv rename kept sidecar: %v", err)
	}
	if err := store.RemoveLayout("data/sales.csv"); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutPathUsesFullTableName(t *testing.T) {
	if got := LayoutPath("data/orders.csv"); got != "data/orders.csv.hidden.metadata.json" {
		t.Fatalf("path = %q", got)
	}
}
