package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/desktop/control/controltest"
	"miya/internal/uiruntime"
)

func TestDesktopDisplaysPickerNamesAndLayout(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			_, _, displays, _ := controltest.Display(t)
			m := testMobileApp()
			p := liveTestDesktopPage(t, m)
			p.Pickable, p.DisplaysAvailable, p.AppsAvailable = true, true, true
			m.DesktopPresentDisplays(p, displays)
			m.Router().Push("/desktop")
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			tt.Frame()
			for _, text := range []string{"Full desktop", "Desktops", "Windows", "Apps", "Desktop 1 · Built-in Retina Display", "Desktop 2 · DELL P2725QE", "1512 × 982 · Main display", "2560 × 1440"} {
				if !tt.HasText(text) {
					t.Fatal("missing", text)
				}
			}
			first, a := tt.Find("Desktop 1 · Built-in Retina Display")
			second, b := tt.Find("Desktop 2 · DELL P2725QE")
			if !a || !b || first.X < 0 || first.X+first.W > float32(width) || second.Y < first.Y+first.H {
				t.Fatal("display list overlaps", first, second)
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-displays-%d", width))
			tt.SetPreferences(ui.Preferences{TextScale: 1.6})
			tt.Frame()
			second, _ = tt.Find("Desktop 2 · DELL P2725QE")
			if second.X < 0 || second.X+second.W > float32(width) || second.Y+second.H > 750 {
				t.Fatal("large display title clipped", second)
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-displays-large-%d", width))
		})
	}
}

func TestDesktopDisplaysInitializeShowsChoiceThenSelectsSecondary(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, displays, _ := controltest.Display(t)
	d.SetLeaseWindow(nil) // the full-desktop menu entry
	frame := windowJPEG(t)
	d.CaptureDisplay = func(display desktop.Display) ([]byte, error) { return frame, nil }
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client = controltest.Client(t, d, token)
	p.Active = false
	m.DesktopInitialize(p)
	wait(func() bool { return p.Phase == "picker" && !p.Busy })
	if p.PickerTab != 2 || len(p.Displays) != 2 || p.Active {
		t.Fatal("multiple displays did not offer a choice", p.PickerTab, p.Displays)
	}
	m.Router().Push("/desktop")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Desktop 2 · DELL P2725QE"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return p.Phase == "live" && p.Image != nil })
	if p.WindowOnly || p.Display != displays[1] || !strings.Contains(p.ConnectedStatus(), "Desktop 2 · DELL P2725QE") {
		t.Fatal("wrong display selected", p.Display, p.ConnectedStatus())
	}
	p.Cancel()
}

func TestDesktopDisplaysDisconnectReturnsToPicker(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, displays, _ := controltest.Display(t)
	d.Displays = func() ([]desktop.Display, error) { return displays[:1], nil }
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client, p.Pickable, p.DisplaysAvailable, p.Display, p.Displays = controltest.Client(t, d, token), true, true, displays[1], displays
	m.ReceiveDesktopFrame(p, nil, nil, false, &control.HTTPError{Status: 409, Message: "display disconnected"})
	wait(func() bool { return p.Phase == "picker" && !p.Busy })
	if p.Active || p.Image != nil || len(p.Queue) != 0 || p.PickerTab != 2 || len(p.Displays) != 1 {
		t.Fatal("disconnected display remained live")
	}
	p.Cancel()
}

func TestDesktopDisplaysFirstTapAfterSwitchIsSingleClick(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	image := p.Image
	p.Trackpad = true
	tap(m, p, 100, 100)
	p.InvalidateTarget()
	// Simulate receiving the first frame of the next selected desktop.
	p.Active, p.Image, p.Sending = true, image, true
	tap(m, p, 100, 100)
	if len(p.Queue) != 1 || p.Queue[0].Kind != "click" {
		t.Fatal("the first tap on another display became a double click", p.Queue)
	}
}

type displayRoundTripFunc func(*http.Request) (*http.Response, error)

func (f displayRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDesktopDisplaysDiscardInFlightFrameAfterSwitch(t *testing.T) {
	_, wait := unlockUI(t)
	d, token, displays, _ := controltest.Display(t)
	frame := func(c color.RGBA) []byte {
		im := image.NewRGBA(image.Rect(0, 0, 20, 20))
		for y := 0; y < 20; y++ {
			for x := 0; x < 20; x++ {
				im.SetRGBA(x, y, c)
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, im, nil); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	oldFrame, newFrame := frame(color.RGBA{R: 200, A: 255}), frame(color.RGBA{B: 200, A: 255})
	d.CaptureDisplay = func(display desktop.Display) ([]byte, error) {
		if display.ID == displays[0].ID {
			return oldFrame, nil
		}
		return newFrame, nil
	}
	client := controltest.Client(t, d, token)
	ctx := context.Background()
	if _, err := client.SelectDisplay(ctx, displays[0]); err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var delay atomic.Bool
	base := client.Transport()
	client.SetTransport(displayRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if strings.HasSuffix(r.URL.Path, "/frame") && delay.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return response, err
	}))
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Client, p.Pickable, p.DisplaysAvailable, p.Display, p.Displays, p.Image = client, true, true, displays[0], displays, nil
	m.DesktopPoll(p)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old frame did not start")
	}
	m.DesktopSelectDisplay(p, displays[1])
	wait(func() bool { return p.Phase == "live" && p.Image != nil })
	expected := sha256.Sum256(newFrame)
	if p.ImageHash != expected {
		t.Fatal("new display did not load")
	}
	close(release)
	// Drain the delayed old response's main-thread callback.
	callbacks := make(chan struct{})
	go func() { time.Sleep(50 * time.Millisecond); uiruntime.RunOnMain(func() { close(callbacks) }) }()
	wait(func() bool {
		select {
		case <-callbacks:
			return true
		default:
			return false
		}
	})
	if p.ImageHash != expected || p.Display != displays[1] {
		t.Fatal("old frame replaced selected display")
	}
	p.Cancel()
}
