package nsui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/uiruntime"
)

// startDesktop shows the shell as the Mac window does.
func startDesktop(t *testing.T, h Host) *nsgotest.App {
	t.Helper()
	t.Cleanup(func() {
		uiruntime.SetAppWindow(nil)
		current = nil
	})
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		return root(cx, h)
	}, nsgotest.Size(1000, 640), nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
}

// windowY is a view's top in the window, past its scrolled ancestors.
func windowY(v *nsgotest.View) float64 {
	y := 0.0
	for ; v != nil; v = v.Parent {
		y += float64(v.Frame.Origin.Y)
		if v.Parent != nil {
			y -= float64(v.Parent.Offset.Y)
		}
	}
	return y
}

// While a turn runs on the Mac, its status sits just above the composer and
// the transcript ends above both: no message shows behind the status line,
// and no empty row pushes the composer away from it.
func TestDesktopComposerDocksUnderTranscript(t *testing.T) {
	var msgs []model.Message
	for i := 1; i <= 30; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		msgs = append(msgs, model.Message{ID: i, Role: role, Content: strings.Repeat("A long line of the transcript. ", 8), TurnID: fmt.Sprintf("t%d", (i+1)/2)})
	}
	h := newFakeHost(model.Thread{ID: 3, Busy: true, HistoryLoaded: true, StartedAtMs: time.Now().Add(-65 * time.Second).UnixMilli(), Messages: msgs})
	app := startDesktop(t, h)
	app.Settle()
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Working for") {
		t.Fatalf("no working status; texts %q", app.Texts())
	}
	status, card, transcript := app.Find("id:turn-status"), app.Find("id:composer-card"), app.Find("id:chat-transcript")
	statusTop := windowY(status)
	statusBottom := statusTop + float64(status.Frame.Size.Height)
	if gap := windowY(card) - statusBottom; gap < 0 || gap > 12 {
		t.Fatalf("composer is %.1fpt from the status line, want at most 12", gap)
	}
	if bottom := windowY(transcript) + float64(transcript.Frame.Size.Height); bottom > statusTop+0.5 {
		t.Fatalf("transcript ends at %.1f, under the status line at %.1f", bottom, statusTop)
	}
}

// A long conversation title ends in an ellipsis inside the sidebar rather
// than running past its edge.
func TestSidebarLongTitleStaysInside(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 4, Title: strings.Repeat("后台的 UI 细节优化一下，更符合规范 ", 6), HistoryLoaded: true})
	app := startDesktop(t, h)
	app.Settle()
	row, side := app.Find("id:thread-4"), app.Find("id:sidebar-panel")
	sideRight := float64(side.Frame.Origin.X + side.Frame.Size.Width)
	var check func(v *nsgotest.View, x float64)
	check = func(v *nsgotest.View, x float64) {
		x += float64(v.Frame.Origin.X)
		if right := x + float64(v.Frame.Size.Width); right > sideRight+0.5 {
			t.Fatalf("%q reaches %.1f past the sidebar edge %.1f", v.Text, right, sideRight)
		}
		for _, c := range v.Children {
			check(c, x)
		}
	}
	x := 0.0
	for p := row.Parent; p != nil; p = p.Parent {
		x += float64(p.Frame.Origin.X)
	}
	check(row, x)
}
