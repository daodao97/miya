package nschat

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	re "github.com/nsgo-dev/nsgo/base/richeditor"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func TestRichReferenceChipsCopyCutPasteAndUndo(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("b", 32), Backend: model.AgentCodex, SessionID: "task"}
	thread := model.ReferenceText(ref, "中文😀")
	agent := composer.AgentReferenceText(model.AgentClaude, "Claude")
	initial := "/review " + thread + " @\"dir/a b.go\" " + agent + " "
	var draft *core.Signal[string]
	handle := new(re.Ref)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		draft = core.NewSignal(cx, initial)
		return ReferenceEditor{Ref: handle, Commands: func() []model.ComposerChoice { return []model.ComposerChoice{{Name: "review"}} }, Input: core.Input{TestID: "rich", Value: draft.Get, OnChange: draft.Set, AutoFocus: true, Style: core.Style{Height: core.Pt(160)}}}
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}), nsgotest.Size(800, 400))
	app.Find("text:@中文😀")
	if style := app.SpanStyle("text:@中文😀"); style.Color == 0 {
		t.Fatal("chip has no style")
	}
	s := handle.Session()
	assertAtoms := func(want int) {
		t.Helper()
		n := 0
		for _, child := range s.State().Doc.FirstChild().Content.Content {
			if !child.IsText() {
				n++
			}
		}
		if n != want {
			t.Fatalf("atoms=%d want %d: %s", n, want, s.JSON())
		}
	}
	assertAtoms(4)
	app.Type("id:rich", "!")
	if draft.GetUntracked() != initial+"!" {
		t.Fatal(draft.GetUntracked())
	}
	// Selecting an atom uses one document position, regardless of its label or ID.
	app.Post(func() { s.Dispatch(s.State().Tr().SetSelection(re.TextSelectionAt(s.State().Doc, 3, 4))) })
	app.Settle()
	app.Key("mod+c")
	if app.Clipboard != thread {
		t.Fatalf("copy = %q", app.Clipboard)
	}
	app.RightClick("id:rich-editor-content")
	app.ChoosePopup("Copy")
	if app.Clipboard != thread {
		t.Fatal("native menu lost identity", app.Clipboard)
	}
	app.RightClick("id:rich-editor-content")
	app.ChoosePopup("Cut")
	if strings.Contains(draft.GetUntracked(), ref.URL()) {
		t.Fatal("cut kept reference")
	}
	app.Key("mod+z")
	if draft.GetUntracked() != initial+"!" {
		t.Fatal("undo lost identity", draft.GetUntracked())
	}
	app.Key("mod+x")
	app.Paste("id:rich", core.PasteEvent{Text: thread})
	if draft.GetUntracked() != initial+"!" {
		t.Fatalf("paste = %q", draft.GetUntracked())
	}
	assertAtoms(4)
	app.Post(func() { s.Dispatch(s.State().Tr().SetSelection(re.TextSelectionAt(s.State().Doc, 1, 2))) })
	app.Settle()
	app.Key("mod+c")
	if app.Clipboard != "/review" {
		t.Fatal("command copy", app.Clipboard)
	}
	app.Key("mod+a")
	app.Key("mod+c")
	copied := app.Clipboard
	if copied != initial+"!" {
		t.Fatalf("whole draft copy = %q", copied)
	}
	app.Key("mod+x")
	if draft.GetUntracked() != "" {
		t.Fatal("cut all did not clear", draft.GetUntracked())
	}
	app.Paste("id:rich", core.PasteEvent{Text: copied})
	if draft.GetUntracked() != copied {
		t.Fatal("paste all lost wire identity", draft.GetUntracked())
	}
	assertAtoms(4)

}

func TestRichPasteReferenceMenuImageBytesAndReadonly(t *testing.T) {
	ref := model.ThreadRef{Source: strings.Repeat("c", 32), Backend: model.AgentClaude, SessionID: "task"}
	var draft *core.Signal[string]
	var readonly, disabled *core.Signal[bool]
	var pasted core.PasteEvent
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		draft = core.NewSignal(cx, "")
		readonly = core.NewSignal(cx, false)
		disabled = core.NewSignal(cx, false)
		return ReferenceEditor{ReferenceTitle: func(model.ThreadRef) string { return "Original title" }, Input: core.Input{TestID: "rich", Value: draft.Get, OnChange: draft.Set, AutoFocus: true, ReadonlyFunc: readonly.Get, Disabled: disabled.Get, OnPaste: func(p core.PasteEvent) { pasted = p }, Style: core.Style{Height: core.Pt(160)}}}
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
	app.Clipboard = ref.URL()
	app.RightClick("id:rich-editor-content")
	app.ChoosePopup("Paste as conversation reference")
	app.Settle()
	if draft.GetUntracked() != model.ReferenceText(ref, "Original title") {
		t.Fatal(draft.GetUntracked())
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	screenshot := encoded.Bytes()
	app.Paste("id:rich", core.PasteEvent{Files: []core.PastedFile{{MIME: "image/png", Data: screenshot}}})
	if len(pasted.Files) != 1 || pasted.Files[0].Name != "pasted.png" || pasted.Files[0].MIME != "image/png" || !bytes.Equal(pasted.Files[0].Data, screenshot) {
		t.Fatalf("pasted = %+v", pasted)
	}
	app.ClipboardImage = screenshot
	pasted = core.PasteEvent{}
	app.RightClick("id:rich-editor-content")
	app.ChoosePopup("Paste")
	app.Settle()
	if len(pasted.Files) != 1 || !bytes.Equal(pasted.Files[0].Data, screenshot) {
		t.Fatalf("native paste lost image bytes: %+v", pasted)
	}
	app.ClipboardImage = nil

	before := draft.GetUntracked()
	app.Post(func() { readonly.Set(true) })
	app.Settle()
	app.Key("mod+a")
	app.Key("mod+c")
	if app.Clipboard != before {
		t.Fatal("readonly copy", app.Clipboard)
	}
	app.Key("x")
	event := core.PasteEvent{Text: "ignored"}
	app.Report(nsgotest.Event{Kind: nsgotest.EventPaste, ID: app.Find("role:textbox").ID, Data: &event})
	pasted = core.PasteEvent{}
	event = core.PasteEvent{Files: []core.PastedFile{{MIME: "image/png", Data: screenshot}}}
	app.Report(nsgotest.Event{Kind: nsgotest.EventPaste, ID: app.Find("role:textbox").ID, Data: &event})
	if draft.GetUntracked() != before || len(pasted.Files) != 0 {
		t.Fatal("readonly edited or pasted")
	}
	app.Post(func() { disabled.Set(true) })
	app.Settle()
	if app.Enabled("id:rich") {
		t.Fatal("disabled input is enabled")
	}
}

func TestRichCompletionCaptureKeysAndMultiline(t *testing.T) {
	var draft *core.Signal[string]
	var capture *core.Signal[bool]
	var keys []string
	sent := 0
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		draft = core.NewSignal(cx, "")
		capture = core.NewSignal(cx, true)
		return ReferenceEditor{CaptureKeys: func() []string {
			if capture.Get() {
				return []string{"enter", "tab", "up", "down", "escape"}
			}
			return nil
		}, OnKey: func(key string) { keys = append(keys, key) }, Input: core.Input{TestID: "rich", Value: draft.Get, OnChange: draft.Set, AutoFocus: true, OnSubmit: func(string) { sent++ }, Style: core.Style{Height: core.Pt(160)}}}
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
	for _, key := range []string{"enter", "tab", "up", "down", "escape"} {
		app.Key(key)
	}
	if strings.Join(keys, ",") != "enter,tab,up,down,escape" || sent != 0 || draft.GetUntracked() != "" {
		t.Fatalf("keys=%v sent=%d draft=%q", keys, sent, draft.GetUntracked())
	}
	app.Post(func() { capture.Set(false) })
	app.Settle()
	app.Type("id:rich", "one")
	app.Key("shift+enter")
	app.Type("id:rich", "two")
	if draft.GetUntracked() != "one\ntwo" {
		t.Fatal(draft.GetUntracked())
	}
	app.Key("enter")
	if sent != 1 {
		t.Fatal("Enter did not submit", sent)
	}
}
