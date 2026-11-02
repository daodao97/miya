package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestMobileUserMessageBlankSpaceKeepsVerticalScroll(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			th := m.state.Projects[0].Threads[0]
			th.HistoryLoaded = true
			for i := 0; i < 40; i++ {
				th.Messages = append(th.Messages, Message{ID: i + 1, Role: "user", Content: fmt.Sprintf("消息 %02d", i)})
			}
			m.openThread(th)
			m.showThread(th)
			m.draft = "保留草稿"
			tt := ui.NewTester(m.viewRoot, width, 750)
			tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
			tt.Frame()
			list := m.view.listFor(th.ID)
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
			if m.drawerDragging || m.drawerOffset.X != 0 || m.menu {
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
			if m.router().Path() != "/thread" || m.open != th.ID || m.menu || m.draft != "保留草稿" {
				t.Fatal("scrolling changed the conversation, drawer or draft")
			}
			drag(tt, 8, float32(width)-40, y)
			if !m.menu {
				t.Fatal("message scrolling disabled the drawer edge gesture")
			}
		})
	}
}
