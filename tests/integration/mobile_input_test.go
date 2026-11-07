package integration

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/mobile"
)

func TestMobileMessagePasteReplacesSelectionAndKeepsCaret(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentCodex)
	m.Draft = "你好👋尾"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyEnd)
	tt.Key(ui.Shift, ui.KeyLeft)
	tt.SetClipboard("世界🌍\n下一行")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if m.Draft != "你好👋世界🌍\n下一行" {
		t.Fatalf("pasted draft = %q", m.Draft)
	}
	tt.Type("!")
	tt.Frame()
	if m.Draft != "你好👋世界🌍\n下一行!" {
		t.Fatalf("typing after paste = %q", m.Draft)
	}
	tt.SetClipboard("")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if m.Draft != "你好👋世界🌍\n下一行!" {
		t.Fatal("an empty clipboard should leave the draft alone")
	}
}

func TestMobilePairCodePaste(t *testing.T) {
	m := mobile.NewMobileApp()
	m.Loaded = true
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	tt.SetClipboard("miya://connect/code")
	if err := tt.Click("Paste"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Paste != "miya://connect/code" || m.Link != "" {
		t.Fatalf("paste should fill the code without connecting: paste=%q link=%q", m.Paste, m.Link)
	}
}

func TestMobileComposerEditMenuRetainsCopyPasteAndSelection(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentCodex)
	m.Draft = "description"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	choose := func(label string) {
		t.Helper()
		if err := tt.RightClick("Message"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	choose("Select all")
	a, z := m.InlineInput.Doc.Selection()
	if a != 0 || z != len("description") || !tt.Focused("Message") {
		t.Fatalf("menu selection=%d:%d focus=%v", a, z, tt.Focused("Message"))
	}
	choose("Copy")
	tt.Type("draft")
	tt.Frame()
	if m.Draft != "draft" {
		t.Fatalf("replace copied selection=%q", m.Draft)
	}
	choose("Paste")
	if m.Draft != "draftdescription" || !tt.Focused("Message") {
		t.Fatalf("paste=%q focus=%v", m.Draft, tt.Focused("Message"))
	}
}

func TestMobileComposerExpandsWithoutLosingFocus(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentCodex)
	m.State.Model, m.State.Effort = "gpt-6.1-sol", "high"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	compact, ok := tt.Find("Message")
	if !ok {
		t.Fatal("message input missing")
	}
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("这是一段足够长的消息，会自动换行，换行后文字应该铺满整个输入框的宽度。")
	tt.Frame()
	expanded, _ := tt.Find("Message")
	send, _ := tt.Find("Send")
	if expanded.W <= compact.W+80 || expanded.Y+expanded.H > send.Y {
		t.Fatalf("multiline input should use full width above its tools: compact=%v expanded=%v send=%v", compact, expanded, send)
	}
	if !tt.Focused("Message") {
		t.Fatal("wrapping lost keyboard focus")
	}
	tt.Type("\n第二行")
	tt.Frame()
	if !strings.HasSuffix(m.Draft, "\n第二行") {
		t.Fatalf("typing after expansion lost the caret: %q", m.Draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("短消息")
	tt.Frame()
	shrunk, _ := tt.Find("Message")
	if m.Draft != "短消息" || shrunk.W != compact.W || !tt.Focused("Message") {
		t.Fatalf("short input should return to the capsule without losing focus: draft=%q bounds=%v", m.Draft, shrunk)
	}
}
