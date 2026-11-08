package client

import (
	"testing"

	"miya/internal/chat/model"
	"miya/internal/contacts"
)

func TestMirrorRemapMovesDraftsAndMessagesBeforeNumberReuse(t *testing.T) {
	first := model.Thread{ID: 1, Contact: &contacts.Session{ID: "first"}}
	second := model.Thread{ID: 2, Contact: &contacts.Session{ID: "second"}}
	m := Mirror{Drafts: map[int]string{1: "Draft for first", 2: ""}, Messages: map[int][]model.Message{1: {{Content: "First answer"}}, 2: {{Content: "Second answer"}}}}
	a, b := first, second
	a.ID, b.ID = 2, 1
	m.RemapContacts([]model.Thread{first, second}, []model.Thread{a, b}, true)
	if m.Drafts[2] != "Draft for first" || m.Drafts[1] != "" || m.Messages[2][0].Content != "First answer" || m.Messages[1][0].Content != "Second answer" {
		t.Fatal("renumbering mixed conversations")
	}
	if _, ok := m.Drafts[1]; !ok {
		t.Fatal("explicitly cleared draft was lost")
	}
	m.RemapContacts([]model.Thread{a, b}, []model.Thread{a, b}, false)
	if len(m.Drafts) != 0 || len(m.Messages) != 0 {
		t.Fatal("cached content leaked between service sources")
	}
}
