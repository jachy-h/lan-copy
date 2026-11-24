// Package processstats samples the resource usage of the Lan Copy process.
package processstats

import (
	"math"
	"runtime"
	"sync"
	"time"
)

const minCPUInterval = 500 * time.Millisecond

// Sample is one point-in-time snapshot of the current process.
type Sample struct {
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes int64     `json:"memoryBytes"`
	CPUReady    bool      `json:"cpuReady"`
	SampledAt   time.Time `json:"sampledAt"`
}

// Sampler keeps the previous CPU reading so percentages can be derived from a
// delta. It runs no background goroutine and is safe for concurrent use.
type Sampler struct {
	mu          sync.Mutex
	lastCPU     time.Duration
	lastAt      time.Time
	lastPercent float64
	ready       bool
}

func New() *Sampler { return &Sampler{} }

func (s *Sampler) Sample() Sample {
	cpu, cpuErr := processCPU()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	sample := Sample{MemoryBytes: int64(memory.Sys), SampledAt: now}
	if cpuErr != nil {
		return sample
	}
	if s.lastAt.IsZero() {
		s.lastCPU, s.lastAt = cpu, now
		return sample
	}
	elapsed := now.Sub(s.lastAt)
	if elapsed < minCPUInterval {
		sample.CPUPercent, sample.CPUReady = s.lastPercent, s.ready
		return sample
	}
	delta := cpu - s.lastCPU
	if delta < 0 {
		delta = 0
	}
	percent := float64(delta) / float64(elapsed) * 100
	sample.CPUPercent = math.Round(percent*10) / 10
	sample.CPUReady = true
	s.lastCPU, s.lastAt = cpu, now
	s.lastPercent, s.ready = sample.CPUPercent, true
	return sample
}
