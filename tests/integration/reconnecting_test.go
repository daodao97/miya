package integration

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
)

func TestReconnectStateReachesDesktopAndPhone(t *testing.T) {
	for _, surface := range []string{"desktop", "phone"} {
		t.Run(surface, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			c.Serial = 10
			c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Title: "Reconnect test", HistoryLoaded: true,
				Messages: []model.Message{{ID: 1, Role: "user", Content: "Continue the task"}}}})
			c.SetActive(1)
			syncView := func() {}
			if surface == "phone" {
				m := testMobileApp()
				m.OpenThread(c.Current())
				tt = ui.NewTester(m.ViewRoot, 390, 750)
				s := phoneserver.NewThreadSync(nil)
				syncView = func() {
					d, changed := s.Update(c.Current())
					if changed {
						// Exercise the actual wire format, including status-only deltas.
						data, _ := json.Marshal(d)
						var received phoneproto.ThreadDelta
						if err := json.Unmarshal(data, &received); err != nil {
							t.Fatal(err)
						}
						m.ReceiveThread(1, received)
					}
				}
			}
			frame := func() { syncView(); tt.Frame() }
			c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
			c.Handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"Reconnecting... 4/5"}}`))
			frame()
			label := appmodel.Tr("Reconnecting…") + " 4/5"
			if _, ok := tt.Find(label); !ok {
				t.Fatalf("retry count is hidden by elapsed time: %q", tt.Texts())
			}
			before := tt.Image()
			tt.Advance(300 * time.Millisecond)
			frame()
			if !tt.FramePending() && string(before.Pix) == string(tt.Image().Pix) {
				t.Fatal("reconnecting indicator does not animate")
			}
			saveReconnectSnapshot(t, tt, surface+"-reconnecting")
			c.Handle(event("item/commandExecution/outputDelta", `{"threadId":"t1","turnId":"turn1","itemId":"cmd","delta":"Background command output"}`))
			if msgfmt.ThreadReconnectLabel(c.Current()) == "" {
				t.Fatal("background tool output incorrectly ended reconnection")
			}
			c.Handle(event("error", `{"threadId":"t1","turnId":"old-turn","willRetry":true,"error":{"message":"Reconnecting... 1/5"}}`))
			if c.Current().Status != "Retrying: Reconnecting... 4/5" {
				t.Fatal("stale retry overwrote the current attempt")
			}
			c.Handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"reply","delta":"Connection restored"}`))
			frame()
			if _, ok := tt.Find(label); ok || c.Current().Status != "Working…" {
				t.Fatal("retry indicator remained after model output resumed")
			}
			c.Handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"Reconnecting... 5/5"}}`))
			frame()
			if _, ok := tt.Find(appmodel.Tr("Reconnecting…") + " 5/5"); !ok {
				t.Fatal("retry indicator disappeared before recovery or failure")
			}
			c.Handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":false,"error":{"message":"Connection lost after all retries"}}`))
			// Some servers only include the error on the preceding notification.
			c.Handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed"}}`))
			frame()
			if c.Current().Busy || c.Current().Status != "Connection lost after all retries" {
				t.Fatal("terminal failure lost the error or stayed busy")
			}
			if _, ok := tt.Find(appmodel.Tr("Reconnecting…") + " 5/5"); ok {
				t.Fatal("failed turn still shows reconnection")
			}
			if !tt.HasText("Connection lost after all retries") {
				t.Fatalf("terminal error is invisible: %q", tt.Texts())
			}
			errors := 0
			for _, m := range c.Current().Messages {
				if m.Role == "error" {
					errors++
				}
			}
			if errors != 1 {
				t.Fatalf("retry errors created %d cards", errors)
			}
			saveReconnectSnapshot(t, tt, surface+"-failed")
		})
	}
}

func TestRetryRecoveryAndMissingFailureDetail(t *testing.T) {
	for _, progress := range []struct{ method, params string }{
		{"item/reasoning/summaryTextDelta", `{"threadId":"t1","turnId":"turn1","itemId":"r","delta":"Summary"}`},
		{"item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Recovered"}}`},
	} {
		c := testController()
		c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
		c.Handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"temporary"}}`))
		if got := msgfmt.ThreadWorkingLabel(c.Threads()[0]); got != appmodel.Tr("Reconnecting…") {
			t.Fatalf("generic retry label = %q", got)
		}
		c.Handle(event(progress.method, progress.params))
		if strings.HasPrefix(c.Threads()[0].Status, "Retrying:") {
			t.Fatal("model activity did not clear retry status")
		}
		c.Handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed"}}`))
		th := c.Threads()[0]
		if th.Busy || th.Messages[len(th.Messages)-1].Role != "error" || th.Messages[len(th.Messages)-1].Content == "" {
			t.Fatal("failure without details needs a visible fallback error")
		}
	}
}

func saveReconnectSnapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MIYA_SNAPSHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}
