package tablestore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"

	"lan-copy/internal/filestore"
)

const maxListEntries = 10000

type Store struct {
	files *filestore.Store

	marksMu   sync.RWMutex
	marks     map[string]struct{}
	marksPath string
}

func New(files *filestore.Store) *Store { return &Store{files: files} }

type File struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type Table struct {
	Path     string    `json:"path"`
	Version  string    `json:"version"`
	Modified time.Time `json:"modified"`
	*Document
}

func (s *Store) List(ctx context.Context) ([]File, error) {
	entries, err := s.files.ListDirectory(ctx, "", ".csv", maxListEntries)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.Directory || !strings.EqualFold(path.Ext(entry.Path), ".csv") {
			continue
		}
		files = append(files, File{Path: entry.Path, Name: entry.Name, Size: entry.Size, Modified: entry.Modified})
	}
	return files, nil
}

func (s *Store) Read(ctx context.Context, name string) (*Table, error) {
	if err := checkCSVPath(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := s.files.SnapshotFile(name, MaxFileBytes)
	if err != nil {
		if errors.Is(err, filestore.ErrFileTooLarge) {
			return nil, fmt.Errorf("%w: file is larger than %d bytes", ErrTooLarge, MaxFileBytes)
		}
		return nil, err
	}
	document, err := Parse(snapshot.Data)
	if err != nil {
		return nil, err
	}
	return &Table{Path: name, Version: snapshot.Version, Modified: snapshot.Modified, Document: document}, nil
}

func (s *Store) Query(ctx context.Context, name string, query Query) (*Table, Result, error) {
	table, err := s.Read(ctx, name)
	if err != nil {
		return nil, Result{}, err
	}
	result, err := table.Document.Query(query)
	if err != nil {
		return nil, Result{}, err
	}
	return table, result, nil
}

// LockAndUpdate validates the expected version and applies every operation
// inside one file-locked read/modify/write cycle.
func (s *Store) LockAndUpdate(ctx context.Context, name string, request UpdateRequest) (string, Applied, error) {
	if err := checkCSVPath(name); err != nil {
		return "", Applied{}, err
	}
	if request.ExpectedVersion == "" {
		return "", Applied{}, fmt.Errorf("%w: expectedVersion is required", ErrVersionConflict)
	}
	if err := ctx.Err(); err != nil {
		return "", Applied{}, err
	}
	var applied Applied
	snapshot, err := s.files.UpdateFile(name, MaxFileBytes, func(data []byte) ([]byte, error) {
		if filestore.Version(data) != request.ExpectedVersion {
			return nil, fmt.Errorf("%w: expected %s", ErrVersionConflict, request.ExpectedVersion)
		}
		document, err := Parse(data)
		if err != nil {
			return nil, err
		}
		applied, err = document.Apply(request)
		if err != nil {
			return nil, err
		}
		return document.Marshal()
	})
	if err != nil {
		if errors.Is(err, filestore.ErrFileTooLarge) {
			return "", Applied{}, fmt.Errorf("%w: file is larger than %d bytes", ErrTooLarge, MaxFileBytes)
		}
		return "", Applied{}, err
	}
	return snapshot.Version, applied, nil
}

func (s *Store) Create(ctx context.Context, name string, columns []string) (*Table, error) {
	if err := checkCSVPath(name); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document, err := NewDocument(columns)
	if err != nil {
		return nil, err
	}
	data, err := document.Marshal()
	if err != nil {
		return nil, err
	}
	snapshot, err := s.files.CreateFile(name, data)
	if err != nil {
		return nil, err
	}
	return &Table{Path: name, Version: snapshot.Version, Modified: snapshot.Modified, Document: document}, nil
}

// CurrentVersion returns the version of an existing table for conflict
// responses without parsing the CSV.
func (s *Store) CurrentVersion(name string) (string, error) {
	if err := checkCSVPath(name); err != nil {
		return "", err
	}
	snapshot, err := s.files.SnapshotFile(name, MaxFileBytes)
	if err != nil {
		if errors.Is(err, filestore.ErrFileTooLarge) {
			return "", fmt.Errorf("%w: file is larger than %d bytes", ErrTooLarge, MaxFileBytes)
		}
		return "", err
	}
	return snapshot.Version, nil
}

func checkCSVPath(name string) error {
	if name == "" || !strings.EqualFold(path.Ext(name), ".csv") {
		return fmt.Errorf("%w: %s", ErrNotCSV, name)
	}
	if strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return fs.ErrInvalid
	}
	return nil
}
