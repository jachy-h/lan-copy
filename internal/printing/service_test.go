package printing

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type testBackend struct {
	list  func() ([]Printer, error)
	print func(string) (int, error)
}

func (b testBackend) Printers() ([]Printer, error)       { return b.list() }
func (b testBackend) PrintTest(name string) (int, error) { return b.print(name) }

func awaitJob(t *testing.T, service *Service, id uint64) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range service.Jobs() {
			if job.ID == id && (job.Status == "submitted" || job.Status == "failed") {
				return job
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("test submission did not finish")
	return Job{}
}

func TestSubmissionAndBusyProtection(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	s := serviceForTest(t, testBackend{
		list: func() ([]Printer, error) { return []Printer{{Name: "USB printer"}}, nil },
		print: func(name string) (int, error) {
			calls.Add(1)
			close(started)
			<-release
			return 42, nil
		},
	})
	job, err := s.Test("USB printer")
	if err != nil || job.Status != "queued" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("print not started")
	}
	if _, err := s.Test("USB printer"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second submission: %v", err)
	}
	snapshot := s.Jobs()
	snapshot[0].Status = "tampered"
	if s.Jobs()[0].Status != "processing" {
		t.Fatal("caller modified component state")
	}
	close(release)
	result := awaitJob(t, s, job.ID)
	if result.Status != "submitted" || result.SystemID != 42 || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
}

func TestPrinterValidationAndDriverFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		listErr  error
		printErr error
		want     string
		calls    int32
	}{
		{"missing", nil, nil, ErrNotFound.Error(), 0},
		{"printer", errors.New("spooler unavailable"), nil, "spooler unavailable", 0},
		{"printer", nil, errors.New("driver failed"), "driver failed", 1},
	} {
		t.Run(tc.want, func(t *testing.T) {
			var calls atomic.Int32
			s := serviceForTest(t, testBackend{
				list:  func() ([]Printer, error) { return []Printer{{Name: "printer"}}, tc.listErr },
				print: func(string) (int, error) { calls.Add(1); return 0, tc.printErr },
			})
			job, err := s.Test(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			result := awaitJob(t, s, job.ID)
			if result.Status != "failed" || result.Error != tc.want || calls.Load() != tc.calls {
				t.Fatalf("result=%+v calls=%d", result, calls.Load())
			}
		})
	}
}

func TestHistoryBoundAndUnavailable(t *testing.T) {
	s := serviceForTest(t, testBackend{
		list:  func() ([]Printer, error) { return []Printer{{Name: "printer"}}, nil },
		print: func(string) (int, error) { return 1, nil },
	})
	for i := 0; i < 55; i++ {
		job, err := s.Test("printer")
		if err != nil {
			t.Fatal(err)
		}
		awaitJob(t, s, job.ID)
	}
	jobs := s.Jobs()
	if len(jobs) != 50 || jobs[0].ID != 55 || jobs[49].ID != 6 {
		t.Fatalf("history=%+v", jobs)
	}
	for _, unavailable := range []*Service{nil, serviceForTest(t, nil)} {
		if _, err := unavailable.Printers(); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if _, err := unavailable.Test("printer"); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if len(unavailable.Jobs()) != 0 {
			t.Fatal("unexpected jobs")
		}
	}
}

func serviceForTest(t *testing.T, backend Backend) *Service {
	t.Helper()
	s, err := NewWithBackend(backend, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
