package nschat

import (
	"strings"
	"testing"

	re "github.com/nsgo-dev/nsgo/base/richeditor"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/chat/model"
)

func TestRichEditorMidDraftCaretAndOwnerHistory(t *testing.T) {
	var draft, owner *core.Signal[string]
	ref := new(re.Ref)
	edited := ""
	caret := 0
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		draft = core.NewSignal(cx, "before @fi after")
		owner = core.NewSignal(cx, "first")
		return ReferenceEditor{Ref: ref, Owner: owner.Get, OnEdit: func(text string, pos int) { edited = text; caret = pos; draft.Set(text) }, Input: core.Input{TestID: "rich", Value: draft.Get, AutoFocus: true, Style: core.Style{Height: core.Pt(100)}}}
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
	s := ref.Session()
	app.Post(func() { s.Dispatch(s.State().Tr().SetSelection(re.TextSelectionAt(s.State().Doc, 11, 11))) })
	app.Settle()
	app.Type("id:rich", "le")
	if edited != "before @file after" || caret != 12 {
		t.Fatalf("draft=%q caret=%d", edited, caret)
	}
	wire := model.ReferenceText(model.ThreadRef{Source: strings.Repeat("b", 32), Backend: model.AgentCodex, SessionID: "task"}, "Same label")
	app.Post(func() { draft.Set("before " + wire + " after") })
	app.Settle()
	if got := s.State().Selection.Head(); got != 9 {
		t.Fatalf("completion caret=%d want after atom", got)
	}
	app.Type("id:rich", "!")
	if draft.GetUntracked() != "before "+wire+"! after" {
		t.Fatal(draft.GetUntracked())
	}
	app.Post(func() { owner.Set("second"); draft.Set("other conversation") })
	app.Settle()
	if s.Text() != "other conversation" {
		t.Fatal("switch did not reset", s.Text())
	}
	if depth := re.UndoDepth(s.State()); depth != 0 {
		t.Fatal("new owner retained history", depth)
	}
	app.Key("mod+z")
	if s.Text() != "other conversation" {
		t.Fatal("undo crossed conversations", s.Text())
	}
}

func TestRichEditorQuotedFilesAndCodeStayLiteral(t *testing.T) {
	for _, text := range []string{"`@file.go` ", "```\n@file.go \n```", "mail@example.com ", "@\"unclosed file "} {
		if got := editorTokens(text, nil, nil); len(got) > 0 {
			t.Errorf("literal %q made chips: %+v", text, got)
		}
	}
}

func TestRichFileAtomDoesNotAbsorbAdjacentEdits(t *testing.T) {
	var draft *core.Signal[string]
	ref := new(re.Ref)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		draft = core.NewSignal(cx, "@file.go after")
		return ReferenceEditor{Ref: ref, Input: core.Input{TestID: "rich", Value: draft.Get, OnChange: draft.Set, AutoFocus: true, Style: core.Style{Height: core.Pt(100)}}}
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
	s := ref.Session()
	app.Post(func() { s.Dispatch(s.State().Tr().SetSelection(re.TextSelectionAt(s.State().Doc, 2, 2))) })
	app.Settle()
	app.Type("id:rich", ".new")
	first := s.State().Doc.FirstChild().FirstChild()
	if first.Type.Name != "file" || first.AttrStr("text") != "@file.go" || draft.GetUntracked() != "@file.go.new after" {
		t.Fatal("adjacent text changed atom identity", s.JSON())
	}
	app.Post(func() { s.Dispatch(s.State().Tr().SetSelection(re.TextSelectionAt(s.State().Doc, 1, 2))) })
	app.Settle()
	app.Key("mod+c")
	if app.Clipboard != "@file.go" {
		t.Fatal("copy includes adjacent edit", app.Clipboard)
	}
}
