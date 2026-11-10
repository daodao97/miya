package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

func TestMobileUserMessageBlankSpaceKeepsVerticalScroll(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded = true
			for i := 0; i < 40; i++ {
				th.Messages = append(th.Messages, model.Message{ID: i + 1, Role: "user", Content: fmt.Sprintf("消息 %02d", i)})
			}
			m.OpenThread(th)
			m.ShowThread(th)
			m.Draft = "保留草稿"
			tt := ui.NewTester(m.ViewRoot, width, 750)
			setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
			tt.Frame()
			list := m.View.ListFor(th.ID)
			list.ScrollTo(20, ui.Center)
			tt.Frame()
			bubble, ok := tt.Find("消息 20")
			if !ok || bubble.X < 80 {
				t.Fatalf("short message has no left blank space: %v", bubble)
			}
			x, y := float32(60), bubble.Y+bubble.H/2
			tt.Press(x, y)
			tt.Move(x, y-40)
			tt.Frame()
			if m.DrawerDragging || m.DrawerOffset.X != 0 || m.Menu {
				t.Fatal("vertical movement in message whitespace was captured by the drawer")
			}
			// Native touch scrolling delivers precise scroll events after
			// the press yields; exercise both directions at the same origin.
			tt.Scroll(x, y, 0, -100)
			moved, ok := tt.Find("消息 20")
			if !ok || moved.Y < bubble.Y+50 {
				t.Fatalf("blank space did not scroll toward earlier messages: before=%v after=%v", bubble, moved)
			}
			tt.Scroll(x, y, 0, 100)
			restored, ok := tt.Find("消息 20")
			if !ok || restored.Y != bubble.Y {
				t.Fatalf("blank space did not scroll back: before=%v after=%v", bubble, restored)
			}
			tt.Release(x, y-40)
			tt.Advance(time.Second)
			if m.Router().Path() != "/thread" || m.Open != th.ID || m.Menu || m.Draft != "保留草稿" {
				t.Fatal("scrolling changed the conversation, drawer or draft")
			}
			drag(tt, 8, float32(width)-40, y)
			if !m.Menu {
				t.Fatal("message scrolling disabled the drawer edge gesture")
			}
		})
	}
}
