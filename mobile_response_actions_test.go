package main

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	conversation "miya/internal/conversation"
	model "miya/internal/model"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"

	ui "github.com/egoist/mygo/ui"
)

func TestMobileResponseActionsWorkWithoutHover(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
			t.Run(fmt.Sprintf("%d/%s", width, agent), func(t *testing.T) {
				m := testMobileApp()
				th := m.State.Projects[0].Threads[0]
				th.Agent = agent
				th.Title = "源会话"
				th.HistoryLoaded = true
				th.Messages = []appmodel.Message{{ID: 10, Role: "assistant", TurnID: "done", Content: "第一段回复", StartedAtMs: 1000, CompletedAtMs: 4000}, {ID: 11, Role: "assistant", TurnID: "done", Content: "第二段回复"}}
				m.State.Projects[0].Threads[0] = th
				m.OpenThread(th)
				m.ShowThread(th)
				tt := ui.NewTester(m.ViewRoot, width, 750)
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
				m.Draft = "继续修复这个问题"
				photo := filepath.Join(t.TempDir(), "photo.png")
				if err := os.WriteFile(photo, []byte("cached photo"), 0600); err != nil {
					t.Fatal(err)
				}
				m.Images = []string{photo}
				// Attachments change the composer height and the anchored status
				// toolbar's position, just as a real UI frame would after picking one.
				tt.Frame()
				if err := tt.Click("Hand off to a new conversation"); err != nil {
					t.Fatal(err)
				}
				want := model.ReferenceText(testRef(agent, th.ServerID), th.Title) + " 继续修复这个问题"
				if m.Open != 0 || m.Viewer.Active() != 0 || m.Draft != want || m.Selected != "/a" || m.NewAgent != agent {
					t.Fatalf("handoff did not create the phone draft: open=%d draft=%q project=%s agent=%s", m.Open, m.Draft, m.Selected, m.NewAgent)
				}
				if len(m.Images) != 1 || m.Images[0] != photo {
					t.Fatal("handoff lost the draft attachment")
				}
				if _, err := os.Stat(photo); err != nil {
					t.Fatal("handoff deleted the draft photo")
				}
				doc := m.InlineInput.Doc
				if doc == nil || conversation.InlineThreadRaw(doc.Text()) != want {
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
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []appmodel.Message{{ID: 1, Role: "assistant", Content: "A reply without timing metadata"}}}})
	app.C.(*controller.Controller).SetActive(1)
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
