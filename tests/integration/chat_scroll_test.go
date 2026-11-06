package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/controller"
)

func scrollTestMessages() []model.Message {
	var messages []model.Message
	for i := 0; i < 40; i++ {
		messages = append(messages, model.Message{ID: i + 100, Role: "user", Content: fmt.Sprintf("history %02d", i)})
	}
	return messages
}

func TestDesktopSendReturnsToLatest(t *testing.T) {
	for _, action := range []string{"queue", "steer", "offline", "command", "empty"} {
		t.Run(action, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			// Loading conversations accept messages into the queue without
			// launching a backend process.
			c.SetThreads([]model.Thread{{ID: 1, Loading: action == "queue" || action == "steer", HistoryLoaded: true, Messages: scrollTestMessages()}})
			c.SetActive(1)
			tt.Frame()
			list := app.ListFor(1)
			list.ScrollTo(10, ui.Center)
			tt.Frame()
			if list.AtEnd() {
				t.Fatal("expected to be reading history before sending")
			}
			draft := "new message"
			if action == "command" {
				draft = "/status"
			} else if action == "empty" {
				draft = ""
			}
			c.RememberDraft(draft)
			app.SendDraft(action == "steer")
			tt.Frame()
			accepted := action == "queue" || action == "steer"
			if list.AtEnd() != accepted {
				t.Fatalf("at end = %v, accepted = %v", list.AtEnd(), accepted)
			}
			if accepted {
				if len(c.Current().Queue) != 1 || c.Draft() != "" {
					t.Fatal("send was not accepted")
				}
				c.Update(1, func(th *model.Thread) {
					th.Messages = append(th.Messages, model.Message{ID: 200, Role: "assistant", Content: "latest reply"})
				})
				tt.Frame()
				tt.Advance(time.Second)
				if !list.AtEnd() || !tt.HasText("latest reply") {
					t.Fatal("sending did not restore following new replies")
				}
			}
		})
	}
}

func TestMobileSentReturnsToLatest(t *testing.T) {
	for _, sendError := range []string{"", "发送失败"} {
		t.Run(fmt.Sprintf("error=%s", sendError), func(t *testing.T) {
			m := testMobileApp()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded, th.Messages = true, scrollTestMessages()
			m.OpenThread(th)
			m.ShowThread(th)
			tt := ui.NewTester(m.ViewRoot, 390, 750)
			tt.Frame()
			list := m.View.ListFor(th.ID)
			list.ScrollTo(10, ui.Center)
			tt.Frame()
			if list.AtEnd() {
				t.Fatal("expected to be reading history before sending")
			}
			m.Draft, m.Sending = "new message", true
			m.Sent(th.ID, sendError)
			tt.Frame()
			if list.AtEnd() != (sendError == "") {
				t.Fatalf("at end = %v after send error %q", list.AtEnd(), sendError)
			}
		})
	}
}
