//go:build darwin && !ios

package integration

import (
	"bytes"
	"flag"
	"fmt"
	"image/jpeg"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
)

var nativeQAText string // UI thread only
var nativeQAScroll ui.ScrollState
var nativeQAScrollRect ui.Rect
var nativeQAWindow *mygo.Window
var nativeQAAction atomic.Int32

func TestMain(m *testing.M) {
	if os.Getenv("MIYA_DESKTOP_QA_DIR") == "" {
		os.Exit(m.Run())
	}
	_ = flag.Set("test.run", "^TestDesktopNativeQA$")
	_ = flag.Set("test.v", "true")
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: "Miya Desktop QA", Width: 600, Height: 400, Content: ui.View(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
				ui.Text(c, "Remote desktop input fixture").FontSize(22)
				ui.TextArea(c, &nativeQAText).Grow(1).FillWidth().Label("QA input").Placeholder("Disposable test input")
				list := ui.Scroll(c).FillWidth().Height(80).Shrink(0).TrackScroll(&nativeQAScroll)
				nativeQAScrollRect = list.Bounds()
				list.Children(func() {
					ui.Column(c).FillWidth().Children(func() {
						for i := range 20 {
							ui.Textf(c, "Scroll row %d", i).Height(24).Shrink(0)
						}
					})
				})
				ui.Button(c, "QA action").OnClick(func() { nativeQAAction.Add(1) })
			})
		})})
		nativeQAWindow = w
		w.OnFirstFrame(func() { go func() { os.Exit(m.Run()) }() })
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Opt in: every event is directed to the disposable window in this process.
func TestDesktopNativeQA(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_QA_DIR") == "" {
		t.Skip("opt-in real window input")
	}
	waitFor := func(what string, check func() bool) {
		t.Helper()
		guard := time.NewTimer(time.Minute)
		defer guard.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !check() {
			select {
			case <-guard.C:
				t.Fatalf("%s did not happen", what)
			case <-tick.C:
			}
		}
	}
	var window desktop.Window
	waitFor("fixture window", func() bool {
		ws, err := desktop.Windows()
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws {
			if w.PID == os.Getpid() && w.Title == "Miya Desktop QA" {
				window = w
				return true
			}
		}
		return false
	})
	nativeQAWindow.Focus()
	waitFor("fixture focus", func() bool { return nativeQAWindow.IsFocused() })
	send := func(in desktop.Input) {
		t.Helper()
		if err := desktop.SendWindowInput(window, in); err != nil {
			t.Fatal(err)
		}
	}
	send(desktop.Input{Kind: "click", X: 0.5, Y: 0.5})
	const text = "purego input: 你好 👋 surrogate pairs and a second chunk"
	send(desktop.Input{Kind: "text", Text: text})
	waitFor("Unicode text input", func() bool {
		var got string
		mygo.RunOnMain(func() { got = nativeQAText })
		return got == text
	})
	t.Log("window click and multi-chunk Unicode input verified")
	send(desktop.Input{Kind: "key", Key: "a", Modifier: "command"})
	send(desktop.Input{Kind: "text", Text: "replacement verified"})
	waitFor("command-key selection", func() bool {
		var got string
		mygo.RunOnMain(func() { got = nativeQAText })
		return got == "replacement verified"
	})
	d, err := desktop.WindowDialog(window)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Title+d.Message, "replacement verified") {
		t.Fatal("editable contents read as dialog text")
	}
	found := false
	for _, a := range d.Actions {
		if a.Label == "QA action" {
			found = true
		}
	}
	if !found {
		t.Fatal("fixture action missing")
	}
	if err := desktop.PressWindowAction(window, "QA action"); err != nil {
		t.Fatal(err)
	}
	waitFor("AX button action", func() bool { return nativeQAAction.Load() == 1 })
	t.Log("command key, dialog isolation and AX button press verified")
	var scrollX, scrollY float64
	mygo.RunOnMain(func() {
		content, frame := nativeQAWindow.ContentBounds(), nativeQAWindow.Bounds()
		r := nativeQAScrollRect
		scrollX = (float64(content.X-frame.X) + float64(r.X+r.W/2)) / float64(frame.Width-1)
		scrollY = (float64(content.Y-frame.Y) + float64(r.Y+r.H/2)) / float64(frame.Height-1)
	})
	t.Logf("native scroll point: %.3f, %.3f", scrollX, scrollY)
	send(desktop.Input{Kind: "scroll", X: scrollX, Y: scrollY, DeltaY: -80})
	waitFor("native list scroll", func() bool {
		var offset float32
		mygo.RunOnMain(func() { offset = nativeQAScroll.Y })
		return offset > 0
	})
	t.Log("native wheel event scrolled the fixture list")

	b, err := desktop.CaptureWindow(window)
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MIYA_DESKTOP_QA_DIR")+"/fixture.jpg", b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture JPEG: %dx%d, %d bytes", config.Width, config.Height, len(b))
}
