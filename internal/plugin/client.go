package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the single entry point for calling plugin HTTP services. Business
// code resolves a plugin by ID and never constructs container addresses.
type Client struct {
	manager *Manager
}

func (m *Manager) Client() *Client { return &Client{manager: m} }

func (c *Client) GetJSON(ctx context.Context, pluginID, path string, target any) error {
	_, err := c.Do(ctx, pluginID, Request{Method: http.MethodGet, Path: path, JSON: nil}, target)
	return err
}

func (c *Client) PostJSON(ctx context.Context, pluginID, path string, input, target any) error {
	_, err := c.Do(ctx, pluginID, Request{Method: http.MethodPost, Path: path, JSON: input}, target)
	return err
}

// Do performs one bounded request. Non-2xx responses are returned together
// with ErrPluginRequestFailed so callers can inspect a small error body.
func (c *Client) Do(ctx context.Context, pluginID string, req Request, target any) (*Response, error) {
	base, config, err := c.manager.clientTarget(pluginID)
	if err != nil {
		c.manager.addMetric(pluginID, 0, true)
		return nil, err
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	if err := validateClientPath(req.Path); err != nil {
		c.manager.addMetric(pluginID, 0, true)
		return nil, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = time.Duration(config.RequestTimeoutSeconds) * time.Second
	}
	if timeout <= 0 || timeout > 300*time.Second {
		timeout = DefaultRequestTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := strings.TrimRight(base, "/") + req.Path
	if len(req.Query) > 0 {
		values := url.Values(req.Query)
		endpoint += "?" + values.Encode()
	}
	var body io.Reader = req.Body
	contentType := ""
	if req.Multipart != nil {
		stream, mediaType, err := streamMultipart(requestCtx, req.Multipart)
		if err != nil {
			c.manager.addMetric(pluginID, 0, true)
			return nil, err
		}
		body = stream
		contentType = mediaType
	} else if req.JSON != nil {
		payload, err := json.Marshal(req.JSON)
		if err != nil {
			c.manager.addMetric(pluginID, 0, true)
			return nil, err
		}
		body = bytes.NewReader(payload)
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(requestCtx, req.Method, endpoint, body)
	if err != nil {
		c.manager.addMetric(pluginID, 0, true)
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	for name, values := range req.Header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}

	start := time.Now()
	response, err := c.manager.httpClient.Do(request)
	duration := time.Since(start)
	if err != nil {
		c.manager.addMetric(pluginID, duration, true)
		return nil, classifyClientError(pluginID, err)
	}
	defer response.Body.Close()
	limit := req.MaxBytes
	if limit <= 0 {
		limit = DefaultResponseBytes
	}
	if limit > MaxDownloadBytes {
		limit = MaxDownloadBytes
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if readErr != nil {
		c.manager.addMetric(pluginID, duration, true)
		return nil, classifyClientError(pluginID, readErr)
	}
	if int64(len(data)) > limit {
		c.manager.addMetric(pluginID, duration, true)
		return nil, fmt.Errorf("%w: plugin %s response exceeded %d bytes", ErrPluginResponseTooLarge, pluginID, limit)
	}
	result := &Response{Status: response.StatusCode, Header: response.Header, Body: data}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		c.manager.addMetric(pluginID, duration, true)
		return result, &PluginRequestError{
			PluginID: pluginID, Status: response.StatusCode,
			Message: responseSnippet(data),
		}
	}
	if target != nil && len(data) > 0 {
		if err := json.Unmarshal(data, target); err != nil {
			c.manager.addMetric(pluginID, duration, true)
			return result, fmt.Errorf("%w: decode plugin %s response: %v", ErrPluginRequestFailed, pluginID, err)
		}
	}
	c.manager.addMetric(pluginID, duration, false)
	return result, nil
}

// Download streams a plugin file with a hard size limit. The caller must close
// the returned reader.
func (c *Client) Download(ctx context.Context, pluginID, path string) (io.ReadCloser, error) {
	base, config, err := c.manager.clientTarget(pluginID)
	if err != nil {
		return nil, err
	}
	if err := validateClientPath(path); err != nil {
		return nil, err
	}
	timeout := time.Duration(config.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 300*time.Second {
		timeout = DefaultRequestTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	start := time.Now()
	response, err := c.manager.httpClient.Do(request)
	if err != nil {
		cancel()
		c.manager.addMetric(pluginID, time.Since(start), true)
		return nil, classifyClientError(pluginID, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		cancel()
		c.manager.addMetric(pluginID, time.Since(start), true)
		return nil, &PluginRequestError{PluginID: pluginID, Status: response.StatusCode, Message: responseSnippet(snippet)}
	}
	c.manager.addMetric(pluginID, time.Since(start), false)
	return &limitedReadCloser{
		reader: io.LimitReader(response.Body, MaxDownloadBytes+1),
		closer: response.Body,
		cancel: cancel,
		limit:  MaxDownloadBytes,
		read:   0,
	}, nil
}

func (m *Manager) clientTarget(pluginID string) (string, Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pluginLocked(pluginID); !ok {
		return "", Config{}, fmt.Errorf("%w: plugin %s", ErrPluginNotInstalled, pluginID)
	}
	instance, ok := instanceForPlugin(m.state.Instances, pluginID)
	if !ok {
		return "", Config{}, fmt.Errorf("%w: plugin %s", ErrPluginNotInstalled, pluginID)
	}
	switch instance.Status {
	case StatusRunning:
	case StatusUnhealthy:
		return "", Config{}, fmt.Errorf("%w: plugin %s", ErrPluginUnhealthy, pluginID)
	default:
		return "", Config{}, fmt.Errorf("%w: plugin %s is %s", ErrPluginNotRunning, pluginID, instance.Status)
	}
	if instance.InternalAddress == "" {
		return "", Config{}, fmt.Errorf("%w: plugin %s has no address", ErrPluginUnavailable, pluginID)
	}
	return instance.InternalAddress, instance.Config.Clone(), nil
}

func validateClientPath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%w: plugin path must start with /", ErrPluginRequestFailed)
	}
	if strings.Contains(path, "://") || strings.Contains(path, "..") || strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("%w: invalid plugin path", ErrPluginRequestFailed)
	}
	return nil
}

func streamMultipart(ctx context.Context, request *MultipartRequest) (io.Reader, string, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	go func() {
		err := writeMultipart(ctx, multipartWriter, request)
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
	}()
	return reader, contentType, nil
}

func writeMultipart(ctx context.Context, writer *multipart.Writer, request *MultipartRequest) error {
	for _, field := range request.Fields {
		if err := writer.WriteField(field.Name, field.Value); err != nil {
			return err
		}
	}
	for _, file := range request.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		part, err := writer.CreateFormFile(file.Field, file.Filename)
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, file.Reader); err != nil {
			return err
		}
	}
	return nil
}

func classifyClientError(pluginID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: plugin %s", ErrPluginTimeout, pluginID)
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return fmt.Errorf("%w: plugin %s", ErrPluginTimeout, pluginID)
	}
	return fmt.Errorf("%w: plugin %s: %v", ErrPluginUnavailable, pluginID, err)
}

func responseSnippet(body []byte) string {
	const limit = 512
	if len(body) > limit {
		body = body[:limit]
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(body), ""))
}

// PluginRequestError classifies a non-2xx plugin response.
type PluginRequestError struct {
	PluginID string
	Status   int
	Message  string
}

func (e *PluginRequestError) Error() string {
	return fmt.Sprintf("%v: plugin %s returned %d: %s", ErrPluginRequestFailed, e.PluginID, e.Status, e.Message)
}

func (e *PluginRequestError) Is(target error) bool { return target == ErrPluginRequestFailed }

func (e *PluginRequestError) Unwrap() error { return ErrPluginRequestFailed }

type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
	cancel context.CancelFunc
	limit  int64
	read   int64
}

func (l *limitedReadCloser) Read(buffer []byte) (int, error) {
	count, err := l.reader.Read(buffer)
	l.read += int64(count)
	if l.read > l.limit {
		return count, fmt.Errorf("%w: download exceeded %d bytes", ErrPluginResponseTooLarge, l.limit)
	}
	return count, err
}

func (l *limitedReadCloser) Close() error {
	l.cancel()
	return l.closer.Close()
}
