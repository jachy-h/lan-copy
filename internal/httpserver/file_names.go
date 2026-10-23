package httpserver

import (
	"mime"
	"path/filepath"
	"strings"
)

func contentType(name string) string {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); value != "" {
		return value
	}
	return "application/octet-stream"
}
