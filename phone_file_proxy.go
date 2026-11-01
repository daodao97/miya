package main

import (
	"context"
	"crypto/rand"
	cryptoSubtle "crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync/atomic"
	"time"

	"miya/internal/remote"
)

type phoneFileProxy struct {
	server    *http.Server
	transport *http.Transport
	conn      atomic.Pointer[remote.Conn]
	URL       string
}

// The browser sees ordinary same-origin HTTP, including absolute /assets URLs.
// Only the selected project is reachable on this loopback-only origin.
func newPhoneFileProxy(conn *remote.Conn, project string) (*phoneFileProxy, error) {
	p := new(phoneFileProxy)
	p.conn.Store(conn)
	err := p.start(project, func(ctx context.Context) (net.Conn, error) {
		current := p.conn.Load()
		if current == nil {
			return nil, errors.New("电脑连接已断开")
		}
		return current.DialFiles(ctx)
	})
	return p, err
}

func (p *phoneFileProxy) start(project string, dial func(context.Context) (net.Conn, error)) error {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(secret[:])
	cookieName := "miya_files_" + token[:12]
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	origin := "http://" + ln.Addr().String()
	p.URL = origin + "/.miya-open/" + token
	p.transport = &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 6, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return dial(ctx)
		},
	}
	projectID := projectFileID(project)
	proxy := &httputil.ReverseProxy{Transport: p.transport, ModifyResponse: func(response *http.Response) error {
		location := response.Header.Get("Location")
		if strings.HasPrefix(location, "/"+projectID+"/") {
			response.Header.Set("Location", strings.TrimPrefix(location, "/"+projectID))
		}
		return nil
	}, Director: func(r *http.Request) {
		r.URL.Scheme = "http"
		r.URL.Host = "desktop"
		r.Host = "desktop"
		r.URL.Path = "/" + projectFileID(project) + r.URL.Path
		r.URL.RawPath = ""
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "无法连接电脑文件服务。请确认桌面端已更新且保持在线，然后重试。", http.StatusBadGateway)
	}}
	p.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Host != ln.Addr().String() {
			http.Error(w, "Forbidden", 403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.URL.Path == "/.miya-open/"+token {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil || cryptoSubtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
			http.Error(w, "Forbidden", 403)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != origin {
			http.Error(w, "Forbidden", 403)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go p.server.Serve(ln)
	return nil
}
func (p *phoneFileProxy) setConn(conn *remote.Conn) {
	p.conn.Store(conn)
	p.transport.CloseIdleConnections()
}
func (p *phoneFileProxy) Close() {
	p.conn.Store(nil)
	p.server.Close()
	p.transport.CloseIdleConnections()
}
