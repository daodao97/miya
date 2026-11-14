package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/tailcat"

	"miya/internal/chat/model"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	phonecache "miya/internal/phone/cache"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type desktopTestTransport func(*http.Request) (*http.Response, error)

func (f desktopTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func desktopClientFixture(t *testing.T) (*Client, chan func(), chan remote.Request) {
	t.Helper()
	m := New()
	m.Loaded = true
	posts := make(chan func(), 128)
	sent := make(chan remote.Request, 32)
	m.Dispatch = func(f func()) { posts <- f }
	m.SendRequest = func(r remote.Request) error { sent <- r; return nil }
	t.Cleanup(func() { m.CloseDesktopPage(false); m.stopDesktopOpen() })
	return m, posts, sent
}
func pumpDesktop(t *testing.T, posts chan func(), done func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !done() {
		select {
		case f := <-posts:
			f()
		case <-deadline.C:
			t.Fatal("desktop worker did not finish")
		}
	}
}
func desktopHTTP(token string, serve func(*http.Request) (int, string)) *control.Client {
	c := control.NewClient(nil, token)
	c.SetTransport(desktopTestTransport(func(r *http.Request) (*http.Response, error) {
		status, body := serve(r)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}))
	return c
}
func TestDesktopOpenRejectsStaleLeaseAndBackRevokes(t *testing.T) {
	m, posts, sent := desktopClientFixture(t)
	m.Connected = true
	m.State.DesktopControl = true
	m.DesktopClient = func(_ *remote.Conn, token string) *control.Client {
		return desktopHTTP(token, func(r *http.Request) (int, string) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/session"):
				return 200, `{"pick":true,"windowOnly":true,"window":{"id":0}}`
			case strings.HasSuffix(r.URL.Path, "/windows"):
				return 200, "[]"
			}
			return 200, "{}"
		})
	}
	m.OpenRemoteDesktop()
	r := <-sent
	if r.Op != "desktop-open" || !r.WindowPick || r.RequestID == "" {
		t.Fatal("bad desktop request", r)
	}
	stale, _ := json.Marshal(phoneproto.DesktopReply{RequestID: "stale", Token: strings.Repeat("b", 64)})
	m.receiveDesktopControl(nil, stale, "")
	if got := <-sent; got.Op != "desktop-close" || got.DesktopToken != strings.Repeat("b", 64) {
		t.Fatal("stale lease not revoked", got)
	}
	reply, _ := json.Marshal(phoneproto.DesktopReply{RequestID: r.RequestID, Token: strings.Repeat("a", 64)})
	m.receiveDesktopControl(nil, reply, "")
	p := m.DesktopPage
	pumpDesktop(t, posts, func() bool { return p.Phase == "picker" && !p.Busy })
	m.CloseDesktopPage(true)
	if m.DesktopPage != nil || p.Ctx.Err() == nil || m.Path == "/desktop" {
		t.Fatal("back kept session")
	}
	got := <-sent
	if got.Op != "desktop-close" || got.DesktopToken != strings.Repeat("a", 64) {
		t.Fatal("back did not revoke lease", got)
	}
}
func TestDesktopResumeRestoresExactTargetAndRejectsOldWorkers(t *testing.T) {
	m, posts, sent := desktopClientFixture(t)
	m.Link = "same-desktop"
	m.Connected = true
	m.Conn = new(remote.Conn)
	ctx, cancel := context.WithCancel(context.Background())
	p := &MobileDesktopPage{Ctx: ctx, Cancel: cancel, Client: desktopHTTP("old", func(*http.Request) (int, string) { return 200, "{}" }), gen: m.Generation, Phase: "live", Active: true, WindowOnly: true, Window: desktop.Window{ID: 42, PID: 7}, Zoom: 2, Trackpad: true, Cursor: DesktopPoint{.6, .4}, owner: remote.Request{Op: "desktop-open", WindowPick: true, ServerID: "owner", Project: "/original"}, desktopLink: m.Link, Text: "secret", Queue: []desktop.Input{{Kind: "text", Text: "no replay"}}}
	m.DesktopPage = p
	late := make(chan struct{})
	applied := false
	m.desktopDo(p, func(context.Context) func() { <-late; return func() { applied = true } })
	m.suspendDesktopPage()
	close(late)
	if p.Phase != "paused" || p.Active || len(p.Queue) != 0 || p.Text != "" || ctx.Err() == nil {
		t.Fatal("suspension did not clear input")
	}
	// A fresh authoritative state on the new link owns resumption.
	m.ApplyPhoneState(phoneproto.State{DesktopControl: true})
	var open remote.Request
	for open.Op != "desktop-open" {
		open = <-sent
	}
	if open.ServerID != "owner" || open.Project != "/original" {
		t.Fatal("resume changed owner", open)
	}
	var jpegData bytes.Buffer
	_ = jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 120, 80)), nil)
	m.DesktopClient = func(_ *remote.Conn, token string) *control.Client {
		return desktopHTTP(token, func(r *http.Request) (int, string) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/session"):
				return 200, `{"pick":true,"windowOnly":true,"window":{"id":0}}`
			case strings.HasSuffix(r.URL.Path, "/windows"):
				return 200, `[{"id":42,"pid":7}]`
			case strings.HasSuffix(r.URL.Path, "/frame"):
				return 200, jpegData.String()
			case strings.HasSuffix(r.URL.Path, "/actions"):
				return 404, "unsupported"
			}
			return 200, "{}"
		})
	}
	reply, _ := json.Marshal(phoneproto.DesktopReply{RequestID: open.RequestID, Token: strings.Repeat("c", 64)})
	m.receiveDesktopControl(m.Conn, reply, "")
	pumpDesktop(t, posts, func() bool { return m.DesktopPage.Image != nil })
	fresh := m.DesktopPage
	if fresh == p || fresh.Window.ID != 42 || fresh.Window.PID != 7 || fresh.Zoom != 2 || len(fresh.Queue) != 0 || fresh.Text != "" || applied {
		t.Fatal("resume reused old state")
	}
	m.CloseDesktopPage(false)
}
func testPairLink() string {
	k := tailcat.NewPrivateKey()
	k.Public.RegionID = 1
	return remote.Link(k.Public.Addr())
}
func TestDesktopsSwitchKeepsSeparateDraftsAndOptions(t *testing.T) {
	m, _, _ := desktopClientFixture(t)
	m.DataDir = t.TempDir()
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	defer m.Close()
	a, b := testPairLink(), testPairLink()
	m.Connect(a)
	id := m.CurrentDesktopID()
	m.Selected = "/a"
	m.NewAgent = "codex"
	m.Draft = "draft A"
	m.Open = 7
	m.ShowThread(model.Thread{ID: 7, ServerID: "session-a", Agent: "codex"})
	m.SetOptions("codex", remote.SendOptions{Model: "model-a"})
	m.Connect(b)
	if m.Draft != "" || m.Open != 0 {
		t.Fatal("new desktop inherited draft")
	}
	m.Draft = "draft B"
	m.SetOptions("codex", remote.SendOptions{Model: "model-b"})
	m.SwitchDesktop(id)
	if m.Draft != "draft A" || m.Selected != "/a" || m.Viewer.Current().ServerID != "session-a" || m.SendOptions("codex").Model != "model-a" {
		t.Fatal("desktop session/options not restored")
	}
}
func TestDesktopSavedUnlockRequiresAuthentication(t *testing.T) {
	m, _, _ := desktopClientFixture(t)
	called := false
	c := desktopHTTP("token", func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/unlock") {
			called = true
		}
		return 200, "{}"
	})
	result := m.DesktopPerformUnlock(context.Background(), c, nil, "key", "password", false, true)
	if result.Err == nil || called {
		t.Fatal("saved password bypassed storage/authentication")
	}
	m.DesktopAuthenticate = func(context.Context) error { return errors.New("denied") }
	result = m.DesktopPerformUnlock(context.Background(), c, &memoryDesktopStore{}, "key", "password", false, true)
	if result.Err == nil || called {
		t.Fatal("cancelled authentication submitted password")
	}
	result = m.DesktopPerformUnlock(context.Background(), c, nil, "", "password", false, false)
	if result.Err != nil || !called {
		t.Fatal("manual unlock unavailable", result.Err)
	}
}

type memoryDesktopStore struct{ data map[string][]byte }

var errDesktopMissing = errors.New("missing")

func (s *memoryDesktopStore) Get(k string) ([]byte, error) {
	if v, ok := s.data[k]; ok {
		return v, nil
	}
	return nil, errDesktopMissing
}
func (s *memoryDesktopStore) Set(k string, v []byte) error {
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[k] = bytes.Clone(v)
	return nil
}
func (s *memoryDesktopStore) Delete(k string) error { delete(s.data, k); return nil }
func TestDesktopRegistryMigrationAndEmptyForget(t *testing.T) {
	link := testPairLink()
	store := &memoryDesktopStore{data: map[string][]byte{"desktop-link": []byte(link)}}
	saved, err := ReadDesktops(store, errDesktopMissing)
	if err != nil || len(saved.Items) != 1 || saved.Items[0].Link != link {
		t.Fatal("singleton migration failed", err)
	}
	_ = store.Set(phonecache.DesktopsKey, []byte(`{"version":1,"items":[]}`))
	saved, err = ReadDesktops(store, errDesktopMissing)
	if err != nil || len(saved.Items) != 0 {
		t.Fatal("forgotten desktop resurrected", err)
	}
}
