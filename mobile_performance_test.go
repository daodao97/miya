package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestMobileHistoryReloadReusesMarkdownViews(t *testing.T) {
	m := testMobileApp()
	desk := Thread{ID: 1, ServerID: "s1", HistoryLoaded: true, Messages: []Message{
		{ID: 10, Role: "user", TurnID: "turn1", ItemID: "user1", Content: "Explain this"},
		{ID: 11, Role: "assistant", TurnID: "turn1", ItemID: "answer1", Content: "Previous **answer**\n\n```go\nprintln(1)\n```"},
	}}
	m.openThread(desk)
	m.showThread(desk)
	documents := map[*parser.Document]bool{}
	m.view.md.Block = func(_ *ui.Context, d *parser.Document, _ int) bool {
		documents[d] = true
		return false
	}
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if len(documents) != 2 {
		t.Fatalf("expected two rendered message documents, got %d", len(documents))
	}
	// Resuming on send replaces the read-only history's runtime IDs.
	messages := append([]Message(nil), desk.Messages...)
	for i := range messages {
		messages[i].ID += 100
	}
	m.receiveThread(1, phoneThreadDelta{Thread: listThread(desk), Reset: true,
		Order: []int{110, 111}, Changed: messages})
	tt.Frame()
	if len(documents) != 2 {
		t.Fatalf("history reload rebuilt parsed Markdown: %d documents", len(documents))
	}
	// A genuinely new prompt still gets its own view and keeps the old ones.
	m.receiveThread(1, phoneThreadDelta{Thread: listThread(desk), Order: []int{110, 111, 112},
		Changed: []Message{{ID: 112, Role: "user", TurnID: "turn2", ItemID: "user2", Content: "Follow-up"}}})
	tt.Frame()
	if len(documents) != 3 || !tt.HasText("Follow-up") || !tt.HasText("Previous") {
		t.Fatalf("new prompt replaced the existing views: %d documents, %q", len(documents), tt.Texts())
	}
}

func TestMobileMetadataUpdateReusesHistory(t *testing.T) {
	m := newMobileApp()
	desk := longThread(1000)
	m.open = desk.ID
	s := newPhoneThreadSync(nil)
	d, _ := s.Update(desk)
	m.receiveThread(desk.ID, d)
	before := m.viewer.current()
	groups := m.view.groups.of(m.viewer)
	revision := m.viewer.revision()
	m.cacheDir = t.TempDir()
	t.Cleanup(func() { m.flushCache(true) })
	desk.Tokens, desk.Title = 123, "Updated title"
	d, _ = s.Update(desk)
	m.receiveThread(desk.ID, d)
	cur := m.viewer.current()
	if cur.Tokens != 123 || m.title != "Updated title" {
		t.Fatal("metadata was not applied")
	}
	if &cur.Messages[0] != &before.Messages[0] || &m.view.groups.of(m.viewer)[0] != &groups[0] || m.viewer.revision() != revision {
		t.Fatal("metadata update rebuilt unchanged history")
	}
	if len(m.dirty) != 0 {
		t.Fatal("metadata update scheduled a full message-cache rewrite")
	}
	// Busy changes must still refresh how the last response is presented.
	desk.Busy = true
	d, _ = s.Update(desk)
	m.receiveThread(desk.ID, d)
	groups = m.view.groups.of(m.viewer)
	if !groups[len(groups)-1].Streaming {
		t.Fatal("reusing messages hid the running turn")
	}
	desk.Messages = append([]Message(nil), desk.Messages...)
	desk.Messages[len(desk.Messages)-1].Content = "Updated reply"
	d, _ = s.Update(desk)
	m.receiveThread(desk.ID, d)
	cur = m.viewer.current()
	if cur.Messages[len(cur.Messages)-1].Content != "Updated reply" || m.viewer.revision() == revision || len(m.dirty) == 0 {
		t.Fatal("changed messages were not rendered and persisted")
	}
}

func BenchmarkMobileThreadUpdate(b *testing.B) {
	for _, changed := range []bool{false, true} {
		b.Run(fmt.Sprintf("changed=%v", changed), func(b *testing.B) {
			m := testMobileApp()
			desk := longThread(1000)
			m.openThread(desk)
			s := newPhoneThreadSync(nil)
			d, _ := s.Update(desk)
			m.receiveThread(desk.ID, d)
			m.view.groups.of(m.viewer)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.Thread.Tokens = i
				d.Reset, d.Changed = false, nil
				if changed {
					msg := desk.Messages[len(desk.Messages)-1]
					msg.Content = fmt.Sprintf("Streamed update %d", i)
					d.Changed = []Message{msg}
				}
				m.receiveThread(desk.ID, d)
				m.view.groups.of(m.viewer)
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
			m.openThread(desk)
			m.showThread(desk)
			tt := ui.NewTester(m.viewRoot, 390, 750)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.sending = i%2 == 0
				if renumber {
					for j := range desk.Messages {
						desk.Messages[j].ID++
					}
					m.showThread(desk)
				}
				tt.Frame()
			}
		})
	}
}
