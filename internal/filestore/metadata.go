package filestore

import "strings"

// MetadataSuffix marks Lan Copy internal metadata files that live next to a
// shared file. They are never exposed through the file browser or file APIs.
const MetadataSuffix = ".hidden.metadata.json"

// IsHiddenMetadata reports whether a shared-root relative path is an internal
// metadata file. The comparison is case-insensitive so reserved names cannot
// be bypassed by changing case on case-insensitive file systems.
func IsHiddenMetadata(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), MetadataSuffix)
}
