package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Lifecycle event delivery is best effort: events live in memory, one FIFO
// queue per plugin, and a slow or stopped plugin never blocks a filesystem
// operation.
const (
	EventSchemaVersion = 1

	DefaultEventsEndpoint = "/events"
	MaxEventSubscriptions = 32
	MaxEventBytes         = 64 << 10
	EventQueueSize        = 128
	EventHistorySize      = 50
	EventDeliveryTimeout  = 5 * time.Second
	EventMaxAttempts      = 3
	EventCircuitFailures  = 5
	EventCircuitCooldown  = 60 * time.Second
	EventResponseBytes    = 4 << 10
	EventTokenEnv         = "LAN_COPY_EVENT_TOKEN"

	EventHeaderName      = "X-Lan-Copy-Event"
	EventHeaderID        = "X-Lan-Copy-Event-Id"
	EventHeaderTimestamp = "X-Lan-Copy-Timestamp"
	EventHeaderSignature = "X-Lan-Copy-Signature"
)

// Event is the JSON envelope delivered to a subscribed plugin.
type Event struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Data          json.RawMessage `json:"data"`

	body []byte
}

// EventStats are in-memory delivery counters for one plugin.
type EventStats struct {
	Queued          uint64     `json:"queued"`
	Delivered       uint64     `json:"delivered"`
	Failed          uint64     `json:"failed"`
	Dropped         uint64     `json:"dropped"`
	LastDeliveredAt *time.Time `json:"lastDeliveredAt,omitempty"`
	LastErrorAt     *time.Time `json:"lastErrorAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
}

// EventDelivery is one bounded in-memory delivery record.
type EventDelivery struct {
	EventID    string    `json:"eventId"`
	Type       string    `json:"type"`
	At         time.Time `json:"at"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	StatusCode int       `json:"statusCode,omitempty"`
	DurationMS int64     `json:"durationMs"`
	Error      string    `json:"error,omitempty"`
}

// EventEndpointView describes one plugin's subscription and recent deliveries.
type EventEndpointView struct {
	Endpoint      string          `json:"endpoint"`
	Subscriptions []string        `json:"subscriptions"`
	Stats         EventStats      `json:"stats"`
	Deliveries    []EventDelivery `json:"deliveries"`
}

// EventCatalogEntry documents one lifecycle event type for the UI and docs.
type EventCatalogEntry struct {
	Type        string          `json:"type"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
}

// Payload types published by the HTTP layer.
type EntryEventData struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	PreviousPath string `json:"previousPath,omitempty"`
	PreviousName string `json:"previousName,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Recursive    bool   `json:"recursive,omitempty"`
}

type UploadedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type EventCounts struct {
	Files       int `json:"files"`
	Directories int `json:"directories"`
}

type FileUploadedData struct {
	Destination string         `json:"destination"`
	Files       []UploadedFile `json:"files"`
	Directories []string       `json:"directories"`
	Counts      EventCounts    `json:"counts"`
	Truncated   bool           `json:"truncated"`
}

type TextEventData struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title,omitempty"`
	Size      int64      `json:"size"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type SavedTextFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type TextSavedData struct {
	ID    string          `json:"id"`
	Files []SavedTextFile `json:"files"`
}

type SystemPingData struct {
	RequestedBy string    `json:"requestedBy"`
	At          time.Time `json:"at"`
}

type eventCircuitState struct {
	failures  int
	openUntil time.Time
}

// validEventSubscription accepts an exact type, a domain wildcard such as
// folder.*, or the global wildcard *.
func validEventSubscription(value string) bool {
	if value == "*" {
		return true
	}
	if strings.HasSuffix(value, ".*") {
		return eventDomainPattern.MatchString(strings.TrimSuffix(value, ".*"))
	}
	return eventTypePattern.MatchString(value)
}

func validEventType(value string) bool {
	return eventTypePattern.MatchString(value)
}

func matchesEventSubscription(subscriptions []string, eventType string) bool {
	for _, subscription := range subscriptions {
		if subscription == "*" || subscription == eventType {
			return true
		}
		if strings.HasSuffix(subscription, ".*") {
			prefix := strings.TrimSuffix(subscription, "*")
			if strings.HasPrefix(eventType, prefix) {
				return true
			}
		}
	}
	return false
}

// Publish builds one event and enqueues it for every running, healthy plugin
// that subscribes to it. It never blocks and never returns an error to the
// caller; delivery problems are visible through EventEndpoint.
func (m *Manager) Publish(eventType string, data any) string {
	if m == nil || m.ctx == nil || !validEventType(eventType) {
		return ""
	}
	raw := json.RawMessage(`{}`)
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			m.logger.Printf("插件事件序列化失败: type=%s 错误=%v", eventType, err)
			return ""
		}
		raw = encoded
	}
	if len(raw) > MaxEventBytes {
		m.logger.Printf("插件事件数据过大，已跳过: type=%s bytes=%d", eventType, len(raw))
		return ""
	}
	id, err := randomID("evt-")
	if err != nil {
		return ""
	}
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            id,
		Type:          eventType,
		OccurredAt:    m.now().UTC(),
		Data:          raw,
	}
	body, err := json.Marshal(event)
	if err != nil || len(body) > MaxEventBytes {
		return ""
	}
	event.body = body

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return ""
	}
	targets := m.eventTargetsLocked(event)
	for _, pluginID := range targets {
		m.enqueueEventLocked(pluginID, event)
	}
	return id
}

func (m *Manager) eventTargetsLocked(event Event) []string {
	targets := make([]string, 0, 1)
	for _, instance := range m.state.Instances {
		image, ok := imageByID(m.state.Images, instance.ImageID)
		if !ok || !matchesEventSubscription(image.Manifest.Events, event.Type) {
			continue
		}
		if instance.Status != StatusRunning || instance.HealthStatus != HealthHealthy || instance.InternalAddress == "" {
			m.recordEventLocked(instance.PluginID, event, EventDelivery{Status: "dropped", Error: "插件未运行或健康检查未通过"})
			continue
		}
		targets = append(targets, instance.PluginID)
	}
	return targets
}

func (m *Manager) enqueueEventLocked(pluginID string, event Event) {
	queue := m.ensureEventQueueLocked(pluginID)
	stats := m.eventStatsLocked(pluginID)
	select {
	case queue <- event:
		stats.Queued++
	default:
		// Keep the newest event; dropping the oldest bounds memory and makes
		// the loss visible in the counters.
		select {
		case <-queue:
		default:
		}
		select {
		case queue <- event:
			stats.Queued++
		default:
		}
		stats.Dropped++
		m.logEventDropLocked(pluginID)
	}
}

func (m *Manager) ensureEventQueueLocked(pluginID string) chan Event {
	if queue, ok := m.eventQueues[pluginID]; ok {
		return queue
	}
	queue := make(chan Event, EventQueueSize)
	m.eventQueues[pluginID] = queue
	m.eventWG.Add(1)
	go m.eventWorker(pluginID, queue)
	return queue
}

func (m *Manager) eventWorker(pluginID string, queue chan Event) {
	defer m.eventWG.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case event, ok := <-queue:
			if !ok {
				return
			}
			m.deliverEvent(pluginID, event)
		}
	}
}

func (m *Manager) deliverEvent(pluginID string, event Event) {
	m.mu.Lock()
	instance, hasInstance := instanceForPlugin(m.state.Instances, pluginID)
	image, hasImage := Image{}, false
	if hasInstance {
		image, hasImage = imageByID(m.state.Images, instance.ImageID)
	}
	if hasInstance && hasImage && event.Type != "system.ping" && !matchesEventSubscription(image.Manifest.Events, event.Type) {
		// The image or subscription changed since the event was queued.
		m.recordEventLocked(pluginID, event, EventDelivery{Status: "dropped", Error: "插件未订阅该事件"})
		m.mu.Unlock()
		return
	}
	endpoint := DefaultEventsEndpoint
	if hasImage && image.Manifest.Endpoints.Events != "" {
		endpoint = image.Manifest.Endpoints.Events
	}
	token, _ := m.store.EventToken(instance.ID)
	address := instance.InternalAddress
	runnable := hasInstance && instance.Status == StatusRunning && instance.HealthStatus == HealthHealthy
	if !hasInstance {
		m.recordEventLocked(pluginID, event, EventDelivery{Status: "dropped", Error: "插件未安装"})
		m.mu.Unlock()
		return
	}
	circuit := m.eventCircuit[pluginID]
	if !circuit.openUntil.IsZero() && m.now().Before(circuit.openUntil) {
		m.recordEventLocked(pluginID, event, EventDelivery{Status: "dropped", Error: "投递熔断中"})
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	if !runnable || address == "" || token == "" || !hasImage {
		reason := "插件未运行或健康检查未通过"
		if token == "" {
			reason = "插件事件密钥未配置，请重新启动插件"
		}
		m.mu.Lock()
		m.recordEventLocked(pluginID, event, EventDelivery{Status: "dropped", Error: reason})
		m.mu.Unlock()
		return
	}
	if err := validateClientPath(endpoint); err != nil {
		m.mu.Lock()
		m.recordEventLocked(pluginID, event, EventDelivery{Status: "failed", Error: err.Error()})
		m.mu.Unlock()
		return
	}

	start := time.Now()
	attempts, statusCode, err := m.postEvent(pluginID, address, endpoint, token, event)
	duration := time.Since(start).Milliseconds()
	if err == nil {
		m.mu.Lock()
		m.eventCircuit[pluginID] = eventCircuitState{}
		m.recordEventLocked(pluginID, event, EventDelivery{
			Status: "delivered", Attempts: attempts, StatusCode: statusCode, DurationMS: duration,
		})
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	circuit = m.eventCircuit[pluginID]
	circuit.failures++
	open := false
	if circuit.failures >= EventCircuitFailures {
		circuit = eventCircuitState{openUntil: m.now().Add(EventCircuitCooldown)}
		open = true
	}
	m.eventCircuit[pluginID] = circuit
	m.recordEventLocked(pluginID, event, EventDelivery{
		Status: "failed", Attempts: attempts, StatusCode: statusCode,
		DurationMS: duration, Error: truncate(err.Error(), MaxHealthError),
	})
	m.mu.Unlock()
	if open {
		m.logger.Printf("插件事件投递连续失败，暂停 60 秒: plugin=%s 错误=%v", pluginID, err)
	}
}

func (m *Manager) postEvent(pluginID, address, endpoint, token string, event Event) (int, int, error) {
	timestamp := strconv.FormatInt(m.now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(event.body)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))

	var lastErr error
	statusCode := 0
	for attempt := 1; attempt <= EventMaxAttempts; attempt++ {
		if attempt > 1 {
			delay := time.Duration(attempt-1) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-m.ctx.Done():
				timer.Stop()
				return attempt - 1, statusCode, m.ctx.Err()
			case <-timer.C:
			}
		}
		ctx, cancel := context.WithTimeout(m.ctx, EventDeliveryTimeout)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(address, "/")+endpoint, strings.NewReader(string(event.body)))
		if err != nil {
			cancel()
			return attempt, statusCode, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "lan-copy-plugin-events/1")
		request.Header.Set(EventHeaderName, event.Type)
		request.Header.Set(EventHeaderID, event.ID)
		request.Header.Set(EventHeaderTimestamp, timestamp)
		request.Header.Set(EventHeaderSignature, signature)
		response, err := m.httpClient.Do(request)
		if err != nil {
			cancel()
			lastErr = classifyClientError(pluginID, err)
			if m.ctx.Err() != nil {
				return attempt, statusCode, lastErr
			}
			continue
		}
		statusCode = response.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, EventResponseBytes))
		_ = response.Body.Close()
		cancel()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return attempt, statusCode, nil
		}
		lastErr = fmt.Errorf("插件返回状态码 %d", response.StatusCode)
		if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusRequestTimeout {
			return attempt, statusCode, lastErr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("插件事件投递失败")
	}
	return EventMaxAttempts, statusCode, lastErr
}

func (m *Manager) eventStatsLocked(pluginID string) *EventStats {
	stats, ok := m.eventStats[pluginID]
	if !ok {
		stats = &EventStats{}
		m.eventStats[pluginID] = stats
	}
	return stats
}

func (m *Manager) recordEventLocked(pluginID string, event Event, delivery EventDelivery) {
	if _, ok := m.pluginLocked(pluginID); !ok {
		return
	}
	now := m.now()
	if delivery.At.IsZero() {
		delivery.At = now
	}
	delivery.EventID = event.ID
	delivery.Type = event.Type
	stats := m.eventStatsLocked(pluginID)
	switch delivery.Status {
	case "delivered":
		stats.Delivered++
		stats.LastDeliveredAt = &now
		stats.LastError = ""
	case "failed":
		stats.Failed++
		stats.LastErrorAt = &now
		stats.LastError = truncate(delivery.Error, MaxHealthError)
	case "dropped":
		stats.Dropped++
	}
	records := append(m.eventRecords[pluginID], delivery)
	if len(records) > EventHistorySize {
		records = append([]EventDelivery(nil), records[len(records)-EventHistorySize:]...)
	}
	m.eventRecords[pluginID] = records
}

func (m *Manager) logEventDropLocked(pluginID string) {
	now := m.now()
	if now.Sub(m.eventLastDropLog) < time.Minute {
		return
	}
	m.eventLastDropLog = now
	m.logger.Printf("插件事件队列已满，丢弃最旧事件: plugin=%s", pluginID)
}

// EventEndpoint returns one plugin's subscription, counters and recent
// deliveries. Records are in memory only and reset when Lan Copy restarts.
func (m *Manager) EventEndpoint(pluginID string) (EventEndpointView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pluginLocked(pluginID); !ok {
		return EventEndpointView{}, fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	view := EventEndpointView{
		Endpoint:   DefaultEventsEndpoint,
		Deliveries: []EventDelivery{},
		Stats:      EventStats{},
	}
	if instance, ok := instanceForPlugin(m.state.Instances, pluginID); ok {
		if image, found := imageByID(m.state.Images, instance.ImageID); found {
			if image.Manifest.Endpoints.Events != "" {
				view.Endpoint = image.Manifest.Endpoints.Events
			}
			view.Subscriptions = append([]string{}, image.Manifest.Events...)
		}
	} else if image, found := latestImageForPlugin(m.state.Images, pluginID); found {
		if image.Manifest.Endpoints.Events != "" {
			view.Endpoint = image.Manifest.Endpoints.Events
		}
		view.Subscriptions = append([]string{}, image.Manifest.Events...)
	}
	if stats, ok := m.eventStats[pluginID]; ok {
		view.Stats = *stats
	}
	view.Deliveries = append(view.Deliveries, m.eventRecords[pluginID]...)
	if view.Deliveries == nil {
		view.Deliveries = []EventDelivery{}
	}
	return view, nil
}

// SendTestEvent enqueues a system.ping directly, independent of subscriptions,
// so an administrator can verify endpoint reachability and signature checks.
func (m *Manager) SendTestEvent(pluginID string) (string, error) {
	m.mu.Lock()
	if _, ok := m.pluginLocked(pluginID); !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: plugin %s", ErrNotFound, pluginID)
	}
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: plugin %s", ErrPluginNotInstalled, pluginID)
	}
	if instance.Status != StatusRunning || instance.HealthStatus != HealthHealthy {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: plugin %s is %s", ErrPluginNotRunning, pluginID, instance.Status)
	}
	if instance.InternalAddress == "" {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: plugin %s has no event endpoint", ErrPluginUnavailable, pluginID)
	}
	now := m.now().UTC()
	data, err := json.Marshal(SystemPingData{RequestedBy: "plugins-page", At: now})
	if err != nil {
		m.mu.Unlock()
		return "", err
	}
	id, err := randomID("evt-")
	if err != nil {
		m.mu.Unlock()
		return "", err
	}
	event := Event{
		SchemaVersion: EventSchemaVersion, ID: id, Type: "system.ping",
		OccurredAt: now, Data: data,
	}
	event.body, err = json.Marshal(event)
	if err != nil {
		m.mu.Unlock()
		return "", err
	}
	if len(event.body) > MaxEventBytes {
		m.mu.Unlock()
		return "", fmt.Errorf("%w: event payload too large", ErrInvalidConfig)
	}
	m.enqueueEventLocked(pluginID, event)
	m.mu.Unlock()
	return id, nil
}

// EventCatalog is the single source for the lifecycle menu and API.
func EventCatalog() []EventCatalogEntry {
	entries := []struct {
		Type        string
		Category    string
		Description string
		Data        string
	}{
		{"file.uploaded", "文件", "上传事务提交成功后发送，单文件、多文件和文件夹上传统一为该事件。", `{"destination":"docs","files":[{"path":"docs/a.pdf","size":182344}],"directories":["docs/sub"],"counts":{"files":1,"directories":1},"truncated":false}`},
		{"file.deleted", "文件", "删除共享文件成功后发送。", `{"path":"docs/a.pdf","name":"a.pdf","size":182344}`},
		{"file.renamed", "文件", "文件原地重命名成功后发送。", `{"path":"docs/b.pdf","name":"b.pdf","previousPath":"docs/a.pdf","previousName":"a.pdf"}`},
		{"file.moved", "文件", "文件移动到其他文件夹成功后发送。", `{"path":"archive/b.pdf","name":"b.pdf","previousPath":"docs/b.pdf","previousName":"b.pdf"}`},
		{"folder.created", "文件夹", "新建文件夹成功后发送。", `{"path":"docs/2026","name":"2026"}`},
		{"folder.deleted", "文件夹", "删除文件夹成功后发送；recursive 表示是否递归删除。", `{"path":"docs/2026","name":"2026","recursive":true}`},
		{"folder.renamed", "文件夹", "文件夹原地重命名成功后发送。", `{"path":"docs/new","name":"new","previousPath":"docs/old","previousName":"old"}`},
		{"folder.moved", "文件夹", "文件夹移动成功后发送。", `{"path":"archive/old","name":"old","previousPath":"docs/old","previousName":"old"}`},
		{"text.created", "文本", "新建文本或图文碎片成功后发送。", `{"id":"6f1c...","kind":"text","title":"便签","size":1024,"expiresAt":"2026-11-25T03:04:05Z"}`},
		{"text.deleted", "文本", "删除文本或图文碎片成功后发送。", `{"id":"6f1c...","kind":"text","title":"便签","size":1024}`},
		{"text.saved", "文本", "把碎片转存为共享文件成功后发送。", `{"id":"6f1c...","files":[{"path":"docs/便签.txt","name":"便签.txt"}]}`},
		{"system.ping", "调试", "仅由插件页面的“发送测试事件”触发，不参与订阅匹配。", `{"requestedBy":"plugins-page","at":"2026-11-18T03:04:05Z"}`},
	}
	catalog := make([]EventCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		catalog = append(catalog, EventCatalogEntry{
			Type: entry.Type, Category: entry.Category, Description: entry.Description,
			Data: json.RawMessage(entry.Data),
		})
	}
	return catalog
}
