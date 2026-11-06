package integration

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"

	"miya/internal/mobile"
	"miya/internal/model"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
)

func TestMobileHistoryReloadReusesMarkdownViews(t *testing.T) {
	m := testMobileApp()
	desk := model.Thread{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []model.Message{
		{ID: 10, Role: "user", TurnID: "turn1", ItemID: "user1", Content: "Explain this"},
		{ID: 11, Role: "assistant", TurnID: "turn1", ItemID: "answer1", Content: "Previous **answer**\n\n```go\nprintln(1)\n```"},
	}}
	m.OpenThread(desk)
	m.ShowThread(desk)
	documents := map[*parser.Document]bool{}
	m.View.Md.Block = func(_ *ui.Context, d *parser.Document, _ int) bool {
		documents[d] = true
		return false
	}
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if len(documents) != 2 {
		t.Fatalf("expected two rendered message documents, got %d", len(documents))
	}
	// Resuming on send replaces the read-only history's runtime IDs.
	messages := append([]model.Message(nil), desk.Messages...)
	for i := range messages {
		messages[i].ID += 100
	}
	m.ReceiveThread(1, phoneproto.ThreadDelta{Thread: model.ListThread(desk), Reset: true,
		Order: []int{110, 111}, Changed: messages})
	tt.Frame()
	if len(documents) != 2 {
		t.Fatalf("history reload rebuilt parsed Markdown: %d documents", len(documents))
	}
	// A genuinely new prompt still gets its own view and keeps the old ones.
	m.ReceiveThread(1, phoneproto.ThreadDelta{Thread: model.ListThread(desk), Order: []int{110, 111, 112},
		Changed: []model.Message{{ID: 112, Role: "user", TurnID: "turn2", ItemID: "user2", Content: "Follow-up"}}})
	tt.Frame()
	if len(documents) != 3 || !tt.HasText("Follow-up") || !tt.HasText("Previous") {
		t.Fatalf("new prompt replaced the existing views: %d documents, %q", len(documents), tt.Texts())
	}
}

func TestMobileMetadataUpdateReusesHistory(t *testing.T) {
	m := mobile.NewMobileApp()
	desk := longThread(1000)
	m.Open = desk.ID
	s := phoneserver.NewThreadSync(nil)
	d, _ := s.Update(desk)
	m.ReceiveThread(desk.ID, d)
	before := m.Viewer.Current()
	groups := m.View.Groups.Of(m.Viewer)
	revision := m.Viewer.Revision()
	m.CacheDir = t.TempDir()
	t.Cleanup(func() { m.FlushCache(true) })
	desk.Tokens, desk.Title = 123, "Updated title"
	d, _ = s.Update(desk)
	m.ReceiveThread(desk.ID, d)
	cur := m.Viewer.Current()
	if cur.Tokens != 123 || m.Title != "Updated title" {
		t.Fatal("metadata was not applied")
	}
	if &cur.Messages[0] != &before.Messages[0] || &m.View.Groups.Of(m.Viewer)[0] != &groups[0] || m.Viewer.Revision() != revision {
		t.Fatal("metadata update rebuilt unchanged history")
	}
	if len(m.Dirty) != 0 {
		t.Fatal("metadata update scheduled a full message-cache rewrite")
	}
	// Busy changes must still refresh how the last response is presented.
	desk.Busy = true
	d, _ = s.Update(desk)
	m.ReceiveThread(desk.ID, d)
	groups = m.View.Groups.Of(m.Viewer)
	if !groups[len(groups)-1].Streaming {
		t.Fatal("reusing messages hid the running turn")
	}
	desk.Messages = append([]model.Message(nil), desk.Messages...)
	desk.Messages[len(desk.Messages)-1].Content = "Updated reply"
	d, _ = s.Update(desk)
	m.ReceiveThread(desk.ID, d)
	cur = m.Viewer.Current()
	if cur.Messages[len(cur.Messages)-1].Content != "Updated reply" || m.Viewer.Revision() == revision || len(m.Dirty) == 0 {
		t.Fatal("changed messages were not rendered and persisted")
	}
}

func BenchmarkMobileThreadUpdate(b *testing.B) {
	for _, changed := range []bool{false, true} {
		b.Run(fmt.Sprintf("changed=%v", changed), func(b *testing.B) {
			m := testMobileApp()
			desk := longThread(1000)
			m.OpenThread(desk)
			s := phoneserver.NewThreadSync(nil)
			d, _ := s.Update(desk)
			m.ReceiveThread(desk.ID, d)
			m.View.Groups.Of(m.Viewer)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.Thread.Tokens = i
				d.Reset, d.Changed = false, nil
				if changed {
					msg := desk.Messages[len(desk.Messages)-1]
					msg.Content = fmt.Sprintf("Streamed update %d", i)
					d.Changed = []model.Message{msg}
				}
				m.ReceiveThread(desk.ID, d)
				m.View.Groups.Of(m.Viewer)
			}
		})
	}
}

func BenchmarkMobileSendFrame(b *testing.B) {
	for _, renumber := range []bool{false, true} {
		b.Run(fmt.Sprintf("renumber=%v", renumber), func(b *testing.B) {
			m := testMobileApp()
			desk := longThread(100)
			for i := range desk.Messages {
				desk.Messages[i].ItemID = fmt.Sprintf("item-%d", i)
				desk.Messages[i].TurnID = fmt.Sprintf("turn-%d", i/2)
			}
			m.OpenThread(desk)
			m.ShowThread(desk)
			tt := ui.NewTester(m.ViewRoot, 390, 750)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Sending = i%2 == 0
				if renumber {
					for j := range desk.Messages {
						desk.Messages[j].ID++
					}
					m.ShowThread(desk)
				}
				tt.Frame()
			}
		})
	}
}
