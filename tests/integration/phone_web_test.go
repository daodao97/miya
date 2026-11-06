package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"miya/internal/phoneproxy"
	"miya/internal/webservice"
)

func webProxyFixture(t *testing.T, handler http.Handler) (*phoneproxy.PhoneFileProxy, *http.Client, string) {
	t.Helper()
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	u, _ := url.Parse(app.URL)
	bridge := httptest.NewServer(webservice.Server())
	t.Cleanup(bridge.Close)
	p := new(phoneproxy.PhoneFileProxy)
	if err := p.StartProxy(webservice.Prefix+u.Port(), "http://localhost:"+u.Port(), func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", bridge.Listener.Addr().String())
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	response, err := client.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("bootstrap: %s", response.Status)
	}
	initial, _ := url.Parse(p.URL)
	return p, client, "http://" + initial.Host
}

func TestPhoneWebProxyHTTP(t *testing.T) {
	_, client, origin := webProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<link href="/assets/style.css"><script src="/app.js"></script>`)
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "active", Domain: "localhost", Path: "/", HttpOnly: true})
			w.Header().Set("Location", "http://"+r.Host+"/welcome?ok=1")
			w.WriteHeader(302)
		case "/welcome":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "active" || r.URL.RawQuery != "ok=1" {
				t.Error("app session or redirect query lost")
			}
			for _, cookie := range r.Cookies() {
				if strings.HasPrefix(cookie.Name, "miya_files_") {
					t.Error("proxy credential leaked to app")
				}
			}
			fmt.Fprint(w, "welcome")
		case "/api/a/b":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != "data=hello" || r.URL.EscapedPath() != "/api/a%2Fb" || r.URL.RawQuery != "q=%2F" {
				t.Errorf("request lost: %s %s %q", r.Method, r.URL, body)
			}
			if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Referer") != "http://"+r.Host+"/form" || r.Header.Get("Authorization") != "Bearer app-token" {
				t.Errorf("request headers lost: %v", r.Header)
			}
			w.Header().Set("Access-Control-Allow-Origin", "http://"+r.Host)
			fmt.Fprint(w, "saved")
		default:
			fmt.Fprint(w, r.URL.Path)
		}
	}))
	for path, expected := range map[string]string{"/assets/style.css": "/assets/style.css", "/app.js": "/app.js", "/login": "welcome"} {
		response, err := client.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || string(body) != expected || response.Request.URL.Host != strings.TrimPrefix(origin, "http://") {
			t.Fatalf("%s: %s %q %s", path, response.Status, body, response.Request.URL)
		}
	}
	request, _ := http.NewRequest("POST", origin+"/api/a%2Fb?q=%2F", strings.NewReader("data=hello"))
	request.Header.Set("Origin", origin)
	request.Header.Set("Referer", origin+"/form")
	request.Header.Set("Authorization", "Bearer app-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("POST/CORS failed")
	}
	request, _ = http.NewRequest("POST", origin+"/api", nil)
	request.Header.Set("Origin", "https://other.example")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin request accepted")
	}
	response, err = http.Get(origin + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("unauthenticated request accepted")
	}
}

func TestPhoneWebProxyStreamingAndWebSocketClose(t *testing.T) {
	ended := make(chan struct{})
	p, client, origin := webProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/ws":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer close(ended)
			defer conn.CloseNow()
			kind, data, err := conn.Read(r.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Write(r.Context(), kind, data); err != nil {
				t.Error(err)
				return
			}
			_, _, _ = conn.Read(r.Context())
		}
	}))
	response, err := client.Get(origin + "/events")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	response.Body.Close()
	if err != nil || line != "data: ready\n" {
		t.Fatalf("SSE buffered: %q %v", line, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(origin, "http://", "ws://", 1)+"/ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {origin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("hot reload")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "hot reload" {
		t.Fatalf("WebSocket failed: %q %v", data, err)
	}
	p.Close()
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("closing viewer left WebSocket open")
	}
}
