package tablestore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"

	"lan-copy/internal/filestore"
)

const (
	// LayoutFileSuffix is appended to the full table file name. Preferences for
	// data/orders.csv therefore live in data/orders.csv.hidden.metadata.json.
	LayoutFileSuffix = filestore.MetadataSuffix
	MaxLayoutBytes   = 256 << 10
)

// Layout stores browser-only column preferences for one smart table. It never
// changes the CSV itself and never changes the table version.
type Layout struct {
	Version int      `json:"version"`
	Order   []string `json:"order"`
	Hidden  []string `json:"hidden"`
}

// LayoutPath returns the sidecar path for a table path.
func LayoutPath(table string) string {
	return table + LayoutFileSuffix
}

func emptyLayout() Layout {
	return Layout{Version: 1, Order: []string{}, Hidden: []string{}}
}

// ReadLayout reads column preferences. A missing, unreadable, oversized or
// malformed sidecar is treated as "no preferences" so opening a table never
// fails because of metadata.
func (s *Store) ReadLayout(name string) (Layout, error) {
	if err := checkCSVPath(name); err != nil {
		return Layout{}, err
	}
	snapshot, err := s.files.SnapshotFile(LayoutPath(name), MaxLayoutBytes)
	if err != nil {
		return emptyLayout(), nil
	}
	var layout Layout
	if err := json.Unmarshal(snapshot.Data, &layout); err != nil || layout.Version != 1 {
		return emptyLayout(), nil
	}
	return sanitizeLayout(layout), nil
}

// NormalizeLayout reconciles saved preferences with the current CSV header.
// Existing columns keep the saved order, new columns are appended in physical
// order, and removed columns disappear. Every column can be hidden, including
// the record identifier column.
func NormalizeLayout(layout Layout, columns []string) Layout {
	canonical := make(map[string]string, len(columns))
	for _, name := range columns {
		canonical[strings.ToLower(name)] = name
	}
	normalized := emptyLayout()
	seen := make(map[string]struct{}, len(columns))
	for _, name := range layout.Order {
		key := strings.ToLower(name)
		actual, ok := canonical[key]
		if !ok {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized.Order = append(normalized.Order, actual)
	}
	for _, name := range columns {
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized.Order = append(normalized.Order, name)
	}
	hiddenSeen := make(map[string]struct{}, len(layout.Hidden))
	for _, name := range layout.Hidden {
		key := strings.ToLower(name)
		actual, ok := canonical[key]
		if !ok {
			continue
		}
		if _, duplicate := hiddenSeen[key]; duplicate {
			continue
		}
		hiddenSeen[key] = struct{}{}
		normalized.Hidden = append(normalized.Hidden, actual)
	}
	return normalized
}

// SaveLayout validates the table, normalizes preferences against its current
// header and atomically replaces the sidecar file.
func (s *Store) SaveLayout(ctx context.Context, name string, layout Layout) (Layout, error) {
	if err := checkCSVPath(name); err != nil {
		return Layout{}, err
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, err
	}
	table, err := s.Read(ctx, name)
	if err != nil {
		return Layout{}, err
	}
	normalized := NormalizeLayout(sanitizeLayout(layout), table.Columns)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return Layout{}, err
	}
	if _, err := s.files.WriteFile(LayoutPath(name), append(data, '\n')); err != nil {
		return Layout{}, err
	}
	return normalized, nil
}

// RelocateLayout follows a single-file rename or move. A table renamed to a
// non-CSV name loses its preferences. Directory moves need no work because the
// sidecar moves with the directory and stores no table path.
func (s *Store) RelocateLayout(previous, current string) error {
	if previous == current {
		return nil
	}
	if checkCSVPath(previous) != nil {
		return nil
	}
	if checkCSVPath(current) != nil {
		return s.removeLayoutFile(previous)
	}
	snapshot, err := s.files.SnapshotFile(LayoutPath(previous), MaxLayoutBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, filestore.ErrFileTooLarge) {
			return nil
		}
		return err
	}
	if _, err := s.files.WriteFile(LayoutPath(current), snapshot.Data); err != nil {
		return err
	}
	if err := s.files.DeleteFile(LayoutPath(previous)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveLayout deletes preferences for one table. Missing files are ignored.
func (s *Store) RemoveLayout(name string) error {
	if checkCSVPath(name) != nil {
		return nil
	}
	return s.removeLayoutFile(name)
}

func (s *Store) removeLayoutFile(name string) error {
	if err := s.files.DeleteFile(LayoutPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func sanitizeLayout(layout Layout) Layout {
	clean := emptyLayout()
	clean.Order = cleanNames(layout.Order)
	clean.Hidden = cleanNames(layout.Hidden)
	return clean
}

func cleanNames(names []string) []string {
	result := make([]string, 0, min(len(names), MaxColumns))
	for _, name := range names {
		if len(result) >= MaxColumns {
			break
		}
		if name == "" || len(name) > MaxCellBytes {
			continue
		}
		result = append(result, name)
	}
	return result
}
