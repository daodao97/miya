package runtime

import "net"

// LocalHost accepts numeric addresses assigned to this computer, never an
// arbitrary peer on the LAN or a hostname requiring DNS resolution.
func LocalHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if host == "127.0.0.1" || host == "::1" {
		return true
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		local, _, err := net.ParseCIDR(address.String())
		if err == nil && local.Equal(ip) {
			return true
		}
	}
	return false
}
