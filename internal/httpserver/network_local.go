package httpserver

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

func LocalURLs(port string) []string {
	if host, _, err := net.SplitHostPort(port); err == nil {
		port = strings.TrimPrefix(port, host+":")
	}
	addresses, _ := net.InterfaceAddrs()
	urls := make([]string, 0)
	for _, address := range addresses {
		ipNet, ok := address.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ipNet.IP.String(), port))
	}
	SortLocalURLs(urls)
	return urls
}

func SortLocalURLs(urls []string) {
	sort.Slice(urls, func(i, j int) bool {
		leftPreferred := strings.HasPrefix(urls[i], "http://192.")
		rightPreferred := strings.HasPrefix(urls[j], "http://192.")
		if leftPreferred != rightPreferred {
			return leftPreferred
		}
		return urls[i] < urls[j]
	})
}

func ReachableLocalURLs(ctx context.Context, urls []string) []string {
	const probeTimeout = 1200 * time.Millisecond
	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: probeTimeout}
	reachable := make([]bool, len(urls))
	var probes sync.WaitGroup
	for index, address := range urls {
		probes.Add(1)
		go func(resultIndex int, baseURL string) {
			defer probes.Done()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
			if err != nil {
				return
			}
			response, err := client.Do(request)
			if err != nil {
				return
			}
			_ = response.Body.Close()
			reachable[resultIndex] = response.StatusCode == http.StatusOK
		}(index, address)
	}
	probes.Wait()
	transport.CloseIdleConnections()
	result := make([]string, 0, len(urls))
	for index, address := range urls {
		if reachable[index] {
			result = append(result, address)
		}
	}
	return result
}
