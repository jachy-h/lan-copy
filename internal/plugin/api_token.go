package plugin

import (
	"crypto/hmac"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	APIBaseURLEnv = "LAN_COPY_API_URL"
	APITokenEnv   = "LAN_COPY_API_TOKEN"
)

// HostAddress is one IPv4 address that a plugin container may use to reach the
// host. Interface is kept for logging and deterministic tie-breaking.
type HostAddress struct {
	Interface string
	IP        string
}

// APIAddress is the injected plugin API address together with a human-readable
// explanation. The reason is logged and shown read-only on the plugin page.
type APIAddress struct {
	URL    string
	Reason string
}

// pluginExtraHosts allows the plugin container to reach the host API in
// loopback mode without exposing arbitrary DNS or host mappings. Docker uses
// host.docker.internal and Linux Podman uses host.containers.internal. Podman
// machine (Windows/macOS) points host.containers.internal at the VM rather
// than the host, so it gets no mapping and uses a LAN address instead.
func pluginExtraHosts(networkMode, runtimeType, hostOS string) []string {
	if networkMode != "loopback" {
		return nil
	}
	if runtimeType != "podman" {
		return []string{"host.docker.internal:host-gateway"}
	}
	if hostOS == "linux" {
		return []string{"host.containers.internal:host-gateway"}
	}
	return nil
}

// DefaultAPIBaseURL is the address injected into plugin containers when the
// operator did not pass -plugin-api-url. It also returns why that address was
// chosen so the decision can be logged and displayed.
//
// hostOS is the operating system running Lan Copy, not the container engine's
// guest OS. Podman on Windows/macOS always runs inside a machine VM, so
// host.containers.internal is not host-reachable there.
func DefaultAPIBaseURL(networkMode, port, runtimeType, hostOS string, addresses []HostAddress) APIAddress {
	if port == "" {
		return APIAddress{}
	}
	if networkMode == "container" {
		return APIAddress{
			URL:    "http://lan-copy:" + port,
			Reason: "container 模式使用插件网络内服务名 lan-copy",
		}
	}
	if runtimeType != "podman" {
		return APIAddress{
			URL:    "http://host.docker.internal:" + port,
			Reason: "Docker 使用 host.docker.internal",
		}
	}
	if hostOS == "linux" {
		return APIAddress{
			URL:    "http://host.containers.internal:" + port,
			Reason: "原生 Podman（Linux）使用 host.containers.internal",
		}
	}
	if address, ok := selectHostAddress(addresses); ok {
		return APIAddress{
			URL: "http://" + net.JoinHostPort(address.IP, port),
			Reason: fmt.Sprintf(
				"Podman machine（%s）中 host.containers.internal 指向虚拟机，改用宿主网卡 %s 的局域网 IPv4 %s",
				hostOS, address.Interface, address.IP,
			),
		}
	}
	return APIAddress{
		Reason: fmt.Sprintf(
			"Podman machine（%s）未找到可用局域网 IPv4，未注入 LAN_COPY_API_URL；请用 -plugin-api-url 指定宿主机可达地址",
			hostOS,
		),
	}
}

// collectHostAddresses returns candidate host IPv4 addresses for Podman
// machine. Loopback, link-local and virtual adapter addresses are skipped so
// the injected URL points at the physical LAN interface in normal setups.
func collectHostAddresses() []HostAddress {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	addresses := make([]HostAddress, 0)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		if isVirtualInterfaceName(iface.Name) {
			continue
		}
		interfaceAddresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range interfaceAddresses {
			ip := interfaceIP(address)
			if ip == nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil && eligibleHostIPv4(ip4) {
				addresses = append(addresses, HostAddress{Interface: iface.Name, IP: ip4.String()})
			}
		}
	}
	return addresses
}

func interfaceIP(address net.Addr) net.IP {
	switch value := address.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}

func eligibleHostIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4.IsLoopback() || ip4.IsUnspecified() || ip4.IsMulticast() ||
		ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() {
		return false
	}
	// 100.64.0.0/10 is carrier-grade NAT space commonly used by VPN overlays.
	if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false
	}
	return true
}

func isVirtualInterfaceName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}
	if normalized == "lo" || strings.HasPrefix(normalized, "lo0") {
		return true
	}
	if strings.HasPrefix(normalized, "br") &&
		(len(normalized) == 2 || normalized[2] == '-' || (normalized[2] >= '0' && normalized[2] <= '9') || strings.HasPrefix(normalized, "bridge")) {
		return true
	}
	prefixes := []string{
		"docker", "veth", "virbr", "vmnet", "vboxnet", "vethernet",
		"hyper-v", "wsl", "utun", "tun", "tap", "tailscale", "wg", "zt",
		"bridge", "awdl", "llw", "anpi",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

// selectHostAddress applies a stable preference order: private 192.168/16,
// then 10/8, then 172.16/12, then other IPv4; ties break on the numeric IP and
// finally the interface name.
func selectHostAddress(addresses []HostAddress) (HostAddress, bool) {
	type candidate struct {
		address HostAddress
		ip      net.IP
	}
	candidates := make([]candidate, 0, len(addresses))
	for _, address := range addresses {
		if isVirtualInterfaceName(address.Interface) {
			continue
		}
		ip := net.ParseIP(strings.TrimSpace(address.IP)).To4()
		if ip == nil || !eligibleHostIPv4(ip) {
			continue
		}
		candidates = append(candidates, candidate{
			address: HostAddress{Interface: address.Interface, IP: ip.String()},
			ip:      ip,
		})
	}
	if len(candidates) == 0 {
		return HostAddress{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		leftRank, rightRank := hostAddressRank(left.ip), hostAddressRank(right.ip)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		for index := range left.ip {
			if left.ip[index] != right.ip[index] {
				return left.ip[index] < right.ip[index]
			}
		}
		return left.address.Interface < right.address.Interface
	})
	return candidates[0].address, true
}

func hostAddressRank(ip net.IP) int {
	ip4 := ip.To4()
	if ip4 == nil {
		return 3
	}
	switch {
	case ip4[0] == 192 && ip4[1] == 168:
		return 0
	case ip4[0] == 10:
		return 1
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return 2
	default:
		return 3
	}
}

// AuthenticateAPIToken returns the plugin ID that owns a valid table API token.
// Stopped instances are rejected; running, starting and unhealthy instances can
// call the API while their container is alive.
func (m *Manager) AuthenticateAPIToken(token string) (string, bool) {
	if m == nil || token == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, instance := range m.state.Instances {
		switch instance.Status {
		case StatusRunning, StatusStarting, StatusUnhealthy:
		default:
			continue
		}
		expected, err := m.store.APIToken(instance.ID)
		if err != nil || expected == "" {
			continue
		}
		if hmac.Equal([]byte(expected), []byte(token)) {
			return instance.PluginID, true
		}
	}
	return "", false
}
