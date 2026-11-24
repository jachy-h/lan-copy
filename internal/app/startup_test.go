package app

import (
	"strings"
	"testing"
)

func TestStartupSummaryListsLocalAndLANAddresses(t *testing.T) {
	lines := startupSummary("/tmp/share", "8080")
	if len(lines) < 2 {
		t.Fatalf("startupSummary() = %v", lines)
	}
	if lines[0] != "Lan Copy 已启动，文件保存在 /tmp/share" {
		t.Fatalf("first line = %q", lines[0])
	}
	if lines[1] != "本机访问: http://localhost:8080" {
		t.Fatalf("second line = %q", lines[1])
	}
	for _, line := range lines[2:] {
		if !strings.HasPrefix(line, "局域网访问: http://") {
			t.Fatalf("unexpected LAN line = %q", line)
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "插件") {
			t.Fatalf("startup summary leaked plugin details: %q", line)
		}
	}
}
