package plugin

import (
	"context"
	"sort"
	"sync"
)

type usageTarget struct {
	pluginID string
	name     string
	instance Instance
}

// Usage samples every running plugin container. Individual failures are kept
// in Items so the caller can still show partial totals and Lan Copy's own data.
func (m *Manager) Usage(ctx context.Context) Usage {
	m.mu.Lock()
	targets := make([]usageTarget, 0, len(m.state.Instances))
	for _, instance := range m.state.Instances {
		if instance.Status != StatusRunning || instance.RuntimeInstanceID == "" {
			continue
		}
		name := instance.PluginID
		if pluginEntry, ok := m.pluginLocked(instance.PluginID); ok {
			name = pluginEntry.Name
		}
		targets = append(targets, usageTarget{pluginID: instance.PluginID, name: name, instance: instance.Clone()})
	}
	m.mu.Unlock()

	usage := Usage{Running: len(targets), Items: make([]UsageItem, len(targets)), SampledAt: m.now()}
	if len(targets) == 0 {
		return usage
	}
	items := usage.Items
	var wait sync.WaitGroup
	for index, target := range targets {
		wait.Add(1)
		go func(index int, target usageTarget) {
			defer wait.Done()
			item := UsageItem{PluginID: target.pluginID, Name: target.name}
			stats, err := m.stats(ctx, target.instance)
			if err != nil {
				item.Error = truncate(err.Error(), 200)
				items[index] = item
				return
			}
			if stats == nil {
				item.Error = "当前没有可用的资源采样"
				items[index] = item
				return
			}
			item.Available = true
			item.CPUPercent = stats.CPUPercent
			item.MemoryBytes = stats.MemoryBytes
			item.MemoryLimitBytes = stats.MemoryLimitBytes
			item.PIDs = stats.PIDs
			items[index] = item
		}(index, target)
	}
	wait.Wait()

	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].PluginID < items[j].PluginID
	})
	for _, item := range items {
		if !item.Available {
			usage.Unavailable++
			continue
		}
		usage.CPUPercent += item.CPUPercent
		usage.MemoryBytes += item.MemoryBytes
		usage.MemoryLimitBytes += item.MemoryLimitBytes
		usage.PIDs += item.PIDs
	}
	return usage
}
