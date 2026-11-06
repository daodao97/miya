package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/widgets"
)

func TestTurnStatusKeepsPositionAfterCompletion(t *testing.T) {
	for _, surface := range []string{"desktop", "phone-320", "phone-390"} {
		t.Run(surface, func(t *testing.T) {
			start := time.Now().Add(-10 * time.Second).UnixMilli()
			thread := model.Thread{ID: 1, ServerID: "s1", Title: "Status", TurnID: "current", Busy: true, HistoryLoaded: true, Loaded: true, StartedAtMs: start, Messages: []model.Message{
				{ID: 1, Role: "user", Content: "Earlier question", TurnID: "earlier"},
				{ID: 2, Role: "assistant", Content: "Earlier reply", TurnID: "earlier", StartedAtMs: 1000, CompletedAtMs: 4000},
				{ID: 3, Role: "user", Content: "Latest question", TurnID: "current"},
				{ID: 4, Role: "assistant", Content: "Latest reply", TurnID: "current", StartedAtMs: start},
			}}
			var tt *ui.Tester
			var setThread func(model.Thread)
			var pane *widgets.CollaborationPanel
			if surface == "desktop" {
				app, tester := testApp(t)
				tt = tester
				app.C.(*controller.Controller).CollaborationRemote = &model.CollaborationStatus{Version: 1}
				setThread = func(th model.Thread) {
					app.C.(*controller.Controller).SetThreads([]model.Thread{th})
					app.C.(*controller.Controller).SetActive(th.ID)
					app.C.(*controller.Controller).SetRevision(app.C.(*controller.Controller).Revision() + 1)
				}
				setThread(thread)
				pane, _, _ = app.CollaborationPane(thread)
			} else {
				m := testMobileApp()
				m.State.Collaboration = &model.CollaborationStatus{Version: 1}
				m.OpenThread(thread)
				setThread = func(th model.Thread) { m.State.Projects[0].Threads[0] = th; m.ShowThread(th) }
				setThread(thread)
				pane, _, _ = m.CollaborationPane()
				width := 390
				if surface == "phone-320" {
					width = 320
				}
				tt = ui.NewTester(m.ViewRoot, width, 750)
			}
			pane.Loaded, pane.LoadedAt = true, time.Now()
			pane.Rows = []widgets.CollaborationTaskView{{ID: "review", Title: "Review", Agent: appmodel.AgentCodex}}
			tt.Frame()
			tt.Frame()
			working := ""
			for _, text := range tt.Texts() {
				if strings.HasPrefix(text, "Working for ") {
					working = text
					break
				}
			}
			before, ok := tt.Find(working)
			if !ok || working == "" {
				t.Fatal("missing active status", tt.Texts())
			}
			toggleBefore, ok := tt.Find("Agent tasks (1)")
			if !ok {
				t.Fatal("missing task toggle")
			}
			saveMobileNativeSnapshot(t, tt, "turn-status-working-"+surface)
			thread.Busy, thread.CompletedAtMs = false, start+10000
			thread.Messages = append([]model.Message(nil), thread.Messages...)
			thread.Messages[3].CompletedAtMs = thread.CompletedAtMs
			thread.Messages = append(thread.Messages, model.Message{ID: 5, Role: "tool", Kind: "diff", TurnID: "current", Content: "+one\n+two\n-old"})
			setThread(thread)
			tt.Frame()
			tt.Frame()
			after, ok := tt.Find("Worked for 10s")
			if !ok {
				t.Fatal("missing completed status", tt.Texts())
			}
			toggleAfter, ok := tt.Find("Agent tasks (1)")
			if !ok {
				t.Fatal("task toggle disappeared")
			}
			if before.X != after.X || before.Y != after.Y || before.H != after.H || toggleBefore != toggleAfter {
				t.Fatalf("status or toggle moved: %v / %v; %v / %v", before, after, toggleBefore, toggleAfter)
			}
			count := 0
			for _, text := range tt.Texts() {
				if text == "Worked for 10s" {
					count++
				}
			}
			// The status widget contributes a label and text, with no duplicate
			// duration below the latest reply. Historical timing remains there.
			if count != 2 || !tt.HasText("Worked for 3s") {
				t.Fatal("duplicate latest duration or missing historical timing", tt.Texts())
			}
			if tt.HasText(working) {
				t.Fatal("active status survived completion")
			}
			for _, label := range []string{"+2", "−1"} {
				r, ok := tt.Find(label)
				if !ok || r.X < after.X+after.W || r.X+r.W > toggleAfter.X {
					t.Fatalf("diff is not after elapsed time: %s %v / %v", label, r, after)
				}
				if middle := after.Y + after.H/2; middle < r.Y || middle > r.Y+r.H {
					t.Fatalf("diff is not on the status row: %s %v / %v", label, r, after)
				}
			}
			if surface == "desktop" {
				for _, label := range []string{"Copy response", "Hand off to a new conversation"} {
					r, ok := tt.Find(label)
					if middle := after.Y + after.H/2; !ok || middle < r.Y || middle > r.Y+r.H {
						t.Fatalf("%s stayed below the response: %v / %v", label, r, after)
					}
				}
			}
			tt.Advance(time.Second)
			tt.Frame()
			if !tt.HasText("Worked for 10s") {
				t.Fatalf("completed duration kept counting: %v", tt.Texts())
			}
			saveMobileNativeSnapshot(t, tt, "turn-status-worked-"+surface)
			if err := tt.Click("Agent tasks (1)"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			popup, ok := tt.Find("Agent tasks")
			if !ok || !pane.Open || popup.Y+popup.H > toggleAfter.Y-7 || popup.X < 0 || popup.Y < 0 {
				t.Fatalf("task popover did not open above its button: popup=%v toggle=%v", popup, toggleAfter)
			}
			if root := tt.Image().Bounds(); popup.X+popup.W > float32(root.Dx()) {
				t.Fatalf("task popover exceeds viewport: %v / %v", popup, root)
			}
			if r, _ := tt.Find("Agent tasks (1)"); r != toggleAfter {
				t.Fatalf("popover displaced status/composer: %v / %v", r, toggleAfter)
			}
			saveMobileNativeSnapshot(t, tt, "agent-task-popover-"+surface)
			tt.ClickAt(1, 1)
			tt.Frame()
			if pane.Open || tt.HasText("Refresh") {
				t.Fatal("outside press did not dismiss tasks")
			}
			if err := tt.Click("Agent tasks (1)"); err != nil {
				t.Fatal(err)
			}
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if pane.Open {
				t.Fatal("Escape did not dismiss tasks")
			}
		})
	}
}
