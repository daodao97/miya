package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	dg "miya/internal/delegation"
	"miya/internal/model"
)

func TestMobileTaskOpensTargetConversation(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, reusedID := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/reused-ID-%v", width, reusedID), func(t *testing.T) {
				m := testMobileApp()
				m.state.Collaboration = &collaborationStatus{Version: 1}
				source := m.state.Projects[0].Threads[0]
				source.Messages = []Message{{ID: 1, Role: "assistant", Content: "Review done", StartedAtMs: 1000, CompletedAtMs: 4000}}
				source.HistoryLoaded = true
				target := m.state.Projects[1].Threads[0]
				target.Agent = agentClaude
				if reusedID {
					target.ID = source.ID
				}
				m.state.Projects[1].Threads[0] = target
				targetRef := ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(target), SessionID: target.ServerID}.URL()
				m.openThread(source)
				m.showThread(source)
				pane, _, _ := m.collaborationPane()
				pane.loaded, pane.loadedAt = true, time.Now()
				pane.rows = []collaborationTaskView{{ID: "review", Title: "Review", State: dg.Completed}}
				pane.selected = "review"
				pane.details["review"] = collaborationTaskView{ID: "review", Title: "Review", State: dg.Completed, Thread: targetRef}
				// The destination's previously opened task popover must not reappear
				// over its history when navigating from another conversation.
				destination := m.collaborations.pane(targetRef)
				destination.loaded, destination.loadedAt, destination.open = true, time.Now(), true
				destination.rows = pane.rows
				destination.selected = "review"
				destination.details["review"] = pane.details["review"]
				tt := ui.NewTester(m.viewRoot, width, 750)
				tt.Frame()
				if err := tt.Click("Agent tasks (1)"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if err := tt.Click("Open target conversation"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if m.open != target.ID || m.view.c.current().ServerID != target.ServerID || m.selected != "/b" || m.view.c.current().Agent != agentClaude {
					t.Fatalf("did not navigate to exact target: open=%d current=%+v err=%q", m.open, m.view.c.current(), m.sendErr)
				}
				if pane.open || destination.open || tt.HasText("Back to tasks") {
					t.Fatal("cached task popover covered destination")
				}
				request := m.subscribeRequest(m.open)
				if request.ServerID != target.ServerID || request.Agent != agentClaude || request.Project != "/b" {
					t.Fatalf("wrong subscription: %+v", request)
				}
				target.Messages = []Message{{ID: 2, Role: "assistant", Content: "Target conversation history"}}
				m.receiveThread(target.ID, phoneThreadDelta{Thread: target, Reset: true, Order: []int{2}, Changed: target.Messages})
				tt.Frame()
				if !tt.HasText("Target conversation history") {
					t.Fatal("target history did not become visible")
				}
				// Already inside the destination: only the source navigation belongs here.
				destination.details["review"] = collaborationTaskView{ID: "review", Title: "Review", State: dg.Completed, Thread: targetRef, From: pane.from}
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
				if m.view.c.current().ServerID != source.ServerID || m.selected != "/a" || pane.open || destination.open {
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
				source := m.state.Projects[0].Threads[0]
				target := m.state.Projects[1].Threads[0]
				ref := ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(target), SessionID: target.ServerID}
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
				source.Messages = []Message{{ID: 1, Role: role, Content: body}}
				source.HistoryLoaded = true
				m.openThread(source)
				m.showThread(source)
				tt := ui.NewTester(m.viewRoot, width, 750)
				tt.Frame()
				r, ok := tt.Find("Target conversation")
				if !ok {
					t.Fatal("missing target link", tt.Texts())
				}
				// Markdown exposes its containing paragraph to Find. Tap its words.
				tt.ClickAt(r.X+12, r.Y+r.H/2)
				tt.Frame()
				if m.open != target.ID || m.selected != "/b" {
					t.Fatalf("link did not navigate: open=%d err=%q", m.open, m.sendErr)
				}
			})
		}
	}
}

func TestMobileThreadLinksRejectOtherSourcesAndMissingTargets(t *testing.T) {
	for _, ref := range []ThreadRef{
		{Source: strings.Repeat("b", 32), Backend: agentCodex, SessionID: "s2"},
		{Source: testReferenceSource, Backend: agentClaude, SessionID: "s2"},
		{Source: testReferenceSource, Backend: agentCodex, SessionID: "deleted"},
	} {
		m := testMobileApp()
		m.openThread(m.state.Projects[0].Threads[0])
		m.draft = "Keep my draft"
		m.view.md.OnLink(ref.URL())
		if m.open != 1 || m.draft != "Keep my draft" || m.sendErr != "Conversation unavailable" {
			t.Fatal("unavailable link changed conversation or lacked feedback")
		}
	}
}
