package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/model"
)

func TestMobileResponseActionsWorkWithoutHover(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, agent := range []string{agentCodex, agentClaude} {
			t.Run(fmt.Sprintf("%d/%s", width, agent), func(t *testing.T) {
				m := testMobileApp()
				th := m.state.Projects[0].Threads[0]
				th.Agent = agent
				th.Title = "源会话"
				th.HistoryLoaded = true
				th.Messages = []Message{{ID: 10, Role: "assistant", TurnID: "done", Content: "第一段回复", StartedAtMs: 1000, CompletedAtMs: 4000}, {ID: 11, Role: "assistant", TurnID: "done", Content: "第二段回复"}}
				m.state.Projects[0].Threads[0] = th
				m.openThread(th)
				m.showThread(th)
				tt := ui.NewTester(m.viewRoot, width, 750)
				tt.Move(-1, -1)
				tt.Frame()
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("response-actions-%d-%s", width, agent))
				status, ok := tt.Find("Worked for 3s")
				if !ok {
					t.Fatal("missing response status")
				}
				for _, label := range []string{"Copy response", "Hand off to a new conversation"} {
					r, ok := tt.Find(label)
					if !ok || r.W < 44 || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) {
						t.Fatalf("touch action missing or too small: %s %v", label, r)
					}
					if middle := status.Y + status.H/2; middle < r.Y || middle > r.Y+r.H {
						t.Fatalf("%s is not on the status row: %v / %v", label, r, status)
					}
				}
				if err := tt.Click("Copy response"); err != nil {
					t.Fatal(err)
				}
				tt.Move(-1, -1)
				tt.Frame()
				if tt.Clipboard() != "第一段回复\n\n第二段回复" {
					t.Fatalf("copy did not reach phone clipboard: %q", tt.Clipboard())
				}
				m.draft = "继续修复这个问题"
				photo := filepath.Join(t.TempDir(), "photo.png")
				if err := os.WriteFile(photo, []byte("cached photo"), 0600); err != nil {
					t.Fatal(err)
				}
				m.images = []string{photo}
				// Attachments change the composer height and the anchored status
				// toolbar's position, just as a real UI frame would after picking one.
				tt.Frame()
				if err := tt.Click("Hand off to a new conversation"); err != nil {
					t.Fatal(err)
				}
				want := model.ReferenceText(testRef(agent, th.ServerID), th.Title) + " 继续修复这个问题"
				if m.open != 0 || m.view.c.active() != 0 || m.draft != want || m.selected != "/a" || m.newAgent != agent {
					t.Fatalf("handoff did not create the phone draft: open=%d draft=%q project=%s agent=%s", m.open, m.draft, m.selected, m.newAgent)
				}
				if len(m.images) != 1 || m.images[0] != photo {
					t.Fatal("handoff lost the draft attachment")
				}
				if _, err := os.Stat(photo); err != nil {
					t.Fatal("handoff deleted the draft photo")
				}
				doc := m.inlineInput.doc
				if doc == nil || inlineThreadRaw(doc.Text()) != want {
					t.Fatal("new draft did not reach the visible composer")
				}
				if attachment := doc.Text().AttributesAt(0).Attachment; attachment == nil || attachment.Name != th.Title {
					t.Fatal("handoff did not render the source conversation tag")
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("response-handoff-%d-%s", width, agent))
			})
		}
	}
}

func TestLatestResponseToolbarWithoutDuration(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []Message{{ID: 1, Role: "assistant", Content: "A reply without timing metadata"}}}})
	app.c.setActive(1)
	tt.Frame()
	if err := tt.Click("Copy response"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "A reply without timing metadata" {
		t.Fatal(tt.Clipboard())
	}
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Worked for ") {
			t.Fatal("invented response duration", s)
		}
	}
}
