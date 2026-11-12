//go:build windows

package deploytoken

// Windows does not expose portable directory-handle fsync through os.File.
func syncDirectory(string) error { return nil }
