package integration

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	conversation "miya/internal/chat/view"
	"miya/internal/controller"
)

func TestInlineThreadReferencesKeepTheirPosition(t *testing.T) {
	app, _ := testApp(t)
	app.C.(*controller.Controller).SetThreads([]model.Thread{handoffSource(), {ID: 3, ServerID: "api-session", Title: "接口检查"}})
	r2 := model.ReferenceText(testReference(handoffSource()), "登录修复")
	r3 := model.ReferenceText(testRef(appmodel.AgentCodex, "api-session"), "接口检查")
	style := conversation.ThreadMentionStyle{Font: ui.Font{Size: 13}}
	for _, raw := range []string{r2 + " ", "参考 " + r2 + " 然后修复", "对比 " + r2 + " 和 " + r3 + " 👋", r2 + " " + r2 + " 继续", r2 + r3, "看下，" + r2 + "。还有（" + r3 + "）"} {
		text := conversation.InlineThreadText(raw, true, style, app.ReferenceTitle)
		if got := conversation.InlineThreadRaw(text); got != raw {
			t.Errorf("reference position changed: %q -> %q", raw, got)
		}
	}
	pending := conversation.InlineThreadText(testRef(appmodel.AgentCodex, "pending").URL(), true, style, app.ReferenceTitle)
	if pending.String() != testRef(appmodel.AgentCodex, "pending").URL() {
		t.Fatal("numeric search became a tag before confirmation")
	}
}

func TestComposerShowsInlineTagsAndEditsThemAsOneObject(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "Current: ", HistoryLoaded: true}, handoffSource()})
	c.SetActive(1)
	tt.Frame()
	tt.Type("@登录")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	doc := app.Complete.Input.Doc
	if doc == nil || doc.Text().String() != string(richtext.ObjectReplacement)+" " || c.Draft() != model.ReferenceText(testReference(handoffSource()), "登录修复")+" " {
		t.Fatalf("reference is not inline: draft=%q text=%q", c.Draft(), doc.Text().String())
	}
	if att := doc.Text().AttributesAt(0).Attachment; att == nil || att.Name != "登录修复" {
		t.Fatal("inline tag lacks the conversation title")
	}
	tt.Type("继续修复👋")
	tt.Frame()
	if c.Draft() != model.ReferenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("typing lost tag: %q", c.Draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if tt.Clipboard() != model.ReferenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("copy lost reference identity: %q", tt.Clipboard())
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if c.Draft() != "" {
		t.Fatalf("select-all did not delete the inline tag: %q", c.Draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if c.Draft() != model.ReferenceText(testReference(handoffSource()), "登录修复")+" 继续修复👋" {
		t.Fatalf("undo did not restore inline reference: %q", c.Draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("参考 ")
	tt.Frame()
	tt.SetClipboard(model.ReferenceText(testReference(handoffSource()), "登录修复") + " ")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	tt.Type("继续")
	tt.Frame()
	if c.Draft() != "参考 "+model.ReferenceText(testReference(handoffSource()), "登录修复")+" 继续" {
		t.Fatalf("paste moved reference out of its sentence: %q", c.Draft())
	}
	// The input exposes the same shaped geometry as its inline objects.
	spans := conversation.InlineThreadSpans(doc.Text(), 13)
	layout := ui.ShapeRichText(spans, app.Complete.Input.Style.Font, 600)
	objects := layout.Objects()
	caret := layout.CaretAt(ui.TextCaretPosition{Index: ui.UTF16Len("参考 ") + 1})
	if len(objects) != 1 || caret.X < objects[0].Rect.X+objects[0].Rect.W-1 || caret.Y > objects[0].Rect.Y+objects[0].Rect.H {
		t.Fatalf("text after tag is not on the same line: object=%+v caret=%+v", objects, caret)
	}
}

func TestInlineCompletionPreservesMidSentenceCaretAndChineseInput(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "Current: ", HistoryLoaded: true}, handoffSource()})
	c.SetActive(1)
	c.RememberDraft("检查  的问题")
	tt.Frame()
	doc := app.Complete.Input.Doc
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
	if got := c.Draft(); got != "检查 "+model.ReferenceText(testReference(handoffSource()), "登录修复")+" 继续 的问题" {
		t.Fatalf("completion or Chinese input moved the caret: %q", got)
	}
	tt.Type(" @登")
	tt.Frame()
	if !app.Complete.Open {
		t.Fatalf("second reference search did not open: draft=%q token=%+v moved=%v source=%q", c.Draft(), app.Complete.Token, app.Complete.Input.Moved, app.Complete.Source)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if app.Complete.Open {
		t.Fatal("moving the caret left a stale completion open")
	}
}

func TestMessagesShowThreadTagsWithoutContextPackets(t *testing.T) {
	app, _ := testApp(t)
	source := handoffSource()
	source.Title = `登录 "修复" <检查>`
	raw := "参考 " + model.ReferenceText(testReference(source), source.Title) + " 继续修复"
	text, _, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, func(ref model.ThreadRef) (model.Thread, []model.Message, error) {
		return source, source.Messages, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body, tags := app.ThreadReferenceContent(text)
	if len(tags) != 1 || tags[0].Ref != testReference(source) || tags[0].Title != source.Title || body != raw {
		t.Fatalf("expanded message did not become one named tag: body=%q tags=%+v", body, tags)
	}
	message := model.Message{ID: 50, Role: "user", Content: text}
	tt := ui.NewTester(func(c *ui.Context) { app.MessageView(c, message, false) }, 600, 280)
	tt.Frame()
	if !tt.HasText(source.Title) || !tt.HasText("继续修复") || tt.HasText("miya-thread-context") || tt.HasText("Original request") || tt.HasText("引用对话：") || tt.HasText("@#2") {
		t.Fatalf("message did not hide expanded context behind a named tag: %q", tt.Texts())
	}
	app.StartEdit(message)
	if !strings.Contains(app.EditText, model.ReferenceText(testReference(source), source.Title)) || strings.Contains(app.EditText, model.ContextEnvelopeOpen) {
		t.Fatalf("editing message exposed context instead of its reference: %q", app.EditText)
	}
	_, tags = app.ThreadReferenceContent(model.ReferenceText(testRef(appmodel.AgentCodex, "missing"), "已删除会话"))
	if len(tags) != 1 || tags[0].Title != "已删除会话" {
		t.Fatalf("unknown reference lost its fallback label: %+v", tags)
	}
}

func TestMobileThreadTagRetainsGlobalTitleAndReference(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentCodex)
	m.Draft = model.ReferenceText(testRef(appmodel.AgentCodex, "s2"), "thread-two") + " "
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	doc := m.InlineInput.Doc
	if a := doc.Text().AttributesAt(0).Attachment; a == nil || a.Name != "thread-two" {
		t.Fatal("phone did not resolve the global title")
	}
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好👋")
	tt.Frame()
	if m.Draft != model.ReferenceText(testRef(appmodel.AgentCodex, "s2"), "thread-two")+" 你好👋" {
		t.Fatalf("phone typing lost tag: %q", m.Draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("新内容")
	tt.Frame()
	if m.Draft != "新内容" {
		t.Fatalf("phone select-all did not include tag: %q", m.Draft)
	}
}

func TestComposerConvertsPastedLinkOnlyWhenRequested(t *testing.T) {
	app, tt := testApp(t)
	src := handoffSource()
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, Title: "Current: ", HistoryLoaded: true}, src})
	app.C.(*controller.Controller).SetActive(1)
	tt.Frame()
	tt.SetClipboard(testReference(src).URL())
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if got := app.C.(*controller.Controller).Draft(); got != testReference(src).URL() || len(msgfmt.ThreadRefs(got)) != 0 {
		t.Fatalf("bare link became a reference: %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if err := tt.RightClick("Message Codex"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Paste as conversation reference"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != model.ReferenceText(testReference(src), src.Title) || app.Complete.Open {
		t.Fatalf("conversion failed: %q", app.C.(*controller.Controller).Draft())
	}
	app.Complete.Input.Doc.SetSelection(0, 1)
	tt.Frame()
	if err := tt.RightClick("Message Codex"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Copy session link"); err != nil {
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
	c := app.C.(*controller.Controller)
	source := handoffSource()
	text, _, _ := controller.ExpandThreadRefs("参考 "+model.ReferenceText(testReference(source), source.Title)+" 给登录页加上提示", model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) {
		return source, source.Messages, nil
	})
	c.SetThreads([]model.Thread{{ID: 1, Title: "登录页提示", HistoryLoaded: true, Messages: []model.Message{{ID: 50, Role: "user", Content: text}}}, source})
	c.SetActive(1)
	c.RememberDraft(model.ReferenceText(testReference(source), source.Title) + " 请继续检查移动端")
	for _, dark := range []bool{false, true} {
		app.Appearance = map[bool]string{false: "light", true: "dark"}[dark]
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
	m.State.Projects[1].Threads[0].Title = "登录修复与移动端兼容性检查"
	m.StartConversation("/a", appmodel.AgentCodex)
	m.Draft = model.ReferenceText(testReference(source), source.Title) + " 请继续检查移动端"
	phone := ui.NewTester(m.ViewRoot, 390, 750)
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
