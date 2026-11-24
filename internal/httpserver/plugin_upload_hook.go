package httpserver

import (
	"context"

	"lan-copy/internal/plugin"
)

// pluginUploadDialog is the browser-facing form of a validated upload-hook
// page. The URL is always inside this plugin's own /plugin-app/<id>/ route.
type pluginUploadDialog struct {
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

// runUploadHooks delegates to the plugin manager unless a test override is
// set. Hook failures are already isolated by the manager and never fail the
// upload itself.
func (s *Server) runUploadHooks(ctx context.Context, destination string, files []fileInfo, directories []string) []pluginUploadDialog {
	if s == nil || len(files) == 0 {
		return nil
	}
	data := uploadEventData(destination, files, directories)
	var dialogs []plugin.UploadHookDialog
	switch {
	case s.runPluginUploadHooks != nil:
		dialogs = s.runPluginUploadHooks(ctx, data)
	case s.plugins != nil:
		dialogs = s.plugins.RunUploadHooks(ctx, data)
	}
	if len(dialogs) == 0 {
		return nil
	}
	result := make([]pluginUploadDialog, 0, len(dialogs))
	seen := make(map[string]struct{}, len(dialogs))
	for _, dialog := range dialogs {
		if dialog.PluginID == "" || dialog.Path == "" {
			continue
		}
		// The manager already validates the path; keep the prefix construction
		// in one place so the HTTP layer never accepts an absolute URL.
		url := pluginUIPrefix + dialog.PluginID + dialog.Path
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		result = append(result, pluginUploadDialog{
			PluginID: dialog.PluginID, PluginName: dialog.PluginName,
			Title: dialog.Title, URL: url,
		})
	}
	return result
}
