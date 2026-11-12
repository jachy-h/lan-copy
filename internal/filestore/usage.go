package filestore

// Usage summarizes regular files currently visible in the shared directory.
type Usage struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
}

func (s *Store) Usage() (Usage, error) {
	files, err := s.ListFiles()
	if err != nil {
		return Usage{}, err
	}
	usage := Usage{Files: len(files)}
	for _, file := range files {
		usage.Bytes += file.Size
	}
	return usage, nil
}
