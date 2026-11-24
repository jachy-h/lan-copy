package tablestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lan-copy/internal/filestore"
)

func filestoreStoreForTest(t *testing.T, dir string) *filestore.Store {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filestore.New(dir, filepath.Join(dir, ".app-data"))
}

func TestParseRoundTripPreservesQuotedAndMultiline(t *testing.T) {
	input := "\xEF\xBB\xBFname,note\r\nAlice,\"hello, world\"\r\nBob,\"line1\r\nline2\"\r\n"
	document, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) != 2 || len(document.Rows) != 2 {
		t.Fatalf("unexpected table: %+v", document)
	}
	if document.Rows[1][1] != "line1\nline2" {
		t.Fatalf("multiline value = %q", document.Rows[1][1])
	}
	encoded, err := document.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(encoded), "\xEF\xBB\xBF") || !strings.Contains(string(encoded), "\r\n") {
		t.Fatalf("BOM or CRLF not preserved: %q", encoded)
	}
	reparsed, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Rows[0][1] != "hello, world" {
		t.Fatalf("round trip changed data: %+v", reparsed.Rows)
	}
}

func TestParseRejectsInvalidHeadersAndRaggedRows(t *testing.T) {
	for name, input := range map[string]string{
		"empty":      "",
		"no header":  "\n",
		"empty name": "id,,name\n1,x,y\n",
		"duplicate":  "id,Name,name\n1,a,b\n",
		"ragged":     "id,name\n1\n",
		"too many":   "id,name\n1,a,b\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("invalid CSV accepted")
			}
		})
	}
}

func TestQueryFiltersSortsAndPaginates(t *testing.T) {
	document, err := Parse([]byte("id,name,qty,status\nA,Alpha,10,pending\nB,Beta,2,done\nC,Gamma,30,pending\nD,Delta,4,done\n"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := document.Query(Query{
		Q:       "a",
		Filters: []Filter{{Column: "status", Op: "eq", Value: "pending"}},
		Sort:    &Sort{Column: "qty", Order: "desc"},
		Limit:   1,
		Offset:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Rows) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Rows[0].Values["id"] != "C" || result.Rows[0].Number != 3 {
		t.Fatalf("row = %+v", result.Rows[0])
	}
	result, err = document.Query(Query{Filters: []Filter{{Column: "qty", Op: "gt", Value: "3"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 {
		t.Fatalf("numeric comparison total = %d", result.Total)
	}
	if _, err := document.Query(Query{Filters: []Filter{{Column: "nope", Op: "eq"}}}); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("unknown column error = %v", err)
	}
}

func TestApplyIsAtomicOnFailure(t *testing.T) {
	document, err := Parse([]byte("id,status\nA,new\nB,new\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = document.Apply(UpdateRequest{
		KeyColumn: "id",
		Operations: []Operation{
			{Kind: "update", Key: "A", Changes: map[string]string{"status": "done"}},
			{Kind: "update", Key: "missing", Changes: map[string]string{"status": "done"}},
		},
	})
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("error = %v", err)
	}
	if document.Rows[0][1] != "new" {
		t.Fatalf("failed apply modified rows: %+v", document.Rows)
	}
	applied, err := document.Apply(UpdateRequest{
		KeyColumn: "id",
		Operations: []Operation{
			{Kind: "insert", Values: map[string]string{"id": "C", "status": "new"}},
			{Kind: "update", Key: "A", Changes: map[string]string{"status": "done"}},
			{Kind: "delete", Key: "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != (Applied{Inserted: 1, Updated: 1, Deleted: 1}) {
		t.Fatalf("applied = %+v", applied)
	}
	if len(document.Rows) != 2 || document.Rows[0][1] != "done" || document.Rows[1][0] != "C" {
		t.Fatalf("rows = %+v", document.Rows)
	}
}

func TestApplyRejectsKeyChangesAndDuplicates(t *testing.T) {
	document, err := Parse([]byte("id,status\nA,new\nB,new\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Apply(UpdateRequest{KeyColumn: "id", Operations: []Operation{{Kind: "update", Key: "A", Changes: map[string]string{"id": "C"}}}}); !errors.Is(err, ErrKeyChange) {
		t.Fatalf("key change error = %v", err)
	}
	if _, err := document.Apply(UpdateRequest{KeyColumn: "id", Operations: []Operation{{Kind: "insert", Values: map[string]string{"id": "A"}}}}); !errors.Is(err, ErrKeyDuplicate) {
		t.Fatalf("duplicate insert error = %v", err)
	}
	if _, err := document.Apply(UpdateRequest{Operations: []Operation{{Kind: "delete", Key: "A"}}}); !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("missing key column error = %v", err)
	}
}

func TestStoreCreateReadAndUpdate(t *testing.T) {
	dir := t.TempDir()
	files := filestoreStoreForTest(t, dir)
	store := New(files)
	ctx := context.Background()
	table, err := store.Create(ctx, "data/orders.csv", []string{"id", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if table.Version == "" {
		t.Fatal("missing version")
	}
	if _, err := store.Create(ctx, "data/orders.csv", []string{"id"}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate create error = %v", err)
	}
	version, applied, err := store.LockAndUpdate(ctx, "data/orders.csv", UpdateRequest{
		ExpectedVersion: table.Version,
		KeyColumn:       "id",
		Operations:      []Operation{{Kind: "insert", Values: map[string]string{"id": "A", "status": "new"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Inserted != 1 || version == table.Version {
		t.Fatalf("update = %+v version=%s", applied, version)
	}
	loaded, result, err := store.Query(ctx, "data/orders.csv", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != version || result.Total != 1 || result.Rows[0].Values["status"] != "new" {
		t.Fatalf("loaded = %+v result = %+v", loaded, result)
	}
	if _, _, err := store.LockAndUpdate(ctx, "data/orders.csv", UpdateRequest{
		ExpectedVersion: table.Version,
		KeyColumn:       "id",
		Operations:      []Operation{{Kind: "insert", Values: map[string]string{"id": "B"}}},
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	filesInDir, err := os.ReadDir(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if len(filesInDir) != 1 || strings.HasPrefix(filesInDir[0].Name(), ".lan-copy-") {
		t.Fatalf("unexpected files after update: %v", filesInDir)
	}
}

func TestStoreRejectsNonCSVAndTraversal(t *testing.T) {
	dir := t.TempDir()
	store := New(filestoreStoreForTest(t, dir))
	for _, name := range []string{"a.txt", "../a.csv", "/a.csv", "a\\b.csv"} {
		if _, err := store.Read(context.Background(), name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestStoreConcurrentUpdateOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	store := New(filestoreStoreForTest(t, dir))
	ctx := context.Background()
	table, err := store.Create(ctx, "data/race.csv", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, id := range []string{"A", "B"} {
		go func(index int, id string) {
			<-start
			_, _, err := store.LockAndUpdate(ctx, "data/race.csv", UpdateRequest{
				ExpectedVersion: table.Version,
				KeyColumn:       "id",
				Operations:      []Operation{{Kind: "insert", Values: map[string]string{"id": id}}},
			})
			results <- err
		}(index, id)
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}
