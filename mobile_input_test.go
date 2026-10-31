package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMobileMessagePasteReplacesSelectionAndKeepsCaret(t *testing.T) {
	m := testMobileApp()
	m.draft = "你好👋尾"
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyEnd)
	tt.Key(ui.Shift, ui.KeyLeft)
	tt.SetClipboard("世界🌍\n下一行")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if m.draft != "你好👋世界🌍\n下一行" {
		t.Fatalf("pasted draft = %q", m.draft)
	}
	tt.Type("!")
	tt.Frame()
	if m.draft != "你好👋世界🌍\n下一行!" {
		t.Fatalf("typing after paste = %q", m.draft)
	}
	tt.SetClipboard("")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if m.draft != "你好👋世界🌍\n下一行!" {
		t.Fatal("an empty clipboard should leave the draft alone")
	}
}

func TestMobilePairCodePaste(t *testing.T) {
	m := newMobileApp()
	m.loaded = true
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	tt.SetClipboard("miya://connect/code")
	if err := tt.Click("粘贴"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.paste != "miya://connect/code" || m.link != "" {
		t.Fatalf("paste should fill the code without connecting: paste=%q link=%q", m.paste, m.link)
	}
}

func TestMobileComposerExpandsWithoutLosingFocus(t *testing.T) {
	m := testMobileApp()
	m.state.Model, m.state.Effort = "gpt-6.1-sol", "high"
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	compact, ok := tt.Find("消息")
	if !ok {
		t.Fatal("message input missing")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("这是一段足够长的消息，会自动换行，换行后文字应该铺满整个输入框的宽度。")
	tt.Frame()
	expanded, _ := tt.Find("消息")
	send, _ := tt.Find("发送")
	if expanded.W <= compact.W+80 || expanded.Y+expanded.H > send.Y {
		t.Fatalf("multiline input should use full width above its tools: compact=%v expanded=%v send=%v", compact, expanded, send)
	}
	if !tt.Focused("消息") {
		t.Fatal("wrapping lost keyboard focus")
	}
	tt.Type("\n第二行")
	tt.Frame()
	if !strings.HasSuffix(m.draft, "\n第二行") {
		t.Fatalf("typing after expansion lost the caret: %q", m.draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("短消息")
	tt.Frame()
	shrunk, _ := tt.Find("消息")
	if m.draft != "短消息" || shrunk.W != compact.W || !tt.Focused("消息") {
		t.Fatalf("short input should return to the capsule without losing focus: draft=%q bounds=%v", m.draft, shrunk)
	}
}
