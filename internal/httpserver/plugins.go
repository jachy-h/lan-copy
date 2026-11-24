package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lan-copy/internal/plugin"
)

const (
	pluginAction       = "plugins"
	pluginImageAction  = "plugins-image"
	maxPluginJSONBody  = 1 << 20
	pluginRequestLimit = 30 * time.Second
	maxPluginLogBytes  = 512 << 10
)

// pluginDispatch handles /api/plugins/* in one place. Go's ServeMux cannot
// combine /images/{id} with /{id}/status without an ambiguous match, so the
// subtree is dispatched explicitly while handlers stay small.
func (s *Server) pluginDispatch() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
		if path == "" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				writeError(w, http.StatusMethodNotAllowed, "请求方法不支持")
				return
			}
			s.listPlugins(w, r)
			return
		}
		segments := strings.Split(path, "/")
		if len(segments) == 1 {
			switch segment := segments[0]; segment {
			case "runtime":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginRuntime(w, r)
			case "install":
				if r.Method != http.MethodPost {
					methodNotAllowed(w, "POST")
					return
				}
				s.installPlugin(w, r)
			case "images":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.listPluginImages(w, r)
			case "tasks":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.listPluginTasks(w, r)
			case "event-catalog":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginEventCatalog(w, r)
			default:
				r.SetPathValue("id", segment)
				switch r.Method {
				case http.MethodGet:
					s.pluginDetail(w, r)
				case http.MethodDelete:
					s.uninstallPlugin(w, r)
				default:
					methodNotAllowed(w, "GET, DELETE")
				}
			}
			return
		}
		if len(segments) == 2 && segments[0] == "runtime" && segments[1] == "start" {
			if r.Method != http.MethodPost {
				methodNotAllowed(w, "POST")
				return
			}
			s.startPluginRuntime(w, r)
			return
		}
		if len(segments) == 2 && segments[0] == "images" && segments[1] == "upload" {
			if r.Method != http.MethodPost {
				methodNotAllowed(w, "POST")
				return
			}
			s.uploadPluginImage(w, r)
			return
		}
		if len(segments) == 2 && segments[0] == "images" {
			r.SetPathValue("id", segments[1])
			switch r.Method {
			case http.MethodGet:
				s.pluginImage(w, r)
			case http.MethodDelete:
				s.deletePluginImage(w, r)
			default:
				methodNotAllowed(w, "GET, DELETE")
			}
			return
		}
		if len(segments) == 2 && segments[0] == "tasks" {
			r.SetPathValue("id", segments[1])
			if r.Method != http.MethodGet {
				methodNotAllowed(w, "GET")
				return
			}
			s.pluginTask(w, r)
			return
		}
		if len(segments) == 3 && segments[1] == "events" && segments[2] == "test" {
			if r.Method != http.MethodPost {
				methodNotAllowed(w, "POST")
				return
			}
			r.SetPathValue("id", segments[0])
			s.pluginTestEvent(w, r)
			return
		}
		if len(segments) == 2 {
			pluginID, action := segments[0], segments[1]
			r.SetPathValue("id", pluginID)
			switch action {
			case "start", "stop", "restart":
				if r.Method != http.MethodPost {
					methodNotAllowed(w, "POST")
					return
				}
				s.pluginLifecycle(action)(w, r)
			case "status":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginStatus(w, r)
			case "health":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginHealth(w, r)
			case "logs":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginLogs(w, r)
			case "manifest":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginManifest(w, r)
			case "capabilities":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginCapabilities(w, r)
			case "config":
				if r.Method != http.MethodGet && r.Method != http.MethodPut {
					methodNotAllowed(w, "GET, PUT")
					return
				}
				s.pluginConfig(w, r)
			case "events":
				if r.Method != http.MethodGet {
					methodNotAllowed(w, "GET")
					return
				}
				s.pluginEvents(w, r)
			default:
				writeError(w, http.StatusNotFound, "插件接口不存在")
			}
			return
		}
		writeError(w, http.StatusNotFound, "插件接口不存在")
	})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "请求方法不支持")
}

func (s *Server) pluginRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.plugins.RuntimeInfo(ctx))
}

// startPluginRuntime lets any device that can manage plugins ask the host to
// start Podman. The operation runs as a task so long machine boots do not hold
// an HTTP request open.
func (s *Server) startPluginRuntime(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	task, err := s.plugins.RunTask("runtime-start", "", func(ctx context.Context, progress func(int, string)) (any, error) {
		if err := s.plugins.StartRuntime(ctx, progress); err != nil {
			return nil, err
		}
		infoCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return s.plugins.RuntimeInfo(infoCtx), nil
	})
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"taskId": task.ID})
}

func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": s.plugins.Plugins()})
}

func (s *Server) pluginDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	detail, err := s.plugins.Detail(ctx, r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) installPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	var request plugin.InstallRequest
	if err := decodePluginJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "安装参数无效")
		return
	}
	task, err := s.plugins.RunTask("install", request.ExpectedPluginID, func(ctx context.Context, progress func(int, string)) (any, error) {
		if progress != nil {
			progress(10, "正在创建插件")
		}
		result, err := s.plugins.Install(ctx, request)
		if err != nil {
			return nil, err
		}
		if progress != nil {
			progress(100, "插件安装完成")
		}
		return result, nil
	})
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"taskId": task.ID})
}

func (s *Server) pluginLifecycle(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requirePlugins(w, r, pluginAction) {
			return
		}
		pluginID := r.PathValue("id")
		messages := map[string]string{"start": "正在启动插件", "stop": "正在停止插件", "restart": "正在重启插件"}
		task, err := s.plugins.RunTask(action, pluginID, func(ctx context.Context, progress func(int, string)) (any, error) {
			if progress != nil {
				progress(10, messages[action])
			}
			var runErr error
			switch action {
			case "start":
				runErr = s.plugins.Start(ctx, pluginID)
			case "stop":
				runErr = s.plugins.Stop(ctx, pluginID)
			case "restart":
				runErr = s.plugins.Restart(ctx, pluginID)
			}
			if runErr != nil {
				return nil, runErr
			}
			if progress != nil {
				progress(100, "完成")
			}
			return map[string]string{"pluginId": pluginID, "status": "ok"}, nil
		})
		if err != nil {
			pluginHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"taskId": task.ID})
	}
}

func (s *Server) uninstallPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	var request plugin.UninstallRequest
	if err := decodePluginJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "卸载参数无效")
		return
	}
	if request.DeleteData && !request.ConfirmDataDeletion {
		writeError(w, http.StatusBadRequest, "删除持久化数据需要二次确认")
		return
	}
	pluginID := r.PathValue("id")
	task, err := s.plugins.RunTask("uninstall", pluginID, func(ctx context.Context, progress func(int, string)) (any, error) {
		if progress != nil {
			progress(10, "正在卸载插件")
		}
		if err := s.plugins.Uninstall(ctx, pluginID, request); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(100, "插件已卸载")
		}
		return map[string]string{"pluginId": pluginID, "status": "uninstalled"}, nil
	})
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"taskId": task.ID})
}

func (s *Server) pluginStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := s.plugins.Status(ctx, r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) pluginHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := s.plugins.Status(ctx, r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pluginId": status.PluginID, "healthStatus": status.HealthStatus,
		"status": status.Status, "lastHealthAt": status.LastHealthAt,
		"records": status.HealthRecords, "reason": status.StatusReason,
	})
}

func (s *Server) pluginManifest(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	manifest, err := s.plugins.Manifest(r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (s *Server) pluginCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	capabilities, err := s.plugins.Capabilities(r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": capabilities})
}

func (s *Server) pluginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if !s.requirePlugins(w, r, "") {
			return
		}
		config, err := s.plugins.Config(r.PathValue("id"))
		if err != nil {
			pluginHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, maskConfig(config))
		return
	}
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	var config plugin.Config
	if err := decodePluginJSON(w, r, &config); err != nil {
		writeError(w, http.StatusBadRequest, "插件配置无效")
		return
	}
	updated, err := s.plugins.UpdateConfig(r.PathValue("id"), config)
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, maskConfig(updated))
}

func maskConfig(config plugin.Config) plugin.Config {
	return plugin.MaskConfig(config)
}

func (s *Server) pluginLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			tail = parsed
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), pluginRequestLimit)
	defer cancel()
	body, err := s.plugins.Logs(ctx, r.PathValue("id"), plugin.LogOptions{Tail: tail, MaxBytes: maxPluginLogBytes})
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxPluginLogBytes+1))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	truncated := int64(len(data)) > maxPluginLogBytes
	if truncated {
		data = data[:maxPluginLogBytes]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if truncated {
		w.Header().Set("X-Lan-Copy-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	if truncated {
		_, _ = io.WriteString(w, "\n…日志已截断")
	}
}

func (s *Server) listPluginImages(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": s.plugins.Images()})
}

func (s *Server) pluginImage(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	image, err := s.plugins.Image(r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, image)
}

func (s *Server) deletePluginImage(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pluginRequestLimit)
	defer cancel()
	if err := s.plugins.RemoveImage(ctx, r.PathValue("id")); err != nil {
		pluginHTTPError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uploadPluginImage(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginImageAction) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, plugin.MaxImageUploadBytes+1<<20)
	r.Body = newIdleTimeoutBody(r.Body, 60*time.Second)
	defer r.Body.Close()
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择 Docker 镜像包")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少镜像文件")
		return
	}
	if part.FormName() != "image" || part.FileName() == "" {
		writeError(w, http.StatusBadRequest, "multipart 字段必须为 image")
		return
	}
	staged, err := s.plugins.StageImage(part.FileName(), part)
	_ = part.Close()
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	task, err := s.plugins.RunTask("image_import", "", func(ctx context.Context, progress func(int, string)) (any, error) {
		image, err := s.plugins.ImportStaged(ctx, staged, progress)
		if err != nil {
			return nil, err
		}
		return map[string]string{"imageId": image.ID, "pluginId": image.PluginID}, nil
	})
	if err != nil {
		s.plugins.DiscardStaged(staged)
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"taskId": task.ID})
}

func (s *Server) listPluginTasks(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.plugins.Tasks(limit)})
}

func (s *Server) pluginEventCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": plugin.EventCatalog()})
}

func (s *Server) pluginEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	view, err := s.plugins.EventEndpoint(r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) pluginTestEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, pluginAction) {
		return
	}
	id, err := s.plugins.SendTestEvent(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, plugin.ErrPluginNotRunning) || errors.Is(err, plugin.ErrPluginUnhealthy) ||
			errors.Is(err, plugin.ErrPluginNotInstalled) || errors.Is(err, plugin.ErrPluginUnavailable) {
			writeError(w, http.StatusConflict, "插件未运行、健康检查未通过或未配置事件端点，无法发送测试事件")
			return
		}
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"eventId": id})
}

func (s *Server) pluginTask(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlugins(w, r, "") {
		return
	}
	task, err := s.plugins.Task(r.PathValue("id"))
	if err != nil {
		pluginHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) requirePlugins(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.plugins == nil {
		writeError(w, http.StatusServiceUnavailable, "插件管理不可用")
		return false
	}
	if action != "" && !validActionRequest(r, action) {
		writeError(w, http.StatusForbidden, "请求来源校验失败")
		return false
	}
	return true
}

func decodePluginJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.ContentLength == 0 {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPluginJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEnd(decoder)
}

func pluginHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, plugin.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, plugin.ErrImageInUse), errors.Is(err, plugin.ErrAlreadyInstalled),
		errors.Is(err, plugin.ErrConflict), errors.Is(err, plugin.ErrBusy):
		status = http.StatusConflict
	case errors.Is(err, plugin.ErrInvalidConfig), errors.Is(err, plugin.ErrInvalidManifest),
		errors.Is(err, plugin.ErrNoPluginManifest), errors.Is(err, plugin.ErrProtocolMismatch):
		status = http.StatusBadRequest
	case errors.Is(err, plugin.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, plugin.ErrUnsupportedArchive), errors.Is(err, plugin.ErrUnsupported):
		status = http.StatusUnsupportedMediaType
	case errors.Is(err, plugin.ErrRuntimeUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, plugin.ErrPluginTimeout):
		status = http.StatusGatewayTimeout
	case errors.Is(err, plugin.ErrPluginUnhealthy), errors.Is(err, plugin.ErrPluginUnavailable),
		errors.Is(err, plugin.ErrPluginRequestFailed), errors.Is(err, plugin.ErrPluginProtocolMismatch),
		errors.Is(err, plugin.ErrPluginNotRunning), errors.Is(err, plugin.ErrPluginNotInstalled):
		status = http.StatusBadGateway
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	writeError(w, status, message)
}
