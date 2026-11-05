package main

import (
	"fmt"
	"miya/internal/webservice"
	"net"
	"net/url"
	"strconv"
	"strings"
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
		return service, u, true, fmt.Errorf("Server previews currently support HTTP. Use an http:// address.")
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

func (app *chatApp) navigateRemoteBrowser(s *panelSurface, target string, service webservice.Service) {
	s.webGeneration++
	gen := s.webGeneration
	if s.webProxy != nil {
		s.webProxy.Close()
		s.webProxy = nil
	}
	service, u, proxy, err := remoteBrowserService(target, service)
	if err != nil {
		s.webErr = err.Error()
		return
	}
	s.webErr = ""
	if !proxy {
		if err := s.web.Page().LoadURL(target); err != nil {
			s.webErr = err.Error()
		}
		return
	}
	cl := app.c.remote
	if cl == nil || cl.Wire() == nil {
		s.webErr = errServiceClosed.Error()
		return
	}
	go func() {
		p, err := newPhoneWebProxy(cl.Wire(), service.Port, service.Host, service.RuntimeToken)
		app.c.dispatch(func() {
			if s.closed || s.webGeneration != gen {
				if p != nil {
					p.Close()
				}
				return
			}
			if err != nil {
				s.webErr = err.Error()
				return
			}
			s.webProxy = p
			path := u.EscapedPath()
			if path == "" {
				path = "/"
			}
			if u.RawQuery != "" {
				path += "?" + u.RawQuery
			}
			if err := s.web.Page().LoadURL(p.URL + "?path=" + url.QueryEscape(path)); err != nil {
				s.webErr = err.Error()
			}
		})
	}()
}
