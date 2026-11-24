package plugin

import (
	"context"
	"errors"
	"testing"
)

func setUsageState(manager *Manager, plugins []Plugin, instances []Instance) {
	manager.mu.Lock()
	manager.state.Plugins = plugins
	manager.state.Instances = instances
	manager.mu.Unlock()
}

func TestUsageAggregatesRunningPlugins(t *testing.T) {
	manager, _, _ := testManager(t)
	setUsageState(manager,
		[]Plugin{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta"}, {ID: "gamma", Name: "Gamma"}},
		[]Instance{
			{ID: "ia", PluginID: "alpha", RuntimeInstanceID: "ra", Status: StatusRunning},
			{ID: "ib", PluginID: "beta", RuntimeInstanceID: "rb", Status: StatusRunning},
			{ID: "ig", PluginID: "gamma", RuntimeInstanceID: "rg", Status: StatusStopped},
		})
	usage := manager.Usage(context.Background())
	if usage.Running != 2 || usage.Unavailable != 0 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CPUPercent != 3 || usage.MemoryBytes != 2048 || usage.MemoryLimitBytes != 4096 || usage.PIDs != 6 {
		t.Fatalf("totals = %+v", usage)
	}
	if len(usage.Items) != 2 || usage.Items[0].Name != "Alpha" || usage.Items[1].Name != "Beta" {
		t.Fatalf("items = %+v", usage.Items)
	}
	for _, item := range usage.Items {
		if !item.Available || item.CPUPercent != 1.5 || item.MemoryBytes != 1024 || item.PIDs != 3 {
			t.Fatalf("item = %+v", item)
		}
	}
}

func TestUsageKeepsPartialTotalsWhenOnePluginFails(t *testing.T) {
	manager, runtime, _ := testManager(t)
	runtime.statsErrByID = map[string]error{"rb": errors.New("runtime unavailable")}
	setUsageState(manager,
		[]Plugin{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta"}},
		[]Instance{
			{ID: "ia", PluginID: "alpha", RuntimeInstanceID: "ra", Status: StatusRunning},
			{ID: "ib", PluginID: "beta", RuntimeInstanceID: "rb", Status: StatusRunning},
		})
	usage := manager.Usage(context.Background())
	if usage.Running != 2 || usage.Unavailable != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CPUPercent != 1.5 || usage.MemoryBytes != 1024 || usage.PIDs != 3 {
		t.Fatalf("partial totals = %+v", usage)
	}
	if len(usage.Items) != 2 || !usage.Items[0].Available || usage.Items[1].Available {
		t.Fatalf("items = %+v", usage.Items)
	}
	if usage.Items[1].Error == "" {
		t.Fatalf("failed item error = %q", usage.Items[1].Error)
	}
}

func TestUsageWithoutRunningPlugins(t *testing.T) {
	manager, _, _ := testManager(t)
	usage := manager.Usage(context.Background())
	if usage.Running != 0 || usage.Unavailable != 0 || len(usage.Items) != 0 {
		t.Fatalf("usage = %+v", usage)
	}
}
