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
