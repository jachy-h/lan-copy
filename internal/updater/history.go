package updater

import (
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const historyFile = "history.json"

type historyRecord struct {
	Target    string    `json:"target"`
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Completed time.Time `json:"completed"`
}

type historyEntry struct {
	dir     string
	version string
	id      string
	time    time.Time
	legacy  bool
}

func writeHistoryRecord(stage string, p Plan) error {
	if p.Version == "" || p.ID == "" {
		return errors.New("更新计划缺少版本或更新 ID")
	}
	target, err := filepath.Abs(p.Target)
	if err != nil {
		return err
	}
	data, err := json.Marshal(historyRecord{Target: filepath.Clean(target), ID: p.ID, Version: p.Version, Completed: time.Now().UTC()})
	if err != nil {
		return err
	}
	tmp := filepath.Join(stage, historyFile+".tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(stage, historyFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CleanupHistory scans only direct children of the executable directory. A
// directory is removable only when its exact contents match a recognized
// completed layout and its updater executable is a Lan Copy binary.
func CleanupHistory(executable string, report func(string)) error {
	target, err := filepath.Abs(executable)
	if err != nil {
		return err
	}
	target = filepath.Clean(target)
	root := filepath.Dir(target)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var candidates []historyEntry
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".lan-copy-update-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		item, recognized, err := inspectHistoryDir(path, target)
		if err != nil {
			if report != nil {
				report(fmt.Sprintf("跳过更新目录 %s：%v", path, err))
			}
			continue
		}
		if recognized {
			item.dir = path
			candidates = append(candidates, item)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.legacy && !b.legacy && !a.time.Equal(b.time) {
			return a.time.After(b.time)
		}
		if a.legacy != b.legacy {
			return !a.legacy // Newly recorded versions take precedence over legacy residue.
		}
		if !a.legacy && a.id != b.id {
			return a.id > b.id
		}
		if !a.time.Equal(b.time) {
			return a.time.After(b.time)
		}
		return filepath.Base(a.dir) > filepath.Base(b.dir)
	})

	keptVersions := make(map[string]bool)
	keep := make(map[string]bool)
	for _, item := range candidates {
		if keptVersions[item.version] {
			continue
		}
		if len(keptVersions) == 2 {
			continue
		}
		keptVersions[item.version] = true
		keep[item.dir] = true
	}
	var errs []error
	for _, item := range candidates {
		if keep[item.dir] {
			continue
		}
		if err := os.RemoveAll(item.dir); err != nil {
			errs = append(errs, fmt.Errorf("删除 %s：%w", item.dir, err))
			if report != nil {
				report(fmt.Sprintf("删除更新目录失败 %s：%v", item.dir, err))
			}
		}
	}
	return errors.Join(errs...)
}

func inspectHistoryDir(dir, target string) (historyEntry, bool, error) {
	var zero historyEntry
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return zero, false, errors.New("不是普通目录")
	}
	children, err := os.ReadDir(dir)
	if err != nil {
		return zero, false, err
	}
	for _, child := range children {
		if child.Type()&os.ModeSymlink != 0 || !child.Type().IsRegular() {
			return zero, false, fmt.Errorf("包含链接或特殊文件 %s", child.Name())
		}
	}
	updaterName := "updater"
	if runtime.GOOS == "windows" {
		updaterName += ".exe"
	}
	if len(children) == 1 && children[0].Name() == updaterName {
		if err := verifyUpdater(filepath.Join(dir, updaterName)); err != nil {
			return zero, false, err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return zero, false, err
		}
		return historyEntry{version: "legacy:" + filepath.Base(dir), id: filepath.Base(dir), time: info.ModTime(), legacy: true}, true, nil
	}
	if len(children) != 2 || !containsNames(children, updaterName, historyFile) {
		return zero, false, errors.New("目录仍在使用或内容无法确认")
	}
	if err := verifyUpdater(filepath.Join(dir, updaterName)); err != nil {
		return zero, false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, historyFile))
	if err != nil {
		return zero, false, err
	}
	var record historyRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version == "" || record.ID == "" || record.Completed.IsZero() {
		return zero, false, errors.New("更新历史记录无效")
	}
	recordedTarget, err := filepath.Abs(record.Target)
	if err != nil || filepath.Clean(recordedTarget) != target {
		return zero, false, errors.New("更新历史不属于当前程序")
	}
	return historyEntry{version: record.Version, id: record.ID, time: record.Completed}, true, nil
}

func containsNames(entries []os.DirEntry, first, second string) bool {
	return (entries[0].Name() == first && entries[1].Name() == second) || (entries[0].Name() == second && entries[1].Name() == first)
}

func verifyUpdater(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("辅助程序不是普通文件")
	}
	build, err := buildinfo.ReadFile(path)
	if err != nil || build.Path != "lan-copy" {
		return errors.New("无法确认辅助程序属于 Lan Copy")
	}
	return nil
}
