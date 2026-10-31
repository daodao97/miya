package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestReconnectStateReachesDesktopAndPhone(t *testing.T) {
	for _, surface := range []string{"desktop", "phone"} {
		t.Run(surface, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.c
			c.serial = 10
			c.setThreads([]Thread{{ID: 1, ServerID: "t1", Title: "Reconnect test", HistoryLoaded: true,
				Messages: []Message{{ID: 1, Role: "user", Content: "Continue the task"}}}})
			c.setActive(1)
			syncView := func() {}
			if surface == "phone" {
				m := testMobileApp()
				m.openThread(c.current())
				tt = ui.NewTester(m.viewRoot, 390, 750)
				s := newPhoneThreadSync(nil)
				syncView = func() {
					d, changed := s.update(c.current())
					if changed {
						// Exercise the actual wire format, including status-only deltas.
						data, _ := json.Marshal(d)
						var received phoneThreadDelta
						if err := json.Unmarshal(data, &received); err != nil {
							t.Fatal(err)
						}
						m.receiveThread(1, received)
					}
				}
			}
			frame := func() { syncView(); tt.Frame() }
			c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
			c.handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"Reconnecting... 4/5"}}`))
			frame()
			label := tr("Reconnecting…") + " 4/5"
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
			c.handle(event("item/commandExecution/outputDelta", `{"threadId":"t1","turnId":"turn1","itemId":"cmd","delta":"Background command output"}`))
			if threadReconnectLabel(c.current()) == "" {
				t.Fatal("background tool output incorrectly ended reconnection")
			}
			c.handle(event("error", `{"threadId":"t1","turnId":"old-turn","willRetry":true,"error":{"message":"Reconnecting... 1/5"}}`))
			if c.current().Status != "Retrying: Reconnecting... 4/5" {
				t.Fatal("stale retry overwrote the current attempt")
			}
			c.handle(event("item/agentMessage/delta", `{"threadId":"t1","turnId":"turn1","itemId":"reply","delta":"Connection restored"}`))
			frame()
			if _, ok := tt.Find(label); ok || c.current().Status != "Working…" {
				t.Fatal("retry indicator remained after model output resumed")
			}
			c.handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"Reconnecting... 5/5"}}`))
			frame()
			if _, ok := tt.Find(tr("Reconnecting…") + " 5/5"); !ok {
				t.Fatal("retry indicator disappeared before recovery or failure")
			}
			c.handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":false,"error":{"message":"Connection lost after all retries"}}`))
			// Some servers only include the error on the preceding notification.
			c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed"}}`))
			frame()
			if c.current().Busy || c.current().Status != "Connection lost after all retries" {
				t.Fatal("terminal failure lost the error or stayed busy")
			}
			if _, ok := tt.Find(tr("Reconnecting…") + " 5/5"); ok {
				t.Fatal("failed turn still shows reconnection")
			}
			if !tt.HasText("Connection lost after all retries") {
				t.Fatalf("terminal error is invisible: %q", tt.Texts())
			}
			errors := 0
			for _, m := range c.current().Messages {
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
		c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
		c.handle(event("error", `{"threadId":"t1","turnId":"turn1","willRetry":true,"error":{"message":"temporary"}}`))
		if got := threadWorkingLabel(c.threads()[0]); got != tr("Reconnecting…") {
			t.Fatalf("generic retry label = %q", got)
		}
		c.handle(event(progress.method, progress.params))
		if strings.HasPrefix(c.threads()[0].Status, "Retrying:") {
			t.Fatal("model activity did not clear retry status")
		}
		c.handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"failed"}}`))
		th := c.threads()[0]
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
