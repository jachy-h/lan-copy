package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func instanceURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" || port == "0" {
		return "", fmt.Errorf("invalid existing-instance address: %s", listen)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}).String(), nil
}

func probeRunningInstance(ctx context.Context, client *http.Client, address string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/healthz", nil)
	if err != nil {
		return false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Lan-Copy-Service") != "lan-copy" {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64))
	return err == nil && strings.TrimSpace(string(body)) == "ok", err
}

// Retry briefly while a concurrent first launch is between Listen and Serve.
// Probes never use system proxies or follow redirects to another service.
func openRunningInstance(listen string, launch func(string) error) (bool, error) {
	address, err := instanceURL(listen)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 300 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		found, err := probeRunningInstance(ctx, client, address)
		if found {
			return true, launch(address)
		}
		if err == nil {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}
