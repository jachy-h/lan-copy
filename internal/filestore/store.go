package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Store struct {
	dir     string
	dataDir string
	mu      sync.Mutex
}

func New(dir, dataDir string) *Store { return &Store{dir: dir, dataDir: dataDir} }

func (s *Store) Path(name string) (string, error) {
	if name == "" {
		return s.dir, nil
	}
	if strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return "", fs.ErrInvalid
	}
	current := s.dir
	for _, component := range strings.Split(name, "/") {
		if err := ValidateComponent(component); err != nil {
			return "", fs.ErrInvalid
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fs.ErrPermission
		}
	}
	return current, nil
}

// ValidateComponent is the common cross-platform rule for every path
// component that can enter or be addressed inside the shared directory.
func ValidateComponent(component string) error {
	if component == "" || component == "." || component == ".." || strings.TrimSpace(component) != component || strings.ContainsAny(component, "/\\:*?\"<>|") || strings.HasSuffix(component, ".") || strings.ContainsRune(component, 0) {
		return fs.ErrInvalid
	}
	lower := strings.ToLower(component)
	if strings.HasPrefix(lower, ".upload-") || strings.HasPrefix(lower, ".lan-copy-") {
		return fs.ErrInvalid
	}
	for _, char := range component {
		if char < 32 || char == 127 {
			return fs.ErrInvalid
		}
	}
	// Windows reserves these device names even when one or more extensions
	// follow them. COM/LPT also accept the superscript digits documented by
	// Windows, and CONIN$/CONOUT$ are reserved console device names.
	stem := component
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(stem)
	runes := []rune(stem)
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || (len(runes) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && (runes[3] >= '1' && runes[3] <= '9' || runes[3] == '¹' || runes[3] == '²' || runes[3] == '³')) {
		return fs.ErrInvalid
	}
	return nil
}

func SafeName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	if err := ValidateComponent(name); err != nil {
		return "", errors.New("invalid file name")
	}
	return name, nil
}

const MaxAvailableNameAttempts = 10000

func AvailableName(dir, name string) (string, error) {
	return AvailableNameWithLstat(dir, name, os.Lstat)
}

func AvailableNameWithLstat(dir, name string, lstat func(string) (os.FileInfo, error)) (string, error) {
	if _, err := lstat(filepath.Join(dir, name)); err == nil {
		// Try numbered names below.
	} else if errors.Is(err, os.ErrNotExist) {
		return name, nil
	} else {
		return "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; i <= MaxAvailableNameAttempts; i++ {
		candidate := base + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := lstat(filepath.Join(dir, candidate)); err == nil {
			continue
		} else if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else {
			return "", err
		}
	}
	return "", fs.ErrExist
}
