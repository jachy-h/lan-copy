package httpserver

import (
	"lan-copy/internal/filestore"
	"lan-copy/internal/plugin"
	"lan-copy/internal/textstore"
)

const maxEventListEntries = 100

// publishEvent is a nil-safe bridge from HTTP handlers to lifecycle delivery.
// It never blocks the request: the plugin manager only enqueues.
func (s *Server) publishEvent(eventType string, data any) {
	if s == nil {
		return
	}
	if s.publishPluginEvent != nil {
		s.publishPluginEvent(eventType, data)
		return
	}
	if s.plugins == nil {
		return
	}
	s.plugins.Publish(eventType, data)
}

func (s *Server) publishMutation(result filestore.MutationResult) {
	var eventType string
	switch result.Action {
	case "mkdir":
		eventType = "folder.created"
	case "delete":
		if result.Directory {
			eventType = "folder.deleted"
		} else {
			eventType = "file.deleted"
		}
	case "rename":
		if result.Directory {
			eventType = "folder.renamed"
		} else {
			eventType = "file.renamed"
		}
	case "move":
		if result.Directory {
			eventType = "folder.moved"
		} else {
			eventType = "file.moved"
		}
	default:
		return
	}
	s.publishEvent(eventType, plugin.EntryEventData{
		Path: result.Path, Name: result.Name,
		PreviousPath: result.PreviousPath, PreviousName: result.PreviousName,
		Size: result.Size, Recursive: result.Recursive,
	})
}

// uploadEventData builds the bounded payload shared by the asynchronous
// lifecycle event and the synchronous upload hook.
func uploadEventData(destination string, files []fileInfo, directories []string) plugin.FileUploadedData {
	data := plugin.FileUploadedData{
		Destination: destination,
		Files:       make([]plugin.UploadedFile, 0, len(files)),
		Directories: make([]string, 0, len(directories)),
		Counts:      plugin.EventCounts{Files: len(files), Directories: len(directories)},
	}
	remaining := maxEventListEntries
	for _, file := range files {
		if remaining == 0 {
			data.Truncated = true
			break
		}
		data.Files = append(data.Files, plugin.UploadedFile{Path: file.Name, Size: file.Size})
		remaining--
	}
	for _, directory := range directories {
		if remaining == 0 {
			data.Truncated = true
			break
		}
		data.Directories = append(data.Directories, directory)
		remaining--
	}
	return data
}

// publishUpload sends one batch event per committed upload. Large folder
// uploads are truncated at a fixed item limit so the payload stays bounded.
func (s *Server) publishUpload(destination string, files []fileInfo, directories []string) {
	s.publishEvent("file.uploaded", uploadEventData(destination, files, directories))
}

func (s *Server) publishTextEvent(eventType string, item textstore.Fragment) {
	expires := item.ExpiresAt
	size := int64(len([]byte(item.Content)))
	for _, image := range item.Images {
		size += image.Size
	}
	if size == 0 {
		size = item.Size
	}
	s.publishEvent(eventType, plugin.TextEventData{
		ID: item.ID, Kind: item.Kind, Title: item.Name, Size: size, ExpiresAt: &expires,
	})
}

func (s *Server) publishTextSaved(id string, files []plugin.SavedTextFile) {
	s.publishEvent("text.saved", plugin.TextSavedData{ID: id, Files: files})
}
