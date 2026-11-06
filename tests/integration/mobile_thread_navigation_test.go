package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/widgets"
)

func TestMobileTaskOpensTargetConversation(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, reusedID := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/reused-ID-%v", width, reusedID), func(t *testing.T) {
				m := testMobileApp()
				m.State.Collaboration = &model.CollaborationStatus{Version: 1}
				source := m.State.Projects[0].Threads[0]
				source.Messages = []model.Message{{ID: 1, Role: "assistant", Content: "Review done", StartedAtMs: 1000, CompletedAtMs: 4000}}
				source.HistoryLoaded = true
				target := m.State.Projects[1].Threads[0]
				target.Agent = appmodel.AgentClaude
				if reusedID {
					target.ID = source.ID
				}
				m.State.Projects[1].Threads[0] = target
				targetRef := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(target), SessionID: target.ServerID}.URL()
				m.OpenThread(source)
				m.ShowThread(source)
				pane, _, _ := m.CollaborationPane()
				pane.Loaded, pane.LoadedAt = true, time.Now()
				pane.Rows = []widgets.CollaborationTaskView{{ID: "review", Title: "Review", State: dg.Completed}}
				pane.Selected = "review"
				pane.Details["review"] = widgets.CollaborationTaskView{ID: "review", Title: "Review", State: dg.Completed, Thread: targetRef}
				// The destination's previously opened task popover must not reappear
				// over its history when navigating from another conversation.
				destination := m.Collaborations.Pane(targetRef)
				destination.Loaded, destination.LoadedAt, destination.Open = true, time.Now(), true
				destination.Rows = pane.Rows
				destination.Selected = "review"
				destination.Details["review"] = pane.Details["review"]
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.Frame()
				if err := tt.Click("Agent tasks (1)"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if err := tt.Click("Open target conversation"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if m.Open != target.ID || m.Viewer.Current().ServerID != target.ServerID || m.Selected != "/b" || m.Viewer.Current().Agent != appmodel.AgentClaude {
					t.Fatalf("did not navigate to exact target: open=%d current=%+v err=%q", m.Open, m.Viewer.Current(), m.SendErr)
				}
				if pane.Open || destination.Open || tt.HasText("Back to tasks") {
					t.Fatal("cached task popover covered destination")
				}
				request := m.SubscribeRequest(m.Open)
				if request.ServerID != target.ServerID || request.Agent != appmodel.AgentClaude || request.Project != "/b" {
					t.Fatalf("wrong subscription: %+v", request)
				}
				target.Messages = []model.Message{{ID: 2, Role: "assistant", Content: "Target conversation history"}}
				m.ReceiveThread(target.ID, phoneproto.ThreadDelta{Thread: target, Reset: true, Order: []int{2}, Changed: target.Messages})
				tt.Frame()
				if !tt.HasText("Target conversation history") {
					t.Fatal("target history did not become visible")
				}
				// Already inside the destination: only the source navigation belongs here.
				destination.Details["review"] = widgets.CollaborationTaskView{ID: "review", Title: "Review", State: dg.Completed, Thread: targetRef, From: pane.From}
				if err := tt.Click("Agent tasks (1)"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if tt.HasText("Open target conversation") || !tt.HasText("Open source conversation") {
					t.Fatal("task navigation offered the current conversation")
				}
				if err := tt.Click("Open source conversation"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if m.Viewer.Current().ServerID != source.ServerID || m.Selected != "/a" || pane.Open || destination.Open {
					t.Fatal("source navigation failed or left a popover open")
				}
			})
		}
	}
}

func TestMobileMessageLinksOpenConversation(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, kind := range []string{"markdown", "assistant-mention", "user-mention", "mixed-user-links"} {
			t.Run(fmt.Sprintf("%d/%s", width, kind), func(t *testing.T) {
				m := testMobileApp()
				source := m.State.Projects[0].Threads[0]
				target := m.State.Projects[1].Threads[0]
				ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(target), SessionID: target.ServerID}
				body := "[Target conversation](" + ref.URL() + ")"
				role := "assistant"
				if strings.Contains(kind, "mention") {
					body = model.ReferenceText(ref, "Target conversation")
				}
				if kind == "user-mention" {
					role = "user"
				}
				if kind == "mixed-user-links" {
					role = "user"
					body = model.ReferenceText(ref, "Reference tag") + " and " + body
				}
				source.Messages = []model.Message{{ID: 1, Role: role, Content: body}}
				source.HistoryLoaded = true
				m.OpenThread(source)
				m.ShowThread(source)
				tt := ui.NewTester(m.ViewRoot, width, 750)
				tt.Frame()
				r, ok := tt.Find("Target conversation")
				if !ok {
					t.Fatal("missing target link", tt.Texts())
				}
				// Markdown exposes its containing paragraph to Find. Tap its words.
				tt.ClickAt(r.X+12, r.Y+r.H/2)
				tt.Frame()
				if m.Open != target.ID || m.Selected != "/b" {
					t.Fatalf("link did not navigate: open=%d err=%q", m.Open, m.SendErr)
				}
			})
		}
	}
}

func TestMobileThreadLinksRejectOtherSourcesAndMissingTargets(t *testing.T) {
	for _, ref := range []model.ThreadRef{
		{Source: strings.Repeat("b", 32), Backend: appmodel.AgentCodex, SessionID: "s2"},
		{Source: testReferenceSource, Backend: appmodel.AgentClaude, SessionID: "s2"},
		{Source: testReferenceSource, Backend: appmodel.AgentCodex, SessionID: "deleted"},
	} {
		m := testMobileApp()
		m.OpenThread(m.State.Projects[0].Threads[0])
		m.Draft = "Keep my draft"
		m.View.Md.OnLink(ref.URL())
		if m.Open != 1 || m.Draft != "Keep my draft" || m.SendErr != "Conversation unavailable" {
			t.Fatal("unavailable link changed conversation or lacked feedback")
		}
	}
}
