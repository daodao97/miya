package nsui

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	phoneproxy "miya/internal/phone/proxy"
	webservice "miya/internal/runtime/web"
	svcproto "miya/internal/service/protocol"
)

// Only localhost URLs change meaning when the workspace is remote. Public
// websites keep loading normally in the client's WebView.
func remoteBrowserService(target string, service webservice.Service) (webservice.Service, *url.URL, bool, error) {
	u, err := url.Parse(target)
	if err != nil {
		return service, nil, false, err
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := strings.EqualFold(host, "localhost") || ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
	if !local && service.RuntimeToken == "" {
		return service, u, false, nil
	}
	if u.Scheme != "http" {
		return service, u, true, fmt.Errorf("%s", tr("Server previews currently support HTTP. Use an http:// address."))
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	service.Port, err = strconv.Atoi(port)
	if err != nil {
		return service, u, true, err
	}
	if _, err = webservice.ParsePort(port); err != nil {
		return service, u, true, err
	}
	if service.Host == "" {
		service.Host = "127.0.0.1"
		if ip != nil && ip.IsLoopback() {
			service.Host = host
		}
	}
	return service, u, true, nil
}

// Each navigation owns its proxy until another navigation or tab teardown.
// Cancellation also closes proxies created after the shell has gone away.
func (p *panel) navigateRemoteBrowser(s *surface, target string, service webservice.Service) {
	// Editing the path of a verified LAN service must retain its remote
	// identity; without the token that host would be treated as public.
	if service.RuntimeToken == "" && sameBrowserOrigin(target, s.webTarget) {
		service = s.webService
	}
	if s.webCancel != nil {
		s.webCancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.webCancel = cancel
	s.webProxyURL = ""
	service, u, proxy, err := remoteBrowserService(target, service)
	if err != nil {
		s.webErr.Set(err.Error())
		return
	}
	s.webErr.Set("")
	s.webTarget, s.webService = target, service
	s.page.Set(target)
	if !proxy {
		if s.loaded.GetUntracked() == target {
			s.web.Reload()
		} else {
			s.loaded.Set(target)
		}
		return
	}
	// Stop displaying the source of the retired proxy.
	s.loaded.Set("")
	cl := p.host.State().Remote
	if cl == nil || cl.Wire() == nil {
		s.webErr.Set(svcproto.ErrClosed.Error())
		return
	}
	go func() {
		proxy, err := phoneproxy.NewPhoneWebProxy(cl.Wire(), service.Port, service.Host, service.RuntimeToken)
		if proxy != nil && err == nil {
			context.AfterFunc(ctx, proxy.Close)
		}
		p.cx.Post(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				s.webErr.Set(err.Error())
				return
			}
			s.webErr.Set("")
			s.webProxyURL = proxy.URL
			rel := u.EscapedPath()
			if rel == "" {
				rel = "/"
			}
			if u.RawQuery != "" {
				rel += "?" + u.RawQuery
			}
			if u.Fragment != "" {
				rel += "#" + u.EscapedFragment()
			}
			s.loaded.Set(proxy.URL + "?path=" + url.QueryEscape(rel))
		})
	}()
}

// Translate the proxy origin back to the remote address for the address bar.
// Resubmitting it must never target the client's temporary proxy port.
func browserPageURL(s *surface, raw string) string {
	proxy, _ := url.Parse(s.webProxyURL)
	page, err := url.Parse(raw)
	target, _ := url.Parse(s.webTarget)
	if err != nil || proxy == nil || page == nil || target == nil || proxy.Host == "" || page.Host != proxy.Host {
		return raw
	}
	if page.Path == proxy.Path {
		return target.String()
	}
	page.Scheme, page.Host = target.Scheme, target.Host
	return page.String()
}

// sameBrowserOrigin compares the scheme, hostname and effective port; a
// runtime token must never follow an address-bar change to another origin.
func sameBrowserOrigin(a, b string) bool {
	first, e1 := url.Parse(a)
	second, e2 := url.Parse(b)
	if e1 != nil || e2 != nil || first.Host == "" || second.Host == "" {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if strings.EqualFold(u.Scheme, "http") {
			return "80"
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		return ""
	}
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Hostname(), second.Hostname()) && port(first) == port(second)
}
