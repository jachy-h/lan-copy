//go:build windows

package plugin

// Windows does not expose portable directory-handle fsync through os.File.
// File contents are still flushed before replacement; directory-entry
// durability across sudden power loss is therefore platform limited.
func syncDir(string) error { return nil }
