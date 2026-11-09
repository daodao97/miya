package view

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/interaction"
)

type snapshotHost struct{ thread model.Thread }

func (h *snapshotHost) ViewThread() model.Thread           { return h.thread }
func (*snapshotHost) ViewRevision() int                    { return 0 }
func (*snapshotHost) ViewApprovals() []interaction.Request { return nil }
func (h *snapshotHost) ViewSelectThread(t model.Thread)    { h.thread = t }

func TestMobileUserGroupPreservesTimestampWithoutDesktopHost(t *testing.T) {
	m := model.Message{ID: 1, Role: "user", Content: "A phone message"}
	g := msgfmt.MessageGroup{ID: 1, User: true, Messages: []model.Message{m}, ShowTimestamp: true, Timestamp: time.Now().UnixMilli()}
	v := NewConversationView(&snapshotHost{thread: model.Thread{ID: 1}})
	v.Mobile = true
	tt := ui.NewTester(func(c *ui.Context) { v.GroupView(c, g) }, 390, 400)
	if !tt.HasText(m.Content) || !tt.HasText(msgfmt.FormatMessageTimestamp(g.Timestamp)) {
		t.Fatalf("phone user group lost its message or timestamp: %v", tt.Texts())
	}
	if _, ok := tt.Find("Edit message"); ok {
		t.Fatal("a phone snapshot offered desktop editing")
	}
}

func TestActivityRunsOnlyExpandManually(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name, width := "desktop", 744
		if mobile {
			name, width = "phone", 390
		}
		t.Run(name, func(t *testing.T) {
			host := &snapshotHost{thread: model.Thread{ID: 1, Busy: true, TurnID: "live", Messages: []model.Message{
				{ID: 1, Role: "user", Content: "current"},
				{ID: 2, Role: "tool", TurnID: "live", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "first"}},
				{ID: 3, Role: "tool", TurnID: "live", Kind: "commandExecution", Status: "inProgress", Item: &model.Activity{Command: "second"}},
			}}}
			v := NewConversationView(host)
			v.Mobile = mobile
			tt := ui.NewTester(func(c *ui.Context) {
				for _, g := range msgfmt.ThreadMessageGroups(host.thread, msgfmt.GroupMessages(host.thread.Messages)) {
					v.GroupView(c.Key(g.ID), g)
				}
			}, width, 600)
			assertVisible := func(want bool) {
				t.Helper()
				if tt.HasText("Ran first") != want || (tt.HasText("Running second") || tt.HasText("Ran second")) != want {
					t.Fatalf("activities visible = %v, want %v: %v", !want, want, tt.Texts())
				}
			}
			assertVisible(false)
			if err := tt.Click("2 activities"); err != nil {
				t.Fatal(err)
			}
			assertVisible(true)
			// Completion and incoming body text must preserve a manual expansion.
			host.thread.Messages[2].Status = "completed"
			host.thread.Messages = append(host.thread.Messages, model.Message{ID: 4, Role: "assistant", TurnID: "live", Content: "Result"})
			tt.Frame()
			assertVisible(true)
			host.thread.Busy = false
			tt.Frame()
			assertVisible(true)
			if err := tt.Click("2 activities"); err != nil {
				t.Fatal(err)
			}
			assertVisible(false)
			// Running again, including pending sub-agents, never opens a closed batch.
			host.thread.Busy = true
			host.thread.Messages[2].Agents = []model.SubAgent{{ID: "agent", Status: "running"}}
			tt.Frame()
			assertVisible(false)
			if err := tt.Click("2 activities · 1 Running"); err != nil {
				t.Fatal(err)
			}
			assertVisible(true)
			if err := tt.Click("2 activities · 1 Running"); err != nil {
				t.Fatal(err)
			}
			assertVisible(false)
			// Reopened history starts collapsed as well.
			v = NewConversationView(host)
			v.Mobile = mobile
			tt.Frame()
			assertVisible(false)
		})
	}
}

func TestReasoningVisibleAndExpandable(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name, width := "desktop", 744
		if mobile {
			name, width = "phone", 390
		}
		t.Run(name, func(t *testing.T) {
			host := &snapshotHost{thread: model.Thread{ID: 1, Busy: true, Messages: []model.Message{
				{ID: 1, Role: "activity", Kind: "reasoning", Status: "inProgress", Content: "Checking available libraries."},
			}}}
			v := NewConversationView(host)
			v.Mobile = mobile
			tt := ui.NewTester(func(c *ui.Context) {
				for _, g := range msgfmt.GroupMessages(host.thread.Messages) {
					v.GroupView(c.Key(g.ID), g)
				}
			}, width, 600)
			if !tt.HasText("Thinking…") {
				t.Fatalf("reasoning hidden: %v", tt.Texts())
			}
			if err := tt.Click("Thinking…"); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText("Checking available libraries.") {
				t.Fatalf("reasoning cannot expand: %v", tt.Texts())
			}
			host.thread.Messages[0].Content += " Found a candidate."
			host.thread.Messages[0].Status = "completed"
			tt.Frame()
			if !tt.HasText("Thought") || !tt.HasText("Checking available libraries. Found a candidate.") {
				t.Fatalf("completed reasoning lost: %v", tt.Texts())
			}
			if err := tt.Click("Thought"); err != nil {
				t.Fatal(err)
			}
			if tt.HasText("Checking available libraries. Found a candidate.") {
				t.Fatal("reasoning cannot collapse")
			}
		})
	}
}

func TestReasoningWithoutSummaryHasNoExpandablePlaceholder(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		for _, content := range []string{"", "Thinking…", "Thinking...", "思考中…"} {
			m := model.Message{ID: 1, Role: "activity", Kind: "reasoning", Status: "inProgress", Content: content}
			v := NewConversationView(&snapshotHost{thread: model.Thread{ID: 1}})
			v.Mobile = mobile
			// An old view may already have this placeholder expanded.
			v.Open[m.ID] = true
			tt := ui.NewTester(func(c *ui.Context) { v.MessageView(c, m, false) }, 390, 600)
			if !tt.HasText("Thinking…") || tt.HasText("Thinking...") || tt.HasText("思考中…") {
				t.Fatalf("empty live reasoning lost its status or showed a body: %v", tt.Texts())
			}
			m.Status = "completed"
			tt.Frame()
			if !tt.HasText("Thought") || tt.HasText("Thinking") || tt.HasText("思考中") {
				t.Fatalf("completed reasoning retained placeholder: %v", tt.Texts())
			}
			v.Open[m.ID] = false
			r, ok := tt.Find("Thought")
			if !ok {
				t.Fatal("completed status disappeared")
			}
			tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
			if v.Open[m.ID] {
				t.Fatal("empty reasoning offered expansion")
			}
			// A summary received later restores the normal disclosure control.
			m.Content = "Checked the macOS build flags."
			tt.Frame()
			if err := tt.Click("Thought"); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText(m.Content) {
				t.Fatal("real summary did not expand")
			}
		}
	}
}
