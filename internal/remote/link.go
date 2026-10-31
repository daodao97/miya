// Package remote lets a phone watch Miya's conversations over Tailcat's
// end-to-end encrypted tunnel, as Retty's phone watches its terminals. The
// desktop serves its state; the phone shows it, sends messages and answers
// approvals, but never changes the desktop's settings.
package remote

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/tailscale/tailcat"
)

// Port is the desktop's listening port inside its own Tailcat overlay.
const Port = 4243

func quiet(string, ...any) {}

// Link keeps the case-sensitive Tailcat capability in a URL query, never a
// hostname. Whoever holds it can read every conversation the desktop shows.
func Link(addr tailcat.Addr) string {
	return "miya://connect?" + url.Values{"v": {"1"}, "address": {string(addr)}}.Encode()
}

// ParseLink accepts a full miya://connect link or the bare Tailcat address.
func ParseLink(raw string) (tailcat.Addr, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 4096 {
		return "", errors.New("连接码过长")
	}
	addr := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "miya" || u.Host != "connect" || u.Path != "" || u.Fragment != "" || u.User != nil {
			return "", errors.New("请扫描 Miya 桌面端的连接二维码")
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || q.Get("v") != "1" || len(q["address"]) != 1 {
			return "", errors.New("不支持的连接码版本")
		}
		addr = q.Get("address")
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(addr))
	if err != nil || ci.PresharedKey.IsZero() {
		return "", errors.New("无效的 Miya 连接码")
	}
	return tailcat.Addr(addr), nil
}

// RelayURL is the first public relay of the link's region, the server a
// tunnel reaches first. iOS prepares its network access against it.
func RelayURL(raw string) string {
	addr, err := ParseLink(raw)
	if err != nil {
		return tailcat.DefaultDERPMapURL
	}
	ci, err := tailcat.ParseAddr(addr)
	if err != nil {
		return tailcat.DefaultDERPMapURL
	}
	for _, region := range ci.Region {
		for _, node := range region.Nodes {
			if node.STUNOnly || node.HostName == "" || node.CertName != "" || node.InsecureForTests {
				continue
			}
			return (&url.URL{Scheme: "https", Host: node.HostName, Path: "/"}).String()
		}
	}
	return tailcat.DefaultDERPMapURL
}

// iOS networks may use DNS64 or a domain-based packet tunnel. Dialing the
// relay's baked-in IPs bypasses those routes; keep its TLS hostname and let
// the device resolver choose the reachable address family, as Retty does.
func hostnameRelayAddr(addr tailcat.Addr) tailcat.Addr {
	ci, err := tailcat.ParseAddr(addr)
	if err != nil {
		return addr
	}
	for _, region := range ci.Region {
		for _, node := range region.Nodes {
			if node.HostName == "" {
				continue
			}
			if ip, err := netip.ParseAddr(node.IPv4); err == nil && ip.Is4() {
				node.IPv4 = ""
			}
			if ip, err := netip.ParseAddr(node.IPv6); err == nil && ip.Is6() {
				node.IPv6 = ""
			}
		}
	}
	return ci.Addr()
}
