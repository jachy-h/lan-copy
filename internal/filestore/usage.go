package filestore

import (
	"path/filepath"
	"sort"
	"strings"
)

// TypeUsage summarizes one storage category.
type TypeUsage struct {
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// Usage summarizes regular files currently visible in the shared directory.
type Usage struct {
	Bytes int64       `json:"bytes"`
	Files int         `json:"files"`
	Types []TypeUsage `json:"types"`
}

var storageKindOrder = []string{"image", "video", "audio", "document", "archive", "code", "other"}

var storageKindRank = func() map[string]int {
	ranks := make(map[string]int, len(storageKindOrder))
	for index, kind := range storageKindOrder {
		ranks[kind] = index
	}
	return ranks
}()

var storageExtensionKinds = func() map[string]string {
	groups := map[string]string{
		"image":    "jpg jpeg png gif webp avif bmp ico svg heic heif tif tiff",
		"video":    "mp4 m4v webm ogv mov mkv avi wmv flv",
		"audio":    "mp3 m4a wav flac ogg oga aac opus",
		"document": "pdf doc docx xls xlsx ppt pptx csv tsv txt md rtf odt ods odp epub",
		"archive":  "zip tar gz tgz bz2 xz 7z rar zst",
		"code":     "go js mjs cjs ts tsx jsx vue html htm css scss less json xml yaml yml toml ini sh bash zsh ps1 py rb php rs java kt c h cpp hpp cs sql",
	}
	kinds := make(map[string]string)
	for kind, extensions := range groups {
		for _, extension := range strings.Fields(extensions) {
			kinds["."+extension] = kind
		}
	}
	return kinds
}()

func storageKind(name string) string {
	if kind, ok := storageExtensionKinds[strings.ToLower(filepath.Ext(name))]; ok {
		return kind
	}
	return "other"
}

func (s *Store) Usage() (Usage, error) {
	files, err := s.ListFiles()
	if err != nil {
		return Usage{}, err
	}
	usage := Usage{Files: len(files), Types: make([]TypeUsage, 0, len(storageKindOrder))}
	byKind := make(map[string]*TypeUsage, len(storageKindOrder))
	for _, file := range files {
		usage.Bytes += file.Size
		kind := storageKind(file.Name)
		entry, ok := byKind[kind]
		if !ok {
			entry = &TypeUsage{Kind: kind}
			byKind[kind] = entry
		}
		entry.Bytes += file.Size
		entry.Files++
	}
	for _, entry := range byKind {
		if entry.Files > 0 {
			usage.Types = append(usage.Types, *entry)
		}
	}
	sort.Slice(usage.Types, func(i, j int) bool {
		if usage.Types[i].Bytes != usage.Types[j].Bytes {
			return usage.Types[i].Bytes > usage.Types[j].Bytes
		}
		return storageKindRank[usage.Types[i].Kind] < storageKindRank[usage.Types[j].Kind]
	})
	return usage, nil
}
