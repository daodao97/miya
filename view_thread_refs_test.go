package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestInlineThreadReferencesKeepTheirPosition(t *testing.T) {
	app, _ := testApp(t)
	app.c.setThreads([]Thread{handoffSource(), {ID: 3, ServerID: "api-session", Title: "接口检查"}})
	r2 := referenceText(testReference(handoffSource()), "登录修复")
	r3 := referenceText(testRef(agentCodex, "api-session"), "接口检查")
	style := threadMentionStyle{font: ui.Font{Size: 13}}
	for _, raw := range []string{r2 + " ", "参考 " + r2 + " 然后修复", "对比 " + r2 + " 和 " + r3 + " 👋", r2 + " " + r2 + " 继续", r2 + r3, "看下，" + r2 + "。还有（" + r3 + "）"} {
		text := inlineThreadText(raw, true, style, app.referenceTitle)
		if got := inlineThreadRaw(text); got != raw {
			t.Errorf("reference position changed: %q -> %q", raw, got)
		}
	}
	pending := inlineThreadText(testRef(agentCodex, "pending").URL(), true, style, app.referenceTitle)
	if pending.String() != testRef(agentCodex, "pending").URL() {
		t.Fatal("numeric search became a tag before confirmation")
	}
}

func TestComposerShowsInlineTagsAndEditsThemAsOneObject(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "当前", HistoryLoaded: true}, handoffSource()})
	c.setActive(1)
	tt.Frame()
	tt.Type("@登录")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	doc := app.complete.input.doc
	if doc == nil || doc.Text().String() != string(richtext.ObjectReplacement)+" " || c.draft() != referenceText(testReference(handoffSource()), "登录修复")+" " {
		t.Fatalf("reference is not inline: draft=%q text=%q", c.draft(), doc.Text().String())
	}
	if att := doc.Text().AttributesAt(0).Attachment; att == nil || att.Name != "登录修复" {
		t.Fatal("inline tag lacks the conversation title")
	}
	tt.Type("继续修复👋")
	tt.Frame()
	if c.draft() != referenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("typing lost tag: %q", c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if tt.Clipboard() != referenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("copy lost reference identity: %q", tt.Clipboard())
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if c.draft() != "" {
		t.Fatalf("select-all did not delete the inline tag: %q", c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if c.draft() != referenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("undo did not restore inline reference: %q", c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("参考 ")
	tt.Frame()
	tt.SetClipboard(referenceText(testReference(handoffSource()), "登录修复") + " ")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	tt.Type("继续")
	tt.Frame()
	if c.draft() != "参考 "+referenceText(testReference(handoffSource()), "登录修复")+" 继续" {
		t.Fatalf("paste moved reference out of its sentence: %q", c.draft())
	}
	// The input exposes the same shaped geometry as its inline objects.
	spans := inlineThreadSpans(doc.Text(), 13)
	layout := ui.ShapeRichText(spans, app.complete.input.style.font, 600)
	objects := layout.Objects()
	caret := layout.CaretAt(ui.TextCaretPosition{Index: ui.UTF16Len("参考 ") + 1})
	if len(objects) != 1 || caret.X < objects[0].Rect.X+objects[0].Rect.W-1 || caret.Y > objects[0].Rect.Y+objects[0].Rect.H {
		t.Fatalf("text after tag is not on the same line: object=%+v caret=%+v", objects, caret)
	}
}

func TestInlineCompletionPreservesMidSentenceCaretAndChineseInput(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "当前", HistoryLoaded: true}, handoffSource()})
	c.setActive(1)
	c.rememberDraft("检查  的问题")
	tt.Frame()
	doc := app.complete.input.doc
	doc.SetSelection(3, 3)
	tt.Frame()
	tt.Type("@登录")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Compose("ji", 2)
	tt.Compose("继", 1)
	tt.Compose("", 0)
	tt.Type("继续")
	tt.Frame()
	if got := c.draft(); got != "检查 "+referenceText(testReference(handoffSource()), "登录修复")+" 继续 的问题" {
		t.Fatalf("completion or Chinese input moved the caret: %q", got)
	}
	tt.Type(" @登")
	tt.Frame()
	if !app.complete.open {
		t.Fatalf("second reference search did not open: draft=%q token=%+v moved=%v source=%q", c.draft(), app.complete.token, app.complete.input.moved, app.complete.source)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if app.complete.open {
		t.Fatal("moving the caret left a stale completion open")
	}
}

func TestMessagesShowThreadTagsWithoutContextPackets(t *testing.T) {
	app, _ := testApp(t)
	source := handoffSource()
	source.Title = `登录 "修复" <检查>`
	raw := "参考 " + referenceText(testReference(source), source.Title) + " 继续修复"
	text, _, err := expandThreadRefs(raw, ThreadRef{}, func(ref ThreadRef) (Thread, []Message, error) { return source, source.Messages, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, tags := app.threadReferenceContent(text)
	if len(tags) != 1 || tags[0].Ref != testReference(source) || tags[0].Title != source.Title || body != raw {
		t.Fatalf("expanded message did not become one named tag: body=%q tags=%+v", body, tags)
	}
	message := Message{ID: 50, Role: "user", Content: text}
	tt := ui.NewTester(func(c *ui.Context) { app.messageView(c, message, false) }, 600, 280)
	tt.Frame()
	if !tt.HasText(source.Title) || !tt.HasText("继续修复") || tt.HasText("miya-thread-context") || tt.HasText("最初的请求") || tt.HasText("引用对话：") || tt.HasText("@#2") {
		t.Fatalf("message did not hide expanded context behind a named tag: %q", tt.Texts())
	}
	app.startEdit(message)
	if !strings.Contains(app.editText, referenceText(testReference(source), source.Title)) || strings.Contains(app.editText, contextEnvelopeOpen) {
		t.Fatalf("editing message exposed context instead of its reference: %q", app.editText)
	}
	_, tags = app.threadReferenceContent(referenceText(testRef(agentCodex, "missing"), "已删除会话"))
	if len(tags) != 1 || tags[0].Title != "已删除会话" {
		t.Fatalf("unknown reference lost its fallback label: %+v", tags)
	}
}

func TestMobileThreadTagRetainsGlobalTitleAndReference(t *testing.T) {
	m := testMobileApp()
	m.startConversation("/a", agentCodex)
	m.draft = referenceText(testRef(agentCodex, "s2"), "thread-two") + " "
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	doc := m.inlineInput.doc
	if a := doc.Text().AttributesAt(0).Attachment; a == nil || a.Name != "thread-two" {
		t.Fatal("phone did not resolve the global title")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好👋")
	tt.Frame()
	if m.draft != referenceText(testRef(agentCodex, "s2"), "thread-two")+" 你好👋" {
		t.Fatalf("phone typing lost tag: %q", m.draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("新内容")
	tt.Frame()
	if m.draft != "新内容" {
		t.Fatalf("phone select-all did not include tag: %q", m.draft)
	}
}

func TestComposerConvertsPastedLinkOnlyWhenRequested(t *testing.T) {
	app, tt := testApp(t)
	src := handoffSource()
	app.c.setThreads([]Thread{{ID: 1, Title: "当前", HistoryLoaded: true}, src})
	app.c.setActive(1)
	tt.Frame()
	tt.SetClipboard(testReference(src).URL())
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if got := app.c.draft(); got != testReference(src).URL() || len(threadRefs(got)) != 0 {
		t.Fatalf("bare link became a reference: %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if err := tt.RightClick("Message Codex"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("粘贴为会话引用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.c.draft() != referenceText(testReference(src), src.Title) || app.complete.open {
		t.Fatalf("conversion failed: %q", app.c.draft())
	}
	app.complete.input.doc.SetSelection(0, 1)
	tt.Frame()
	if err := tt.RightClick("Message Codex"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制会话链接"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != testReference(src).URL() {
		t.Fatal(tt.Clipboard())
	}
}

// MIYA_THREAD_TAG_SNAPSHOT=dir captures the composer and message together.
func TestThreadTagSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_THREAD_TAG_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_THREAD_TAG_SNAPSHOT")
	}
	app, tt := testApp(t)
	c := app.c
	source := handoffSource()
	text, _, _ := expandThreadRefs("参考 "+referenceText(testReference(source), source.Title)+" 给登录页加上提示", ThreadRef{}, func(ThreadRef) (Thread, []Message, error) { return source, source.Messages, nil })
	c.setThreads([]Thread{{ID: 1, Title: "登录页提示", HistoryLoaded: true, Messages: []Message{{ID: 50, Role: "user", Content: text}}}, source})
	c.setActive(1)
	c.rememberDraft(referenceText(testReference(source), source.Title) + " 请继续检查移动端")
	for _, dark := range []bool{false, true} {
		app.appearance = map[bool]string{false: "light", true: "dark"}[dark]
		tt.Frame()
		name := map[bool]string{false: "thread-tags-light.png", true: "thread-tags-dark.png"}[dark]
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, tt.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	m := testMobileApp()
	m.state.Projects[1].Threads[0].Title = "登录修复与移动端兼容性检查"
	m.startConversation("/a", agentCodex)
	m.draft = referenceText(testReference(source), source.Title) + " 请继续检查移动端"
	phone := ui.NewTester(m.viewRoot, 390, 750)
	for _, dark := range []bool{false, true} {
		phone.SetDark(dark)
		phone.Frame()
		name := map[bool]string{false: "thread-tags-phone-light.png", true: "thread-tags-phone-dark.png"}[dark]
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, phone.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
