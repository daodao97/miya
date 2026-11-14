package nsmobile

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"github.com/tailscale/tailcat"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	phoneclient "miya/internal/phone/client"
	"miya/internal/phone/desktopview"
	"miya/internal/remote"
)

type desktopTransport func(*http.Request) (*http.Response, error)

func (f desktopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func phoneFrame(t *testing.T, c color.Color) *desktopview.Frame {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	img.Set(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	f, err := desktopview.DecodeImage(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func startDesktop(t *testing.T, phase string) (*nsgotest.App, *App, *phoneclient.MobileDesktopPage, chan remote.Request) {
	t.Helper()
	app, m, sent := startPhone(t)
	ctx, cancel := context.WithCancel(context.Background())
	client := control.NewClient(nil, strings.Repeat("a", 64))
	client.SetTransport(desktopTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/frame") {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	}))
	p := &phoneclient.MobileDesktopPage{Client: client, Ctx: ctx, Cancel: cancel, Phase: phase, Title: "Connecting to Mac", Detail: "If your Mac is asleep, its screen will wake first.", Busy: phase == "connecting", Zoom: 1, Trackpad: true, Cursor: desktopview.Point{X: .5, Y: .5}, Sending: true}
	changePhone(app, m, func() {
		m.DesktopPage = p
		m.DesktopReturn = "/thread"
		m.Path = "/desktop"
		if phase == "live" {
			p.Active = true
			p.Image = phoneFrame(t, color.Black)
			p.Status = "Connected · Full desktop"
		}
	})
	t.Cleanup(func() { app.Post(func() { m.CloseDesktopPage(false) }); app.Settle(); cancel() })
	return app, m, p, sent
}
func TestDesktopConnectingLiveEndedRetainsImageAndEditor(t *testing.T) {
	app, m, p, sent := startDesktop(t, "connecting")
	if !strings.Contains(strings.Join(app.Texts(), " "), "If your Mac is asleep") || app.Exists("id:desktop-frame") {
		t.Fatal("connecting surface", app.Texts())
	}
	changePhone(app, m, func() {
		p.Phase = "live"
		p.Active = true
		p.Busy = false
		p.Image = phoneFrame(t, color.Black)
		p.Status = "Connected · Full desktop"
	})
	frame := app.Find("id:desktop-frame").ID
	app.Press("id:desktop-keyboard")
	app.Type("id:desktop-text", "日本語")
	editor := app.Find("id:desktop-text").ID
	next := phoneFrame(t, color.White)
	changePhone(app, m, func() { m.ReceiveDesktopFrame(p, next.Data, next, false, nil) })
	if app.Find("id:desktop-frame").ID != frame || app.Find("id:desktop-text").ID != editor || p.Text != "日本語" {
		t.Fatal("frame rebuilt editor or image")
	}
	if !bytes.Equal(app.Find("id:desktop-frame").Image.Source.Data, next.Data) {
		t.Fatal("frame source not updated")
	}
	app.Press("id:desktop-send")
	if len(p.Queue) != 1 || p.Queue[0].Text != "日本語" {
		t.Fatal("composed text lost", p.Queue)
	}
	changePhone(app, m, func() { m.DesktopFinish(p, "Remote control ended") })
	if app.Exists("id:desktop-frame") || app.Exists("id:desktop-text") || !strings.Contains(strings.Join(app.Texts(), " "), "Control session ended") || p.Image != nil || p.Text != "" || len(p.Queue) != 0 {
		t.Fatal("ended surface retained live content", app.Texts())
	}
	awaitRequest(t, app, sent, "desktop-close")
	app.Press("id:desktop-return")
	if m.DesktopPage != nil || m.Path != "/thread" {
		t.Fatal("back did not release page")
	}
}
func TestDesktopTouchTrackpadKeyboardAndCancellation(t *testing.T) {
	app, m, p, _ := startDesktop(t, "live")
	app.Press("id:desktop-pointer") // direct touch
	vp := app.Find("id:desktop-viewport")
	at := core.Point{X: vp.Frame.Size.Width / 2, Y: vp.Frame.Size.Height / 2}
	finger := app.Touch("id:desktop-viewport", at)
	finger.Lift()
	if len(p.Queue) != 1 || p.Queue[0].Kind != "click" || p.Queue[0].X < .49 || p.Queue[0].X > .51 {
		t.Fatal("touch tap did not map through image bounds", p.Queue)
	}
	changePhone(app, m, func() { p.Queue = nil })
	finger = app.Touch("id:desktop-viewport", at)
	finger.Move(core.Point{X: at.X, Y: at.Y - 20})
	finger.Move(core.Point{X: at.X, Y: at.Y - 40})
	finger.Lift()
	if len(p.Queue) != 1 || p.Queue[0].Kind != "scroll" || p.Queue[0].DeltaY != -80 {
		t.Fatal("touch scroll lost ordering/coalescing", p.Queue)
	}
	changePhone(app, m, func() { p.Queue = nil })
	finger = app.Touch("id:desktop-viewport", at)
	finger.Cancel()
	if len(p.Queue) != 0 {
		t.Fatal("cancelled press clicked", p.Queue)
	}
	app.Press("id:desktop-pointer")
	app.Wheel("id:desktop-viewport", core.Point{Y: -12}, core.Modifiers{})
	if len(p.Queue) != 1 || p.Queue[0].Kind != "scroll" {
		t.Fatal("native wheel missing", p.Queue)
	}
	app.Pinch("id:desktop-viewport", 1.5, 2)
	if p.Zoom != 2 {
		t.Fatal("pinch scale", p.Zoom)
	}
	changePhone(app, m, func() { p.Queue = nil })
	app.Press("id:desktop-keyboard")
	app.Press("id:desktop-mod-command")
	app.Press("id:desktop-key-c")
	if len(p.Queue) != 1 || p.Queue[0].Kind != "key" || p.Queue[0].Key != "c" || p.Queue[0].Modifier != "command" || p.Modifier != "" {
		t.Fatal("modifier shortcut missing", p.Queue)
	}
	changePhone(app, m, func() { p.Unlock.Locked = true; p.Queue = nil })
	app.Press("id:desktop-key-enter")
	if len(p.Queue) != 0 {
		t.Fatal("locked Mac received input")
	}
}
func TestDesktopFloatKeepsLeaseAndBackgroundClearsSecrets(t *testing.T) {
	app, m, p, sent := startDesktop(t, "live")
	changePhone(app, m, func() {
		p.Text = "secret"
		p.Unlock.Text = "password"
		p.Queue = []desktop.Input{{Kind: "click", X: .5, Y: .5}}
	})
	app.Press("id:desktop-minimize")
	if !p.Floating || !app.Exists("id:desktop-float") || p.Text != "" || len(p.Queue) != 0 {
		t.Fatal("minimize did not clear input")
	}
	app.Press("id:desktop-float")
	if p.Floating || !app.Exists("id:desktop-viewport") {
		t.Fatal("restore failed")
	}
	changePhone(app, m, func() { m.EnterBackground() })
	if p.Phase != "paused" || p.Image != nil || p.Text != "" || p.Unlock.Text != "" || p.Ctx.Err() == nil {
		t.Fatal("background kept lease/frame/secrets")
	}
	awaitRequest(t, app, sent, "desktop-close")
}

func TestDesktopManagerRenamesSwitchesAndRestoresComposer(t *testing.T) {
	app, m, _ := startPhone(t)
	m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	a, b := nsDesktopLink(), nsDesktopLink()
	changePhone(app, m, func() { m.Connect(a); m.Draft = "draft A"; m.Connect(b); m.Draft = "draft B"; m.pages.Open("desktops") })
	idA, idB := m.Desktops.Items[0].ID, m.Desktops.Items[1].ID
	app.Clear("id:desktop-name-" + idA)
	app.Type("id:desktop-name-"+idA, "Office")
	app.Press("id:desktop-rename-" + idA)
	if m.Desktops.Items[0].Name != "Office" || !m.Desktops.Items[0].Renamed {
		t.Fatal("rename not saved")
	}
	app.Press("id:desktop-switch-" + idA)
	if m.Link != a || m.Draft != "draft A" {
		t.Fatal("desktop A draft not restored")
	}
	changePhone(app, m, func() { m.pages.Open("desktops") })
	app.Press("id:desktop-remove-" + idB)
	if len(m.Desktops.Items) != 1 || m.Link != a {
		t.Fatal("removing inactive desktop changed owner")
	}
	changePhone(app, m, func() { m.Close() })
}
func nsDesktopLink() string {
	k := tailcat.NewPrivateKey()
	k.Public.RegionID = 1
	return remote.Link(k.Public.Addr())
}
