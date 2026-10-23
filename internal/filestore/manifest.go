package filestore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxTreeEntries = 10000

type TreeEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
}

type TreeManifest struct {
	Entries []TreeEntry `json:"entries"`
	Replace []string    `json:"replace,omitempty"`
}

type preflightRecord struct {
	Destination string
	Fingerprint string
	Expires     time.Time
}

type PreflightManager struct {
	mu       sync.Mutex
	sessions map[string]preflightRecord
}

func NewPreflightManager() *PreflightManager {
	return &PreflightManager{sessions: make(map[string]preflightRecord)}
}

func uploadEntriesFingerprint(entries []TreeEntry) string {
	data, _ := json.Marshal(entries)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *PreflightManager) Create(destination string, entries []TreeEntry) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, value := range m.sessions {
		if now.After(value.Expires) {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= 256 {
		return "", errors.New("too many pending upload preflights")
	}
	m.sessions[id] = preflightRecord{Destination: destination, Fingerprint: uploadEntriesFingerprint(entries), Expires: now.Add(15 * time.Minute)}
	return id, nil
}

func (m *PreflightManager) Consume(id, destination string, entries []TreeEntry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[id]
	delete(m.sessions, id)
	return ok && time.Now().Before(record.Expires) && record.Destination == destination && record.Fingerprint == uploadEntriesFingerprint(entries)
}

func (m *PreflightManager) Release(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// All paths are relative to a private staging directory, never to the host.
// Reject case aliases too, so the same manifest works on Windows and macOS.
func ValidateUploadTree(entries []TreeEntry) ([]string, []string, error) {
	if len(entries) == 0 || len(entries) > MaxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	explicit := make(map[string]bool)
	canonical := make(map[string]string)
	directories := make(map[string]bool)
	roots := make(map[string]bool)
	for _, entry := range entries {
		if len(entry.Path) > 4096 || entry.Path == "" || path.Clean(entry.Path) != entry.Path || strings.Contains(entry.Path, "\\") || strings.HasPrefix(entry.Path, "/") {
			return nil, nil, fs.ErrInvalid
		}
		components := strings.Split(entry.Path, "/")
		if len(components) > 64 {
			return nil, nil, fs.ErrInvalid
		}
		for index, component := range components {
			if err := ValidateComponent(component); err != nil {
				return nil, nil, fs.ErrInvalid
			}
			prefix := strings.Join(components[:index+1], "/")
			key := strings.ToLower(prefix)
			if existing, ok := canonical[key]; ok && existing != prefix {
				return nil, nil, fs.ErrInvalid
			}
			canonical[key] = prefix
			if index < len(components)-1 {
				directories[prefix] = true
			}
		}
		if _, exists := explicit[entry.Path]; exists {
			return nil, nil, fs.ErrInvalid
		}
		explicit[entry.Path] = entry.Directory
		if entry.Directory {
			directories[entry.Path] = true
		}
		roots[components[0]] = true
	}
	for directory := range directories {
		if isDirectory, exists := explicit[directory]; exists && !isDirectory {
			return nil, nil, fs.ErrInvalid
		}
	}
	// Implicit parent directories also count toward the operation limit.
	if len(canonical) > MaxTreeEntries {
		return nil, nil, fs.ErrInvalid
	}
	dirs := make([]string, 0, len(directories))
	top := make([]string, 0, len(roots))
	for directory := range directories {
		dirs = append(dirs, directory)
	}
	for root := range roots {
		top = append(top, root)
	}
	sort.Strings(dirs)
	sort.Strings(top)
	return dirs, top, nil
}
