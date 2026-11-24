package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"lan-copy/internal/plugin"
)

const (
	// pluginUIPrefix is the only public prefix for plugin browser interfaces.
	// The plugin ID keeps multiple plugins from sharing one route.
	pluginUIPrefix = "/plugin-app/"

	pluginUIResponseHeaderTimeout = 60 * time.Second
)

// newPluginProxyTransport keeps proxied pages independent from the short
// timeouts used for plugin API calls. Streaming responses disable buffering in
// the proxy; the transport only bounds how long an upstream may stay silent
// before sending headers.
func newPluginProxyTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = pluginUIResponseHeaderTimeout
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}

// pluginUIProxy serves /plugin-app/<id>/... from the plugin instance's own
// HTTP port. Every request resolves the instance again because loopback host
// ports change whenever the container restarts.
func (s *Server) pluginUIProxy(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, pluginUIPrefix)
	if remainder == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	id, rest, hasSlash := strings.Cut(remainder, "/")
	if id == "" {
		writePluginUIError(w, http.StatusNotFound, "插件地址不完整。")
		return
	}
	if !hasSlash {
		redirect := pluginUIPrefix + id + "/"
		if r.URL.RawQuery != "" {
			redirect += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, redirect, http.StatusPermanentRedirect)
		return
	}
	if !validPluginUIPath(rest) {
		writePluginUIError(w, http.StatusNotFound, "插件页面不存在。")
		return
	}

	target, err := s.resolvePluginUITarget(id)
	if err != nil {
		writePluginUITargetError(w, err)
		return
	}
	s.servePluginUI(w, r, id, target, rest)
}

func (s *Server) resolvePluginUITarget(id string) (plugin.ProxyTarget, error) {
	if s.pluginUITarget != nil {
		return s.pluginUITarget(id)
	}
	if s.plugins == nil {
		return plugin.ProxyTarget{}, plugin.ErrPluginNotInstalled
	}
	return s.plugins.ProxyTarget(id)
}

func writePluginUITargetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrPluginNoUI):
		writePluginUIError(w, http.StatusNotFound, "这个插件没有提供 Web 界面。")
	case errors.Is(err, plugin.ErrPluginNotInstalled):
		writePluginUIError(w, http.StatusNotFound, "插件未安装或已被卸载。")
	case errors.Is(err, plugin.ErrPluginNotRunning):
		writePluginUIError(w, http.StatusServiceUnavailable, "插件未运行，请先在插件管理页启动。")
	case errors.Is(err, plugin.ErrPluginUnhealthy):
		writePluginUIError(w, http.StatusServiceUnavailable, "插件状态不健康，请查看健康记录和日志。")
	default:
		log.Printf("插件界面目标解析失败: %v", err)
		writePluginUIError(w, http.StatusInternalServerError, "无法解析插件界面地址。")
	}
}

func (s *Server) servePluginUI(w http.ResponseWriter, r *http.Request, id string, target plugin.ProxyTarget, rest string) {
	base, err := url.Parse(strings.TrimSpace(target.BaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		log.Printf("插件界面上游地址无效: plugin=%s", id)
		writePluginUIError(w, http.StatusBadGateway, "插件界面地址无效。")
		return
	}
	prefix := pluginUIPrefix + id
	uiPath := target.UIPath
	if !strings.HasPrefix(uiPath, "/") {
		uiPath = "/" + uiPath
	}
	proxy := &httputil.ReverseProxy{
		Transport:     s.pluginProxyTransport,
		FlushInterval: -1,
		Director: func(out *http.Request) {
			out.URL.Scheme = base.Scheme
			out.URL.Host = base.Host
			out.URL.Path = joinPluginUIPath(uiPath, rest)
			out.URL.RawPath = ""
			out.Host = base.Host
			out.Header.Set("X-Forwarded-Host", r.Host)
			out.Header.Set("X-Forwarded-Proto", forwardedProto(r))
			out.Header.Set("X-Forwarded-Prefix", prefix)
		},
		ModifyResponse: func(response *http.Response) error {
			rewritePluginUILocation(response.Header, prefix, base.Host, r.Host)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
			if errors.Is(proxyErr, context.Canceled) {
				return
			}
			log.Printf("插件界面代理失败: plugin=%s error=%v", id, proxyErr)
			if rw.Header().Get("Content-Type") == "" {
				writePluginUIError(rw, http.StatusBadGateway, "插件暂时无法响应，请稍后重试。")
			}
		},
	}

	// SecurityHeaders applies Lan Copy's CSP and frame policy to every route.
	// A proxied plugin page owns its own policy; keep only the headers that do
	// not constrain the plugin's assets and scripts.
	w.Header().Del("Content-Security-Policy")
	w.Header().Del("X-Frame-Options")
	proxy.ServeHTTP(w, r)
}

// validPluginUIPath rejects traversal that may survive URL normalization.
// Clean paths are already handled by ServeMux; this is defense in depth before
// the remainder is joined onto the plugin's own UI path.
func validPluginUIPath(rest string) bool {
	for _, segment := range strings.Split(rest, "/") {
		switch segment {
		case ".", "..":
			return false
		}
	}
	return !strings.ContainsRune(rest, 0)
}

func joinPluginUIPath(base, rest string) string {
	if base == "" || base == "/" {
		if rest == "" {
			return "/"
		}
		return "/" + strings.TrimLeft(rest, "/")
	}
	base = strings.TrimRight(base, "/")
	if rest == "" {
		return base + "/"
	}
	return base + "/" + strings.TrimLeft(rest, "/")
}

func forwardedProto(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); value != "" {
		return value
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// rewritePluginUILocation prefixes redirects that the plugin emits for its own
// pages, so the browser stays on the proxy prefix. Redirects to unrelated hosts
// and relative redirects are left untouched. Absolute URLs that name the
// internal upstream or the public Lan Copy host are reduced to a path, which
// also avoids leaking the container address to the browser.
func rewritePluginUILocation(header http.Header, prefix, internalHost, publicHost string) {
	for _, name := range []string{"Location", "Content-Location"} {
		value := header.Get(name)
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || !strings.HasPrefix(parsed.Path, "/") {
			continue
		}
		if parsed.Host != "" {
			if !strings.EqualFold(parsed.Host, internalHost) && !strings.EqualFold(parsed.Host, publicHost) {
				continue
			}
			parsed.Scheme = ""
			parsed.Host = ""
		}
		if parsed.Path == prefix || strings.HasPrefix(parsed.Path, prefix+"/") {
			if parsed.Host == "" {
				header.Set(name, parsed.String())
			}
			continue
		}
		parsed.Path = prefix + parsed.Path
		header.Set(name, parsed.String())
	}
}

func writePluginUIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "5")
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>插件界面不可用</title></head>
<body><main style="max-width:36rem;margin:15vh auto;padding:0 1.5rem;font:16px/1.6 system-ui,sans-serif">
<h1 style="font-size:1.5rem">插件界面不可用</h1>
<p>%s</p>
<p><a href="/plugins">返回插件管理</a></p>
</main></body></html>`, message)
}
