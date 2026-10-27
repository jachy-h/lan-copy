// Package printing owns printer discovery and the current process's test jobs.
package printing

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrUnavailable = errors.New("当前仅支持 Windows 主机打印")
	ErrNotFound    = errors.New("打印机不可用，请刷新后重新选择")
	ErrBusy        = errors.New("正在提交测试页，请稍候再试")
)

type Printer struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

type Job struct {
	ID       uint64    `json:"id"`
	Printer  string    `json:"printer"`
	Created  time.Time `json:"created"`
	Status   string    `json:"status"`
	SystemID int       `json:"systemId,omitempty"`
	Error    string    `json:"error,omitempty"`
	Name     string    `json:"name,omitempty"`
	Pages    int       `json:"pages,omitempty"`
	Warning  string    `json:"warning,omitempty"`
}

// Backend is the small boundary needed by the native implementation and tests.
type Backend interface {
	Printers() ([]Printer, error)
	PrintTest(string) (int, error)
}

type Service struct {
	backend   Backend
	spoolRoot string
	mu        sync.Mutex
	jobs      []Job
	nextID    uint64
	busy      bool
}

func (s *Service) Printers() ([]Printer, error) {
	if s == nil || s.backend == nil {
		return nil, ErrUnavailable
	}
	return s.backend.Printers()
}

func (s *Service) Jobs() []Job {
	if s == nil {
		return []Job{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Job{}, s.jobs...)
}

// Test reserves a single submission slot before discovery. Native driver calls
// can block, so they run outside both the HTTP request and the component lock.
// Jobs are never automatically retried: a driver error can follow partial output.
func (s *Service) Test(printer string) (Job, error) {
	if s == nil || s.backend == nil {
		return Job{}, ErrUnavailable
	}
	if printer == "" {
		return Job{}, ErrNotFound
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return Job{}, ErrBusy
	}
	s.busy = true
	s.mu.Unlock()
	job := s.record(printer, "Lan Copy 测试页", 1, "queued")
	go s.submit(job)
	return job, nil
}

func (s *Service) submit(job Job) {
	s.update(job.ID, "processing", 0, nil)
	printers, err := s.Printers()
	found := false
	for _, p := range printers {
		if p.Name == job.Printer {
			found = true
			break
		}
	}
	if err == nil && !found {
		err = ErrNotFound
	}
	var systemID int
	if err == nil {
		systemID, err = s.backend.PrintTest(job.Printer)
	}
	status := "submitted"
	if err != nil {
		status = "failed"
	}
	s.update(job.ID, status, systemID, err)
}

func (s *Service) update(id uint64, status string, systemID int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == "submitted" || status == "failed" {
		s.busy = false
	}
	for i := range s.jobs {
		if s.jobs[i].ID != id {
			continue
		}
		s.jobs[i].Status, s.jobs[i].SystemID = status, systemID
		if err != nil {
			s.jobs[i].Error = fmt.Sprint(err)
		}
		return
	}
}

func (s *Service) record(printer, name string, pages int, status string) Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	job := Job{ID: s.nextID, Printer: printer, Name: name, Pages: pages, Created: time.Now(), Status: status}
	s.jobs = append([]Job{job}, s.jobs...)
	if len(s.jobs) > 50 {
		s.jobs = s.jobs[:50]
	}
	return job
}
