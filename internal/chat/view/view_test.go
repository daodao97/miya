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

func TestActivityRunsExpandDuringCurrentTurn(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		name, width := "desktop", 744
		if mobile {
			name, width = "phone", 390
		}
		t.Run(name, func(t *testing.T) {
			host := &snapshotHost{thread: model.Thread{ID: 1, Busy: true, TurnID: "live", Messages: []model.Message{
				{ID: 1, Role: "user", Content: "previous"},
				{ID: 2, Role: "tool", TurnID: "old", Kind: "commandExecution", Status: "inProgress", Item: &model.Activity{Command: "old-one"}},
				{ID: 3, Role: "tool", TurnID: "old", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "old-two"}},
				{ID: 4, Role: "tool", TurnID: "old", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "old-three"}},
				{ID: 5, Role: "user", Content: "current"},
				{ID: 6, Role: "tool", TurnID: "live", Kind: "commandExecution", Status: "completed", Item: &model.Activity{Command: "live-one"}},
				{ID: 7, Role: "tool", TurnID: "live", Kind: "commandExecution", Status: "inProgress", Item: &model.Activity{Command: "live-two"}},
			}}}
			v := NewConversationView(host)
			v.Mobile = mobile
			tt := ui.NewTester(func(c *ui.Context) {
				for _, g := range msgfmt.ThreadMessageGroups(host.thread, msgfmt.GroupMessages(host.thread.Messages)) {
					v.GroupView(c.Key(g.ID), g)
				}
			}, width, 600)
			assertVisible := func(active bool) {
				t.Helper()
				if tt.HasText("Ran live-one") != active || tt.HasText("Running live-two") != active {
					t.Fatalf("current activities visible = %v: %v", active, tt.Texts())
				}
				if tt.HasText("Running old-one") || tt.HasText("Ran old-two") || tt.HasText("Ran old-three") {
					t.Fatalf("historical activities expanded: %v", tt.Texts())
				}
			}
			assertVisible(true)
			// A running group's header cannot hide its activities or save an
			// expansion that would leave it open after the turn finishes.
			header, ok := tt.Find("2 activities")
			if !ok {
				t.Fatal("current activity header missing")
			}
			tt.ClickAt(header.X+header.W/2, header.Y+header.H/2)
			assertVisible(true)
			host.thread.Busy = false
			tt.Frame()
			assertVisible(false)
			// Reopening history must ignore stale inProgress item statuses.
			v = NewConversationView(host)
			v.Mobile = mobile
			tt.Frame()
			assertVisible(false)
		})
	}
}
