package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestComposerCommandSelectionStagesChipAndEditableArguments(t *testing.T) {
	app, tt := testApp(t)
	tt.Frame()
	tt.Type("/go")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	doc := app.complete.input.doc
	if app.c.draft() != "/goal " || app.notice != "" || app.complete.open {
		t.Fatalf("selection executed a command: draft=%q notice=%q", app.c.draft(), app.notice)
	}
	if doc.Text().String() != string(richtext.ObjectReplacement)+" " {
		t.Fatalf("selected command is not a chip: %q", doc.Text().String())
	}
	if a := doc.Text().AttributesAt(0); a.Tag != composerCommandTag || a.Attachment == nil || a.Attachment.Name != "/goal" {
		t.Fatal("missing command identity", a)
	}
	if !tt.HasText("描述目标，或输入 pause / resume / clear") {
		t.Fatal("missing command argument hint")
	}
	tt.Type("修复手机按钮👋")
	tt.Frame()
	if app.c.draft() != "/goal 修复手机按钮👋" {
		t.Fatal("arguments are not editable", app.c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if tt.Clipboard() != "/goal 修复手机按钮👋" {
		t.Fatal("copy exposed an object placeholder", tt.Clipboard())
	}
	// Backspace deletes the command as one object, preserving its arguments.
	doc.SetSelection(1, 1)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if app.c.draft() != " 修复手机按钮👋" || app.complete.input.commandHint() != "" {
		t.Fatal("removing the chip damaged the arguments", app.c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if app.c.draft() != "/goal 修复手机按钮👋" || doc.Text().AttributesAt(0).Attachment == nil || app.complete.open {
		t.Fatal("undo did not restore the command", app.c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyX)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if app.c.draft() != "/goal 修复手机按钮👋" || doc.Text().AttributesAt(0).Attachment == nil {
		t.Fatal("cut and paste did not restore the command chip", app.c.draft())
	}
	saveCommandSnapshot(t, tt, "desktop-command.png")
}

func TestComposerCommandChipKeepsReferencesAndPlainText(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{handoffSource()})
	ref := referenceText(testReference(handoffSource()), "登录修复")
	app.c.rememberDraft("/goal 参考 " + ref + " 修复")
	tt.Frame()
	doc := app.complete.input.doc
	objects := 0
	for _, run := range doc.Text().Runs(0, doc.Text().Len()) {
		if run.Attributes.Attachment != nil {
			objects++
		}
	}
	if objects != 2 || inlineThreadRaw(doc.Text()) != "/goal 参考 "+ref+" 修复" {
		t.Fatal("command and reference did not coexist", doc.Text().String())
	}
	for _, raw := range []string{"/go", "/goal", "/unknown text", "/Users/me/file.txt note", "正文 /goal text", "```\n/goal text\n```"} {
		app.c.rememberDraft(raw)
		tt.Frame()
		if doc.Text().String() != raw {
			t.Fatalf("ordinary input became a command chip: %q -> %q", raw, doc.Text().String())
		}
	}
}

func TestComposerClaudeCommandChipAndAgentSwitch(t *testing.T) {
	app, tt := testApp(t)
	app.c.setAgent(agentClaude)
	app.c.claudeCmdCache = map[string]claudeCommandList{app.c.cwd: {at: time.Now(), keep: time.Hour, choice: []composerChoice{{Name: "custom-task", ArgumentHint: "任务说明"}}}}
	tt.Frame()
	tt.Type("/custom")
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if app.c.draft() != "/custom-task " || app.complete.input.doc.Text().AttributesAt(0).Attachment == nil || !tt.HasText("任务说明") {
		t.Fatal("custom command did not become an editable chip", app.c.draft())
	}
	app.c.setAgent(agentCodex)
	tt.Frame()
	if app.c.draft() != "/custom-task " || app.complete.input.doc.Text().String() != "/custom-task " || app.complete.input.commandHint() != "" {
		t.Fatal("switching agent retained an unsupported command chip")
	}
}

func TestMobileCommandSelectionUsesInlineChip(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testMobileApp()
			m.startConversation("/a", agentCodex)
			m.draft = "/go"
			tt := ui.NewTester(m.viewRoot, width, 750)
			tt.Frame()
			if err := tt.Click("/goal"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			doc := m.inlineInput.doc
			if m.draft != "/goal " || doc.Text().AttributesAt(0).Tag != composerCommandTag {
				t.Fatal("phone selection is not a chip", m.draft)
			}
			if err := tt.Click("消息"); err != nil {
				t.Fatal(err)
			}
			tt.Type("修复通知文案")
			tt.Frame()
			if m.draft != "/goal 修复通知文案" {
				t.Fatal("phone argument entry lost the command", m.draft)
			}
			if width == 390 {
				saveCommandSnapshot(t, tt, "mobile-command.png")
			}
		})
	}
}

func TestMobileLongCommandFitsTheComposer(t *testing.T) {
	m := testMobileApp()
	m.startConversation("/a", agentClaude)
	name := "lark-workflow-meeting-summary"
	m.commands = map[string][]composerChoice{"/a": {{Name: name, ArgumentHint: "日期范围"}}}
	m.draft = "/" + name + " "
	tt := ui.NewTester(m.viewRoot, 320, 750)
	tt.Frame()
	r, ok := tt.Find("消息")
	a := m.inlineInput.doc.Text().AttributesAt(0).Attachment
	if !ok || a == nil || a.Width > r.W || inlineThreadRaw(m.inlineInput.doc.Text()) != m.draft {
		t.Fatalf("long command overflowed or lost its full name: input=%+v attachment=%+v", r, a)
	}
}

func saveCommandSnapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("MIYA_COMMAND_SNAPSHOT"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}
