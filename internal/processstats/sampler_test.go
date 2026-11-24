package processstats

import (
	"testing"
	"time"
)

func TestSamplerNeedsBaselineThenReportsCPU(t *testing.T) {
	if _, err := processCPU(); err != nil {
		t.Skipf("process CPU sampling unavailable: %v", err)
	}
	sampler := New()
	first := sampler.Sample()
	if first.CPUReady {
		t.Fatal("first sample should only establish the CPU baseline")
	}
	if first.MemoryBytes <= 0 || first.SampledAt.IsZero() {
		t.Fatalf("first sample = %+v", first)
	}

	time.Sleep(minCPUInterval + 100*time.Millisecond)
	second := sampler.Sample()
	if !second.CPUReady {
		t.Fatal("second sample should report CPU usage")
	}
	if second.CPUPercent < 0 {
		t.Fatalf("cpu percent = %v", second.CPUPercent)
	}
	if second.MemoryBytes <= 0 {
		t.Fatalf("memory bytes = %v", second.MemoryBytes)
	}
}

func TestSamplerReusesRecentCPUPercent(t *testing.T) {
	if _, err := processCPU(); err != nil {
		t.Skipf("process CPU sampling unavailable: %v", err)
	}
	sampler := New()
	_ = sampler.Sample()
	time.Sleep(minCPUInterval + 100*time.Millisecond)
	first := sampler.Sample()
	second := sampler.Sample()
	if second.CPUPercent != first.CPUPercent || second.CPUReady != first.CPUReady {
		t.Fatalf("recent sample = %+v, previous = %+v", second, first)
	}
}
