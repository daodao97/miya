package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func scrollTestMessages() []Message {
	var messages []Message
	for i := 0; i < 40; i++ {
		messages = append(messages, Message{ID: i + 100, Role: "user", Content: fmt.Sprintf("history %02d", i)})
	}
	return messages
}

func TestDesktopSendReturnsToLatest(t *testing.T) {
	for _, action := range []string{"queue", "steer", "offline", "command", "empty"} {
		t.Run(action, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.c
			// Loading conversations accept messages into the queue without
			// launching a backend process.
			c.setThreads([]Thread{{ID: 1, Loading: action == "queue" || action == "steer", HistoryLoaded: true, Messages: scrollTestMessages()}})
			c.setActive(1)
			tt.Frame()
			list := app.listFor(1)
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
			c.rememberDraft(draft)
			app.sendDraft(action == "steer")
			tt.Frame()
			accepted := action == "queue" || action == "steer"
			if list.AtEnd() != accepted {
				t.Fatalf("at end = %v, accepted = %v", list.AtEnd(), accepted)
			}
			if accepted {
				if len(c.current().Queue) != 1 || c.draft() != "" {
					t.Fatal("send was not accepted")
				}
				c.update(1, func(th *Thread) {
					th.Messages = append(th.Messages, Message{ID: 200, Role: "assistant", Content: "latest reply"})
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
			th := m.state.Projects[0].Threads[0]
			th.HistoryLoaded, th.Messages = true, scrollTestMessages()
			m.openThread(th)
			m.showThread(th)
			tt := ui.NewTester(m.viewRoot, 390, 750)
			tt.Frame()
			list := m.view.listFor(th.ID)
			list.ScrollTo(10, ui.Center)
			tt.Frame()
			if list.AtEnd() {
				t.Fatal("expected to be reading history before sending")
			}
			m.draft, m.sending = "new message", true
			m.sent(th.ID, sendError)
			tt.Frame()
			if list.AtEnd() != (sendError == "") {
				t.Fatalf("at end = %v after send error %q", list.AtEnd(), sendError)
			}
		})
	}
}
