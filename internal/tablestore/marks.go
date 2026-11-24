package tablestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lan-copy/internal/filestore"
)

// MarksFileName stores the optional browser-side smart-table marks. The file
// lives in the private application data directory, never in the shared folder.
const MarksFileName = "table-marks.json"

type marksDocument struct {
	Paths []string `json:"paths"`
}

// LoadMarks restores the marks file. A missing file is an empty set; malformed
// JSON or invalid paths fail startup instead of silently dropping state.
func (s *Store) LoadMarks(dataDir string) error {
	name := filepath.Join(dataDir, MarksFileName)
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		s.marksMu.Lock()
		s.marksPath = name
		s.marks = make(map[string]struct{})
		s.marksMu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var document marksDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("%s: %w", MarksFileName, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: trailing data", MarksFileName)
	}
	marks := make(map[string]struct{}, len(document.Paths))
	for index, name := range document.Paths {
		if err := validateMarkPath(name); err != nil {
			return fmt.Errorf("%s: path %d is invalid: %w", MarksFileName, index+1, err)
		}
		marks[name] = struct{}{}
	}
	s.marksMu.Lock()
	s.marksPath = name
	s.marks = marks
	s.marksMu.Unlock()
	return nil
}

// IsMarked reports whether a CSV path is marked as a smart table.
func (s *Store) IsMarked(name string) bool {
	s.marksMu.RLock()
	defer s.marksMu.RUnlock()
	_, ok := s.marks[name]
	return ok
}

// SetMark adds or removes one mark. Adding validates that the path is a
// readable CSV table before anything is persisted.
func (s *Store) SetMark(ctx context.Context, name string, marked bool) error {
	if err := validateMarkPath(name); err != nil {
		return err
	}
	s.marksMu.Lock()
	defer s.marksMu.Unlock()
	if s.marksPath == "" {
		return errors.New("table marks are not loaded")
	}
	if marked {
		if _, err := s.Read(ctx, name); err != nil {
			return err
		}
	}
	next := cloneMarks(s.marks)
	if marked {
		next[name] = struct{}{}
	} else {
		delete(next, name)
	}
	if sameMarks(s.marks, next) {
		return nil
	}
	if err := s.persistMarks(next); err != nil {
		return err
	}
	s.marks = next
	return nil
}

// RemoveMarks removes a deleted file or every mark inside a deleted folder.
func (s *Store) RemoveMarks(name string, directory bool) error {
	s.marksMu.Lock()
	defer s.marksMu.Unlock()
	if s.marksPath == "" {
		return nil
	}
	next := cloneMarks(s.marks)
	for marked := range next {
		if marked == name || directory && strings.HasPrefix(marked, name+"/") {
			delete(next, marked)
		}
	}
	if sameMarks(s.marks, next) {
		return nil
	}
	if err := s.persistMarks(next); err != nil {
		return err
	}
	s.marks = next
	return nil
}

// RelocateMarks follows an internal rename or move. A CSV renamed to a
// non-CSV extension loses its mark because it is no longer a table.
func (s *Store) RelocateMarks(previous, current string, directory bool) error {
	s.marksMu.Lock()
	defer s.marksMu.Unlock()
	if s.marksPath == "" {
		return nil
	}
	next := cloneMarks(s.marks)
	changed := false
	for marked := range next {
		if marked != previous && (!directory || !strings.HasPrefix(marked, previous+"/")) {
			continue
		}
		delete(next, marked)
		changed = true
		target := current + strings.TrimPrefix(marked, previous)
		if !directory && validateMarkPath(target) != nil {
			continue
		}
		next[target] = struct{}{}
	}
	if !changed || sameMarks(s.marks, next) {
		return nil
	}
	if err := s.persistMarks(next); err != nil {
		return err
	}
	s.marks = next
	return nil
}

func (s *Store) persistMarks(marks map[string]struct{}) error {
	paths := make([]string, 0, len(marks))
	for name := range marks {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(marksDocument{Paths: paths}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.marksPath)
	file, err := os.CreateTemp(dir, ".table-marks-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temp, s.marksPath); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func validateMarkPath(name string) error {
	if err := checkCSVPath(name); err != nil {
		return err
	}
	for _, component := range strings.Split(name, "/") {
		if err := filestore.ValidateComponent(component); err != nil {
			return fs.ErrInvalid
		}
	}
	return nil
}

func cloneMarks(marks map[string]struct{}) map[string]struct{} {
	next := make(map[string]struct{}, len(marks)+1)
	for name := range marks {
		next[name] = struct{}{}
	}
	return next
}

func sameMarks(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if _, ok := b[name]; !ok {
			return false
		}
	}
	return true
}
