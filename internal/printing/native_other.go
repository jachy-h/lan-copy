//go:build !windows

package printing

func New(dataDir string) (*Service, error) { return NewWithBackend(nil, dataDir) }
