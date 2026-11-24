package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Upload hooks are synchronous post-upload calls. They are intentionally
// separate from lifecycle events: the uploader's browser waits for the page
// directive, while the upload itself must still succeed when the hook fails.
const (
	DefaultUploadHookTimeoutSeconds = 5
	MinUploadHookTimeoutSeconds     = 1
	MaxUploadHookTimeoutSeconds     = 30

	UploadHookResponseBytes   = 64 << 10
	MaxUploadHookDialogs      = 5
	MaxUploadHookDialogsTotal = 10

	UploadHookHeaderName      = "X-Lan-Copy-Hook"
	UploadHookHeaderID        = "X-Lan-Copy-Hook-Id"
	UploadHookHeaderTimestamp = "X-Lan-Copy-Timestamp"
	UploadHookHeaderSignature = "X-Lan-Copy-Signature"
)

// UploadHookDialog is one validated page directive returned by a plugin.
// Path stays relative to the plugin's own UI mount; the HTTP layer prefixes it
// with /plugin-app/<id> so a plugin can never point the browser at another
// origin.
type UploadHookDialog struct {
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Title      string `json:"title"`
	Path       string `json:"path"`
}

type UploadHookResponse struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Dialogs       []UploadHookDialogPayload `json:"dialogs,omitempty"`
}

type UploadHookDialogPayload struct {
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
}

type uploadHookTarget struct {
	pluginID   string
	pluginName string
	hookPath   string
	token      string
	timeout    time.Duration
}

type uploadHookResult struct {
	pluginID   string
	pluginName string
	dialogs    []UploadHookDialog
	duration   time.Duration
	err        error
}

// RunUploadHooks calls every running, healthy plugin that declares an upload
// hook. Calls run concurrently and failures only affect the returned list; the
// caller keeps the upload successful regardless of hook outcomes.
func (m *Manager) RunUploadHooks(ctx context.Context, data FileUploadedData) []UploadHookDialog {
	if m == nil || len(data.Files) == 0 {
		return nil
	}
	targets := m.uploadHookTargets()
	if len(targets) == 0 {
		return nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		m.logger.Printf("插件上传 Hook 数据序列化失败: %v", err)
		return nil
	}
	if len(raw) > MaxEventBytes {
		m.logger.Printf("插件上传 Hook 数据过大，已跳过: bytes=%d", len(raw))
		return nil
	}
	id, err := randomID("hook-")
	if err != nil {
		return nil
	}
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            id,
		Type:          "file.uploaded",
		OccurredAt:    m.now().UTC(),
		Data:          raw,
	}
	body, err := json.Marshal(event)
	if err != nil || len(body) > MaxEventBytes {
		return nil
	}

	results := make(chan uploadHookResult, len(targets))
	var wait sync.WaitGroup
	for _, target := range targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- m.callUploadHook(ctx, target, body)
		}()
	}
	wait.Wait()
	close(results)

	collected := make([]uploadHookResult, 0, len(targets))
	for result := range results {
		collected = append(collected, result)
	}
	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].pluginID < collected[j].pluginID
	})

	dialogs := make([]UploadHookDialog, 0)
	for _, result := range collected {
		if result.err != nil {
			if ctx.Err() == nil {
				m.logger.Printf("插件上传 Hook 失败: plugin=%s duration=%s error=%v",
					result.pluginID, result.duration.Round(time.Millisecond), result.err)
			}
			continue
		}
		if len(dialogs)+len(result.dialogs) > MaxUploadHookDialogsTotal {
			remaining := MaxUploadHookDialogsTotal - len(dialogs)
			if remaining < 0 {
				remaining = 0
			}
			dialogs = append(dialogs, result.dialogs[:remaining]...)
			m.logger.Printf("插件上传 Hook 弹窗超过总上限，已截断: plugin=%s", result.pluginID)
			break
		}
		dialogs = append(dialogs, result.dialogs...)
	}
	return dialogs
}

func (m *Manager) uploadHookTargets() []uploadHookTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	targets := make([]uploadHookTarget, 0, 1)
	for _, instance := range m.state.Instances {
		if instance.Status != StatusRunning || instance.HealthStatus != HealthHealthy || instance.InternalAddress == "" {
			continue
		}
		image, ok := imageByID(m.state.Images, instance.ImageID)
		if !ok || image.Manifest.UploadHook == nil || image.Manifest.UI == nil {
			continue
		}
		token, err := m.store.EventToken(instance.ID)
		if err != nil || token == "" {
			continue
		}
		timeout := time.Duration(image.Manifest.UploadHook.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = DefaultUploadHookTimeoutSeconds * time.Second
		}
		targets = append(targets, uploadHookTarget{
			pluginID: instance.PluginID, pluginName: image.Manifest.Name,
			hookPath: image.Manifest.UploadHook.Path, token: token, timeout: timeout,
		})
	}
	return targets
}

func (m *Manager) callUploadHook(ctx context.Context, target uploadHookTarget, body []byte) uploadHookResult {
	started := m.now()
	result := uploadHookResult{pluginID: target.pluginID, pluginName: target.pluginName}
	timestamp := strconv.FormatInt(m.now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(target.token))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set(UploadHookHeaderName, "file.uploaded")
	headers.Set(UploadHookHeaderID, uploadHookID(body))
	headers.Set(UploadHookHeaderTimestamp, timestamp)
	headers.Set(UploadHookHeaderSignature, signature)

	var response UploadHookResponse
	_, err := m.Client().Do(ctx, target.pluginID, Request{
		Method:   http.MethodPost,
		Path:     target.hookPath,
		Header:   headers,
		Body:     bytes.NewReader(body),
		Timeout:  target.timeout,
		MaxBytes: UploadHookResponseBytes,
	}, &response)
	result.duration = m.now().Sub(started)
	if err != nil {
		result.err = err
		return result
	}
	if len(response.Dialogs) == 0 {
		return result
	}
	if response.SchemaVersion != 1 {
		result.err = fmt.Errorf("%w: upload hook schemaVersion %d", ErrPluginRequestFailed, response.SchemaVersion)
		return result
	}
	seen := make(map[string]struct{}, len(response.Dialogs))
	for _, item := range response.Dialogs {
		if len(result.dialogs) >= MaxUploadHookDialogs {
			m.logger.Printf("插件上传 Hook 弹窗超过单插件上限，已截断: plugin=%s", target.pluginID)
			break
		}
		path, ok := validateUploadHookPagePath(item.Path)
		if !ok {
			m.logger.Printf("插件上传 Hook 返回了非法页面路径，已忽略: plugin=%s path=%q", target.pluginID, item.Path)
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		title := sanitizeUploadHookTitle(item.Title, target.pluginName)
		result.dialogs = append(result.dialogs, UploadHookDialog{
			PluginID: target.pluginID, PluginName: target.pluginName,
			Title: title, Path: path,
		})
	}
	return result
}

// uploadHookID reuses the envelope ID when it can be decoded cheaply. The ID
// is only a request identifier; a fallback keeps malformed bodies from
// producing an empty header.
func uploadHookID(body []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		return envelope.ID
	}
	return ""
}

func sanitizeUploadHookTitle(value, pluginName string) string {
	value = strings.TrimSpace(value)
	cleaned := make([]rune, 0, len(value))
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		cleaned = append(cleaned, r)
	}
	title := strings.TrimSpace(string(cleaned))
	if title == "" {
		title = strings.TrimSpace(pluginName)
	}
	if title == "" {
		title = "插件处理"
	}
	runes := []rune(title)
	if len(runes) > 100 {
		title = string(runes[:100])
	}
	return title
}

// validateUploadHookPagePath accepts only a path/query relative to the
// plugin's own UI. External URLs, fragments, encoded traversal and the
// /plugin-app prefix itself are rejected so the browser always stays inside
// this plugin's proxy route.
func validateUploadHookPagePath(raw string) (string, bool) {
	if raw == "" || len(raw) > 1024 {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Fragment != "" {
		return "", false
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.Contains(parsed.Path, "\\") || strings.ContainsRune(parsed.Path, 0) {
		return "", false
	}
	if parsed.Path == "/plugin-app" || strings.HasPrefix(parsed.Path, "/plugin-app/") {
		return "", false
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	return raw, true
}
