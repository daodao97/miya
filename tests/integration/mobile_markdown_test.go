package integration

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/model"
)

func TestMobileMarkdownTableScrollKeepsConversation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		m := testMobileApp()
		th := m.State.Projects[0].Threads[0]
		th.Busy, th.HistoryLoaded = streaming, true
		th.Messages = []model.Message{{ID: 1, Role: "assistant", Content: "| First column with a long heading | Second column with a long heading | Last column |\n|---|---|---|\n| First value | Second value | Last value |\n\nTable notes."}}
		m.OpenThread(th)
		m.ShowThread(th)
		tt := ui.NewTester(m.ViewRoot, 390, 750)
		tt.Frame()
		tt.Advance(time.Second) // Finish the stream's text reveal on the test clock.
		first, ok := tt.Find("First value")
		if !ok {
			t.Fatal("table did not render")
		}
		second, ok := tt.Find("Second value")
		if !ok {
			t.Fatal("wide table's second column did not render")
		}
		// A text tap focuses the selection proxy on iOS. Its native pan
		// forwards the same precise scroll events as this call.
		tt.ClickAt(first.X+12, first.Y+first.H/2)
		tt.Scroll(first.X+12, first.Y+first.H/2, 240, 0)
		moved, ok := tt.Find("Second value")
		if !ok || moved.X >= second.X-100 || moved.Y != second.Y {
			t.Fatalf("table did not scroll independently: before=%v after=%v", second, moved)
		}
		tt.Scroll(160, first.Y+first.H/2, -240, 0)
		restored, _ := tt.Find("First value")
		if restored.X != first.X || m.Router().Path() != "/thread" || m.Open != th.ID {
			t.Fatal("scrolling back lost the table position or navigated away")
		}
	}
}
