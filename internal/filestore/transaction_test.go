package filestore

import (
	"bytes"
	"errors"
	"testing"
)

func TestUploadBudgetWriterUsesRemainingCapacity(t *testing.T) {
	m := NewResources(ResourceConfig{Concurrent: 1, StagingGiB: 1, IdleSeconds: 5})
	if !m.Reserve(gib - 2) {
		t.Fatal("failed to reserve staging bytes")
	}
	var output bytes.Buffer
	var staged int64
	w := budgetWriter{writer: &output, resources: m, staged: &staged}
	n, err := w.Write([]byte("hello"))
	if n != 2 || !errors.Is(err, ErrStagingBudget) || output.String() != "he" || staged != 2 {
		t.Fatalf("write=(%d,%v), output=%q staged=%d", n, err, output.String(), staged)
	}
}
