package nsui

import (
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	projectfiles "miya/internal/project/files"
	webservice "miya/internal/runtime/web"
	"miya/internal/uiruntime"
)

func TestPanelTabsBelongToThreadReference(t *testing.T) {
	h := runtimeHost()
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-files")
	app.WaitFor("id:file-notes.txt", time.Second)
	app.Press("id:file-notes.txt")
	app.WaitFor("id:file-editor", time.Second)
	app.Press("id:panel-add")
	app.Press("id:panel-add-browser")
	app.Type("id:browser-address-2", "https://alpha.test/preview")
	app.Submit("id:browser-address-2")
	app.WaitFor("id:browser-2", time.Second)
	app.Press("id:thread-2")
	app.WaitFor("id:panel-chooser", time.Second)
	app.Press("id:panel-open-review")
	app.WaitFor("id:review-diff", time.Second)
	app.Press("id:thread-1")
	app.WaitFor("id:browser-2", time.Second)
	if got := app.Find("id:browser-address-2").Text; got != "https://alpha.test/preview" {
		t.Fatalf("lost browser address: %q", got)
	}
	app.Press("id:panel-tab-1")
	app.WaitFor("id:file-editor", time.Second)
	if got := app.Find("id:panel-tab-label-1").Text; got != "notes.txt" {
		t.Fatalf("lost editor selection: %q", got)
	}
	// Sidebar IDs can change while the stable thread ref remains the same.
	app.Post(func() { h.threads[0].ServerID = "1"; h.threads[0].ID = 7; h.current = 7; uiruntime.Invalidate() })
	app.Settle()
	if !app.Exists("id:file-editor") || !app.Exists("id:panel-tab-2") {
		t.Fatal("panel keyed by sidebar ID instead of thread ref")
	}
	app.Press("id:thread-2")
	app.WaitFor("id:review-diff", time.Second)
	if app.Exists("id:panel-tab-2") {
		t.Fatal("tabs leaked from another conversation")
	}
}

func TestPanelAdoptsDraftOnlyWhenItStartsConversation(t *testing.T) {
	h := newFakeHost(model.Thread{}, model.Thread{ID: 2, Title: "Existing"})
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-browser")
	app.Type("id:browser-address-1", "draft.test")
	app.Press("id:thread-2")
	app.WaitFor("id:panel-chooser", time.Second)
	app.Post(func() { h.current = 0; uiruntime.Invalidate() })
	app.WaitFor("id:browser-address-1", time.Second)
	if got := app.Find("id:browser-address-1").Text; got != "draft.test" {
		t.Fatalf("draft address = %q", got)
	}
	app.Post(func() { h.threads[0] = model.Thread{ID: 3, Title: "Just sent"}; h.current = 3; uiruntime.Invalidate() })
	app.Settle()
	if got := app.Find("id:browser-address-1").Text; got != "draft.test" {
		t.Fatalf("first send lost draft tabs: %q", got)
	}
}

type trackedTerminalConn struct {
	net.Conn
	once   sync.Once
	closed *atomic.Int32
}

func (c *trackedTerminalConn) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}

type terminalFiles struct {
	*fakeFiles
	starts, closes atomic.Int32
	peersMu        sync.Mutex
	peers          []net.Conn
}

func (f *terminalFiles) Terminal(string) (io.ReadWriteCloser, error) {
	conn, peer := net.Pipe()
	f.peersMu.Lock()
	f.peers = append(f.peers, peer)
	f.peersMu.Unlock()
	f.starts.Add(1)
	return &trackedTerminalConn{Conn: conn, closed: &f.closes}, nil
}

func TestPanelKeepsTerminalsUntilTabCloseAndShellTeardown(t *testing.T) {
	files := &terminalFiles{fakeFiles: newPanelFiles()}
	defer func() {
		files.peersMu.Lock()
		defer files.peersMu.Unlock()
		for _, peer := range files.peers {
			peer.Close()
		}
	}()
	t.Run("window", func(t *testing.T) {
		h := runtimeHost()
		h.files = files
		app := startBridged(t, h)
		app.Press("role:button Show / Hide right panel")
		app.Press("id:panel-open-terminal")
		app.WaitFor("id:terminal-1", time.Second)
		app.Press("id:thread-2")
		app.WaitFor("id:panel-chooser", time.Second)
		app.Press("id:panel-open-terminal")
		app.WaitFor("id:terminal-1", time.Second)
		app.Press("id:thread-1")
		app.WaitFor("id:terminal-1", time.Second)
		if files.starts.Load() != 2 || files.closes.Load() != 0 {
			t.Fatal("conversation switch restarted or closed terminals")
		}
		app.Press("id:panel-close-1")
		app.WaitUntil(time.Second, func() bool { return files.closes.Load() == 1 })
		app.Press("id:thread-2")
		app.WaitFor("id:terminal-1", time.Second)
	})
	// The shell's cleanup must close even terminals in other conversations.
	deadline := time.Now().Add(time.Second)
	for files.closes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if files.closes.Load() != 2 {
		t.Fatal("shell teardown left a conversation's terminal running")
	}
}

func TestMirroredBrowserPublicURLsAndDisconnectedService(t *testing.T) {
	h := runtimeHost()
	h.remoteHost = true
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-browser")
	app.Type("id:browser-address-1", "https://example.com/page")
	app.Submit("id:browser-address-1")
	app.WaitFor("id:browser-1", time.Second)
	if got := app.Find("id:browser-1").Web.Source.Src; got != "https://example.com/page" {
		t.Fatalf("public URL = %q", got)
	}
	app.Clear("id:browser-address-1")
	app.Type("id:browser-address-1", "http://localhost:3000/path?q=1")
	app.Submit("id:browser-address-1")
	app.Settle()
	if app.Exists("id:browser-1") || !strings.Contains(joinedTexts(app), "Browser unavailable") {
		t.Fatal("remote localhost loaded on the client without a service proxy")
	}
}

func TestProxyNavigationKeepsRemoteAddress(t *testing.T) {
	nsgotest.Start(t, func(cx *core.Scope) core.Node {
		s := &surface{page: core.NewSignal(cx, "https://external.example/"), webTarget: "http://localhost:3000/", webProxyURL: "http://127.0.0.1:45678/.miya-open/secret"}
		for raw, want := range map[string]string{
			"http://127.0.0.1:45678/.miya-open/secret?path=%2F": "http://localhost:3000/",
			"http://127.0.0.1:45678/path?q=1#part":              "http://localhost:3000/path?q=1#part",
			"https://example.com/":                              "https://example.com/",
		} {
			if got := browserPageURL(s, raw); got != want {
				t.Errorf("%s => %s, want %s", raw, got, want)
			}
		}
		return core.View{}
	})
}

type delayedTerminalFiles struct {
	*terminalFiles
	started, release chan struct{}
}

func (f *delayedTerminalFiles) Terminal(dir string) (io.ReadWriteCloser, error) {
	close(f.started)
	<-f.release
	return f.terminalFiles.Terminal(dir)
}

func TestTerminalCreatedAfterShellTeardownIsClosed(t *testing.T) {
	files := &delayedTerminalFiles{terminalFiles: &terminalFiles{fakeFiles: newPanelFiles()}, started: make(chan struct{}), release: make(chan struct{})}
	t.Run("window", func(t *testing.T) {
		h := runtimeHost()
		h.files = files
		app := startBridged(t, h)
		app.Press("role:button Show / Hide right panel")
		app.Press("id:panel-open-terminal")
		select {
		case <-files.started:
		case <-time.After(time.Second):
			t.Fatal("terminal never started")
		}
	})
	close(files.release)
	deadline := time.Now().Add(time.Second)
	for files.closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	files.peersMu.Lock()
	for _, peer := range files.peers {
		peer.Close()
	}
	files.peersMu.Unlock()
	if files.closes.Load() != 1 {
		t.Fatal("late terminal creation escaped shell teardown")
	}
}

func TestRemoteBrowserServiceTargets(t *testing.T) {
	for _, tc := range []struct {
		url, host      string
		port           int
		token          string
		proxy, invalid bool
	}{
		{url: "http://localhost:3000/x?q=1", host: "127.0.0.1", port: 3000, proxy: true},
		{url: "http://[::1]:8080/", host: "::1", port: 8080, proxy: true},
		{url: "http://0.0.0.0/", host: "127.0.0.1", port: 80, proxy: true},
		{url: "https://example.com/"},
		{url: "https://localhost:3000/", proxy: true, invalid: true},
		{url: "http://localhost:65536/", proxy: true, invalid: true},
		{url: "http://192.168.0.4:8080/", host: "192.168.0.4", port: 8080, token: "verified", proxy: true},
	} {
		service := webservice.Service{RuntimeToken: tc.token}
		if tc.token != "" {
			service.Host = tc.host
		}
		got, _, proxy, err := remoteBrowserService(tc.url, service)
		if (err != nil) != tc.invalid || proxy != tc.proxy || (!tc.invalid && tc.proxy && (got.Host != tc.host || got.Port != tc.port || got.RuntimeToken != tc.token)) {
			t.Errorf("%s: service %+v, proxy %v, error %v", tc.url, got, proxy, err)
		}
	}
}

func TestHTMLFilesRemainSourceOnly(t *testing.T) {
	h := runtimeHost()
	files := newPanelFiles()
	files.dirs[""] = []projectfiles.FileEntry{{Name: "index.html", Rel: "index.html"}}
	files.texts["index.html"] = "<script>fetch('http://localhost:3000/')</script>"
	h.files = files
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-files")
	app.WaitFor("id:file-index.html", time.Second)
	app.Press("id:file-index.html")
	app.WaitFor("id:file-editor", time.Second)
	if app.Exists("kind:web") || strings.Contains(joinedTexts(app), "Preview") {
		t.Fatal("HTML enabled without an exact-origin navigation policy")
	}
}

func TestDraftPanelAdoptedWhenSessionStartsInBackground(t *testing.T) {
	h := newFakeHost(model.Thread{}, model.Thread{ID: 2, Title: "Existing", ServerID: "existing"})
	h.threadRef = func(thread model.Thread) model.ThreadRef {
		return model.ThreadRef{Source: "0123456789abcdef0123456789abcdef", Backend: model.AgentCodex, SessionID: thread.ServerID}
	}
	app := startBridged(t, h)
	app.Press("role:button Show / Hide right panel")
	app.Press("id:panel-open-browser")
	app.Type("id:browser-address-1", "draft.test")
	// Send allocated the row, but thread/start has not returned a session ID.
	app.Post(func() { h.threads[0] = model.Thread{ID: 3, Title: "Starting"}; h.current = 3; uiruntime.Invalidate() })
	app.Settle()
	app.Press("id:thread-2")
	app.WaitFor("id:panel-chooser", time.Second)
	// The session arrives while another conversation is selected.
	app.Post(func() { h.threads[0].ServerID = "started"; uiruntime.Invalidate() })
	app.Settle()
	app.Post(func() { h.current = 0; uiruntime.Invalidate() })
	app.WaitFor("id:panel-chooser", time.Second)
	app.Press("id:thread-3")
	app.WaitFor("id:browser-address-1", time.Second)
	if got := app.Find("id:browser-address-1").Text; got != "draft.test" {
		t.Fatalf("late session lost draft panel: %q", got)
	}
}

func TestBrowserOriginKeepsRuntimeTokensOnExactOrigin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"http://EXAMPLE.test/a", "http://example.test:80/b", true},
		{"http://192.168.0.4:8080/a", "http://192.168.0.4:8080/b?q=1", true},
		{"http://[::1]:8080/a", "http://[::1]:8080/b", true},
		{"http://192.168.0.4:8080/", "http://192.168.0.4:8081/", false},
		{"http://example.test/", "https://example.test/", false},
		{"http://example.test/", "http://other.test/", false},
		{"http://example.test/", "", false},
	} {
		if got := sameBrowserOrigin(tc.a, tc.b); got != tc.same {
			t.Errorf("sameBrowserOrigin(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
