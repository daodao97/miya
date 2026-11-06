package phoneproxy

import (
	context "context"
	rand "crypto/rand"
	cryptoSubtle "crypto/subtle"
	hex "encoding/hex"
	errors "errors"
	appmodel "miya/internal/appmodel"
	remote "miya/internal/remote"
	net "net"
	http "net/http"
	httputil "net/http/httputil"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"
	time "time"
)

type PhoneFileProxy struct {
	closeOnce   sync.Once
	server      *http.Server
	transport   *http.Transport
	conn        atomic.Pointer[remote.Conn]
	connections sync.Map // Includes hijacked WebSocket connections until Close.
	URL         string
	WebHost     string
	webToken    string
}

// The browser sees ordinary same-origin HTTP, including absolute /assets URLs.
// Only the selected project is reachable on this loopback-only origin.
func NewPhoneFileProxy(conn *remote.Conn, project string) (*PhoneFileProxy, error) {
	p := new(PhoneFileProxy)
	p.conn.Store(conn)
	err := p.Start(project, func(ctx context.Context) (net.Conn, error) {
		current := p.conn.Load()
		if current == nil {
			return nil, errors.New("Desktop connection lost")
		}
		return current.DialFiles(ctx)
	})
	return p, err
}

func (p *PhoneFileProxy) Start(project string, dial func(context.Context) (net.Conn, error)) error {
	return p.StartProxy("/"+appmodel.ProjectFileID(project), "", dial)
}

func (p *PhoneFileProxy) StartProxy(prefix, webOrigin string, dial func(context.Context) (net.Conn, error)) error {
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
	bootstrapPath := "/.miya-open/" + token
	if webOrigin != "" {
		bootstrapPath = "/.miya-web-open/" + token
	}
	p.URL = origin + bootstrapPath
	p.transport = &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 6, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return dial(ctx)
		},
	}
	proxy := &httputil.ReverseProxy{Transport: p.transport, ModifyResponse: func(response *http.Response) error {
		location := response.Header.Get("Location")
		if strings.HasPrefix(location, prefix+"/") {
			response.Header.Set("Location", strings.TrimPrefix(location, prefix))
		}
		if webOrigin != "" && response.Header.Get("Access-Control-Allow-Origin") == webOrigin {
			response.Header.Set("Access-Control-Allow-Origin", origin)
		}
		return nil
	}, Director: func(r *http.Request) {
		r.URL.Scheme = "http"
		r.URL.Host = "desktop"
		r.Host = "desktop"
		r.URL.RawPath = prefix + r.URL.EscapedPath()
		r.URL.Path = prefix + r.URL.Path
		if webOrigin == "" {
			r.Header.Del("Cookie")
			r.Header.Del("Authorization")
		} else {
			r.Header.Set("X-Miya-Web-Host", p.WebHost)
			r.Header.Set("X-Miya-Runtime", p.webToken)
			cookies := r.Cookies()
			r.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != cookieName {
					r.AddCookie(cookie)
				}
			}
			if r.Header.Get("Origin") == origin {
				r.Header.Set("Origin", webOrigin)
			}
			if ref := r.Header.Get("Referer"); strings.HasPrefix(ref, origin+"/") {
				r.Header.Set("Referer", webOrigin+strings.TrimPrefix(ref, origin))
			}
		}
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Unable to connect to the desktop service. Check that your desktop is online and the service is running.", http.StatusBadGateway)
	}}
	p.server = &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, ConnState: func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			p.connections.Store(conn, true)
		}
		if state == http.StateClosed {
			p.connections.Delete(conn)
		}
	}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Host != ln.Addr().String() {
			http.Error(w, "Forbidden", 403)
			return
		}
		if webOrigin == "" && r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.URL.Path == bootstrapPath {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			target := "/"
			if requested := r.URL.Query().Get("path"); webOrigin != "" && strings.HasPrefix(requested, "/") && !strings.HasPrefix(requested, "//") && !strings.ContainsAny(requested, "\\\r\n") {
				target = requested
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
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

func (p *PhoneFileProxy) SetConn(conn *remote.Conn) {
	p.conn.Store(conn)
	p.transport.CloseIdleConnections()
}

func (p *PhoneFileProxy) Close() {
	p.closeOnce.Do(func() {
		p.conn.Store(nil)
		p.server.Close()
		p.connections.Range(func(key, _ any) bool { key.(net.Conn).Close(); p.connections.Delete(key); return true })
		p.transport.CloseIdleConnections()
	})
}
