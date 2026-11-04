package main

import (
	"context"
	"fmt"
	rt "miya/internal/runtime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const desktopWebPrefix = "/.miya-web/"

// Only numeric ports on this desktop's own interfaces are reachable.
// The handler is served inside the existing paired, private file tunnel.
func desktopWebServer(validators ...func(context.Context, string, string, int) error) http.Handler {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		if rt.LocalHost(host) {
			return dialer.DialContext(ctx, "tcp", address)
		}
		if host != "localhost" {
			return nil, fmt.Errorf("Service address is no longer local: %s", host)
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
		if err != nil {
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort("::1", port))
		}
		return conn, nil
	}, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, desktopWebPrefix), "/", 2)
		if !strings.HasPrefix(r.URL.Path, desktopWebPrefix) || len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		port, err := parseWebPort(parts[0])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		prefix := desktopWebPrefix + parts[0]
		target := &url.URL{Scheme: "http", Host: "localhost:" + strconv.Itoa(port)}
		host := r.Header.Get("X-Miya-Web-Host")
		token := r.Header.Get("X-Miya-Runtime")
		if !validWebServiceHost(host, token) || host != "" && !rt.LocalHost(host) {
			http.Error(w, "Invalid local service address", http.StatusBadRequest)
			return
		}
		if token != "" {
			if len(validators) == 0 {
				http.Error(w, "Desktop update required", 403)
				return
			}
			if err := validators[0](r.Context(), token, host, port); err != nil {
				http.Error(w, err.Error(), http.StatusGone)
				return
			}
		}
		if host != "" {
			target.Host = net.JoinHostPort(host, strconv.Itoa(port))
		}
		proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.RawPath = strings.TrimPrefix(pr.Out.URL.EscapedPath(), prefix)
			pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, prefix)
			pr.SetURL(target)
			pr.Out.Host = "localhost:" + strconv.Itoa(port)
			pr.Out.Header.Del("X-Miya-Web-Host")
			pr.Out.Header.Del("X-Miya-Runtime")
		}, ModifyResponse: func(response *http.Response) error {
			if location := response.Header.Get("Location"); location != "" {
				response.Header.Set("Location", webServiceLocationForHost(location, prefix, port, host))
			}
			cookies := response.Cookies()
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if localWebHost(strings.TrimPrefix(cookie.Domain, ".")) || host != "" && strings.TrimPrefix(cookie.Domain, ".") == host {
					cookie.Domain = ""
				}
				response.Header.Add("Set-Cookie", cookie.String())
			}
			return nil
		}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("Unable to reach port %d on your computer. Check that the HTTP service is running.", port), http.StatusBadGateway)
		}}
		proxy.ServeHTTP(w, r)
	})
}

func localWebHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func parseWebPort(value string) (int, error) {
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("Enter a port number between 1 and 65535")
		}
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("Enter a port number between 1 and 65535")
	}
	return port, nil
}

// Local redirects keep the selected port's phone origin; external links stay intact.
func webServiceLocation(location, prefix string, port int) string {
	return webServiceLocationForHost(location, prefix, port, "")
}

func webServiceLocationForHost(location, prefix string, port int, host string) string {
	u, err := url.Parse(location)
	if err != nil {
		return location
	}
	if u.IsAbs() || u.Host != "" {
		locationPort := u.Port()
		if locationPort == "" {
			locationPort = "80"
		}
		if !(localWebHost(u.Hostname()) || host != "" && u.Hostname() == host) || locationPort != strconv.Itoa(port) || u.Scheme != "" && u.Scheme != "http" {
			return location
		}
		u.Scheme, u.Host = "", ""
		if u.Path == "" {
			u.Path = "/"
		}
	}
	if strings.HasPrefix(u.Path, "/") {
		u.RawPath = prefix + u.EscapedPath()
		u.Path = prefix + u.Path
		return u.String()
	}
	return location
}
