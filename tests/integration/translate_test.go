package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/translate"
	"miya/internal/widgets"

	"github.com/egoist/mygo/ui"
)

// translateIcon is the text of a reply's translate button: the object its
// icon draws in, joined to the word before it.
const translateIcon = "￼⁠"

// translationChoice sets this device's reply translation for a test, and
// puts it back after.
func translationChoice(t *testing.T, on bool, target string) {
	t.Helper()
	was := widgets.Translation()
	widgets.SetTranslationEnabled(on)
	widgets.SetTranslationTarget(target)
	t.Cleanup(func() {
		widgets.SetTranslationEnabled(was.On)
		widgets.SetTranslationTarget(was.Target.Tag)
		widgets.SetTranslationAgent(was.Agent)
		widgets.SetTranslationModel(was.Model)
	})
}

func translationThread(c *controller.Controller, replies ...string) {
	messages := []model.Message{{ID: 1, Role: "user", Content: "修一下解析器"}}
	for i, reply := range replies {
		messages = append(messages, model.Message{ID: i + 2, Role: "assistant", Content: reply, Status: "completed"})
	}
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Loaded: true, HistoryLoaded: true, Messages: messages}})
	c.SetActive(1)
}

// shows reports whether a paragraph reads text, its button after it or not.
func shows(tt *ui.Tester, text string) bool {
	for _, s := range tt.Texts() {
		if s == text || s == text+" "+translateIcon {
			return true
		}
	}
	return false
}

// clickTranslate clicks the translate button ending the paragraph text,
// which must be on that paragraph's line.
func clickTranslate(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	para, ok := tt.Find(text + " " + translateIcon)
	if !ok {
		t.Fatalf("no translate button after %q: %q", text, tt.Texts())
	}
	icon, ok := tt.Find(translateIcon)
	if !ok {
		t.Fatalf("no translate button: %q", tt.Texts())
	}
	if icon.Y < para.Y-1 || icon.Y+icon.H > para.Y+para.H+1 || para.H > 2*icon.H {
		t.Fatalf("button %+v not on the line of its paragraph %+v", icon, para)
	}
	tt.ClickAt(icon.X+icon.W/2, icon.Y+icon.H/2)
	tt.Advance(time.Second)
}

func TestEnglishReplyTranslatesAndBack(t *testing.T) {
	app, tt := testApp(t)
	translationChoice(t, true, "zh-Hans")
	const english = "I fixed the parser and added a test that covers the empty input case."
	const chinese = "我修复了解析器，并添加了一个覆盖空输入情况的测试。"
	var asked []string
	app.Translate = func(_ model.Thread, text, lang string, done func(string, error)) {
		asked = append(asked, lang+":"+text)
		done(chinese, nil)
	}
	translationThread(app.C.(*controller.Controller), english, "好的，已经改完了，测试也通过了。")
	tt.Frame()
	clickTranslate(t, tt, english)
	if !shows(tt, chinese) || shows(tt, english) {
		t.Fatalf("translation not shown in place of the original: %q", tt.Texts())
	}
	clickTranslate(t, tt, chinese)
	if !shows(tt, english) || shows(tt, chinese) {
		t.Fatalf("original not back: %q", tt.Texts())
	}
	// Translated once: showing it again asks nobody.
	clickTranslate(t, tt, english)
	if !shows(tt, chinese) || len(asked) != 1 || asked[0] != "zh-Hans:"+english {
		t.Fatalf("asked %q, shows %q", asked, tt.Texts())
	}
}

func TestReplyTranslationFollowsTheDeviceChoice(t *testing.T) {
	app, tt := testApp(t)
	const english = "I fixed the parser and added a test that covers the empty input case."
	const chinese = "好的，已经改完了，测试也通过了，可以合并。"
	var asked []string
	app.Translate = func(_ model.Thread, text, lang string, done func(string, error)) {
		asked = append(asked, lang+":"+text)
		done("Done: the tests pass and it can be merged.", nil)
	}
	translationChoice(t, false, "zh-Hans")
	translationThread(app.C.(*controller.Controller), english, chinese)
	tt.Frame()
	if _, ok := tt.Find(translateIcon); ok {
		t.Fatal("a translate button shows with translation turned off")
	}
	// Into English, the Chinese reply offers it and the English one not.
	widgets.SetTranslationEnabled(true)
	widgets.SetTranslationTarget("en")
	tt.Frame()
	clickTranslate(t, tt, chinese)
	if len(asked) != 1 || asked[0] != "en:"+chinese {
		t.Fatalf("asked %q", asked)
	}
	if tt.HasText(english + " " + translateIcon) {
		t.Fatal("the English reply offers to translate into English")
	}
}

func TestTranslateButtonAfterCodeTakesItsOwnLine(t *testing.T) {
	app, tt := testApp(t)
	translationChoice(t, true, "zh-Hans")
	app.Translate = func(_ model.Thread, text, lang string, done func(string, error)) { done("好", nil) }
	reply := "Run the tests again with the race detector before merging this change:\n\n```sh\ngo test -race ./...\n```"
	translationThread(app.C.(*controller.Controller), reply)
	tt.Frame()
	if !tt.HasText(translateIcon) {
		t.Fatalf("no button of its own after the code block: %q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, "go test -race") && strings.Contains(s, translateIcon) {
			t.Fatalf("button joined the code block: %q", s)
		}
	}
}

func TestTranslationModelBelongsToItsAgent(t *testing.T) {
	testApp(t)
	translationChoice(t, true, "zh-Hans")
	widgets.SetTranslationModel("ignored")
	if got := widgets.Translation(); got.Agent != "" || got.Model != "" {
		t.Fatalf("a model without an agent: %+v", got)
	}
	widgets.SetTranslationAgent("claude-code")
	widgets.SetTranslationModel("haiku")
	if got := widgets.Translation(); got.Agent != "claude-code" || got.Model != "haiku" {
		t.Fatalf("choice %+v", got)
	}
	widgets.SetTranslationAgent("codex")
	if got := widgets.Translation(); got.Agent != "codex" || got.Model != "" {
		t.Fatalf("Claude's model kept for Codex: %+v", got)
	}
	if got := widgets.TranslationAgents(); !slices.Contains(got, "pi") || !slices.Contains(got, "codex") || !slices.Contains(got, "claude-code") || got[0] != "" {
		t.Fatalf("translating agents %q, want the conversation's and every agent", got)
	}
	widgets.SetTranslationAgent("pi")
	if got := widgets.Translation(); got.Agent != "pi" {
		t.Fatalf("pi not kept as the translating agent: %+v", got)
	}
}

func TestAutoTranslationTranslatesShownRepliesAFewAtATime(t *testing.T) {
	app, tt := testApp(t)
	translationChoice(t, true, "zh-Hans")
	widgets.SetTranslationAuto(true)
	t.Cleanup(func() { widgets.SetTranslationAuto(false) })
	replies := []string{
		"I fixed the parser and added a test that covers the empty input case.",
		"The build now passes on both platforms after updating the signing script.",
		"Next I will look at why the phone reconnects slowly after waking up.",
	}
	answers := map[string]func(string, error){}
	var asked []string
	app.Translate = func(_ model.Thread, text, lang string, done func(string, error)) {
		asked = append(asked, text)
		answers[text] = done
	}
	translationThread(app.C.(*controller.Controller), replies...)
	tt.Frame()
	tt.Advance(time.Second)
	if len(asked) != 2 || asked[0] != replies[0] || asked[1] != replies[1] {
		t.Fatalf("asked %q, want the first two at once", asked)
	}
	answers[replies[0]]("我修复了解析器。", nil)
	tt.Frame()
	tt.Advance(time.Second)
	if len(asked) != 3 || asked[2] != replies[2] {
		t.Fatalf("asked %q, want the third once one is done", asked)
	}
	if !shows(tt, "我修复了解析器。") {
		t.Fatalf("automatic translation not shown: %q", tt.Texts())
	}
	// Back to the original by hand: it stays there.
	clickTranslate(t, tt, "我修复了解析器。")
	tt.Frame()
	if !shows(tt, replies[0]) || len(asked) != 3 {
		t.Fatalf("original not kept: asked %q, shows %q", asked, tt.Texts())
	}
}

func TestReplyTranslatedElsewhereShowsTranslated(t *testing.T) {
	app, tt := testApp(t)
	translationChoice(t, true, "zh-Hans")
	const english = "I fixed the parser and added a test that covers the empty input case."
	const chinese = "我修复了解析器，并添加了一个覆盖空输入情况的测试。"
	kept := map[string]string{}
	var revision uint64
	asked := 0
	app.TranslationRevision = func() uint64 { return revision }
	app.LookupTranslations = func(keys []string, done func(map[string]string)) bool {
		asked++
		found := map[string]string{}
		for _, k := range keys {
			if s, ok := kept[k]; ok {
				found[k] = s
			}
		}
		done(found)
		return true
	}
	app.Translate = func(model.Thread, string, string, func(string, error)) { t.Fatal("translated again here") }
	translationThread(app.C.(*controller.Controller), english)
	tt.Frame()
	tt.Frame()
	if asked != 1 || !shows(tt, english) {
		t.Fatalf("asked %d times, shows %q", asked, tt.Texts())
	}
	// The phone translates it: the desktop counts one more translation.
	kept[translate.Key(english, "zh-Hans")] = chinese
	revision++
	tt.Frame()
	tt.Advance(time.Second)
	if asked != 2 || !shows(tt, chinese) {
		t.Fatalf("asked %d times, shows %q", asked, tt.Texts())
	}
	// Back to the original here, it stays: only this device chooses.
	clickTranslate(t, tt, chinese)
	revision++
	tt.Frame()
	if !shows(tt, english) {
		t.Fatalf("original not kept: %q", tt.Texts())
	}
}
