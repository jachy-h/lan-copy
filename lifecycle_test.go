package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransferTrackingCoversHandlerLifetime(t *testing.T) {
	app, err := newServer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	handler := app.trackTransfer(transferDownload, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/files/example.txt", nil))
		close(done)
	}()
	<-entered
	counts, stopping := app.transfers.snapshot()
	if stopping || counts.Downloads != 1 || counts.Uploads != 0 {
		t.Fatalf("active transfer snapshot = %+v, stopping=%v", counts, stopping)
	}
	close(release)
	<-done
	counts, _ = app.transfers.snapshot()
	if counts.total() != 0 {
		t.Fatalf("transfer count after handler return = %+v", counts)
	}
	if _, started := app.transfers.beginShutdown(false, nil); !started {
		t.Fatal("shutdown did not start after transfer completed")
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/files/example.txt", nil))
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("new transfer after shutdown status = %d", result.Code)
	}
}
