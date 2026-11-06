package main

import (
	png "image/png"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	conversation "miya/internal/conversation"
	model "miya/internal/model"
	os "os"
	filepath "path/filepath"
	strconv "strconv"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
	richtext "github.com/egoist/mygo/ui/richtext"
)

func TestComposerCommandSelectionStagesChipAndEditableArguments(t *testing.T) {
	app, tt := testApp(t)
	tt.Frame()
	tt.Type("/go")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	doc := app.Complete.Input.Doc
	if app.C.(*controller.Controller).Draft() != "/goal " || app.Notice != "" || app.Complete.Open {
		t.Fatalf("selection executed a command: draft=%q notice=%q", app.C.(*controller.Controller).Draft(), app.Notice)
	}
	if doc.Text().String() != string(richtext.ObjectReplacement)+" " {
		t.Fatalf("selected command is not a chip: %q", doc.Text().String())
	}
	if a := doc.Text().AttributesAt(0); a.Tag != conversation.ComposerCommandTag || a.Attachment == nil || a.Attachment.Name != "/goal" {
		t.Fatal("missing command identity", a)
	}
	if !tt.HasText("Describe a goal, or enter pause / resume / clear") {
		t.Fatal("missing command argument hint")
	}
	tt.Type("修复手机按钮👋")
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != "/goal 修复手机按钮👋" {
		t.Fatal("arguments are not editable", app.C.(*controller.Controller).Draft())
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
	if app.C.(*controller.Controller).Draft() != " 修复手机按钮👋" || app.Complete.Input.CommandHint() != "" {
		t.Fatal("removing the chip damaged the arguments", app.C.(*controller.Controller).Draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != "/goal 修复手机按钮👋" || doc.Text().AttributesAt(0).Attachment == nil || app.Complete.Open {
		t.Fatal("undo did not restore the command", app.C.(*controller.Controller).Draft())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyX)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != "/goal 修复手机按钮👋" || doc.Text().AttributesAt(0).Attachment == nil {
		t.Fatal("cut and paste did not restore the command chip", app.C.(*controller.Controller).Draft())
	}
	saveCommandSnapshot(t, tt, "desktop-command.png")
}

func TestComposerCommandChipKeepsReferencesAndPlainText(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{handoffSource()})
	ref := model.ReferenceText(testReference(handoffSource()), "登录修复")
	app.C.(*controller.Controller).RememberDraft("/goal 参考 " + ref + " 修复")
	tt.Frame()
	doc := app.Complete.Input.Doc
	objects := 0
	for _, run := range doc.Text().Runs(0, doc.Text().Len()) {
		if run.Attributes.Attachment != nil {
			objects++
		}
	}
	if objects != 2 || conversation.InlineThreadRaw(doc.Text()) != "/goal 参考 "+ref+" 修复" {
		t.Fatal("command and reference did not coexist", doc.Text().String())
	}
	for _, raw := range []string{"/go", "/goal", "/unknown text", "/Users/me/file.txt note", "正文 /goal text", "```\n/goal text\n```"} {
		app.C.(*controller.Controller).RememberDraft(raw)
		tt.Frame()
		if doc.Text().String() != raw {
			t.Fatalf("ordinary input became a command chip: %q -> %q", raw, doc.Text().String())
		}
	}
}

func TestComposerClaudeCommandChipAndAgentSwitch(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetAgent(appmodel.AgentClaude)
	app.C.(*controller.Controller).ClaudeCmdCache = map[string]controller.ClaudeCommandList{app.C.(*controller.Controller).Cwd: {At: time.Now(), Keep: time.Hour, Choice: []appmodel.ComposerChoice{{Name: "custom-task", ArgumentHint: "任务说明"}}}}
	tt.Frame()
	tt.Type("/custom")
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != "/custom-task " || app.Complete.Input.Doc.Text().AttributesAt(0).Attachment == nil || !tt.HasText("任务说明") {
		t.Fatal("custom command did not become an editable chip", app.C.(*controller.Controller).Draft())
	}
	app.C.(*controller.Controller).SetAgent(appmodel.AgentCodex)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != "/custom-task " || app.Complete.Input.Doc.Text().String() != "/custom-task " || app.Complete.Input.CommandHint() != "" {
		t.Fatal("switching agent retained an unsupported command chip")
	}
}

func TestMobileCommandSelectionUsesInlineChip(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testMobileApp()
			m.StartConversation("/a", appmodel.AgentCodex)
			m.Draft = "/go"
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			if err := tt.Click("/goal"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			doc := m.InlineInput.Doc
			if m.Draft != "/goal " || doc.Text().AttributesAt(0).Tag != conversation.ComposerCommandTag {
				t.Fatal("phone selection is not a chip", m.Draft)
			}
			if err := tt.Click("Message"); err != nil {
				t.Fatal(err)
			}
			tt.Type("修复通知文案")
			tt.Frame()
			if m.Draft != "/goal 修复通知文案" {
				t.Fatal("phone argument entry lost the command", m.Draft)
			}
			if width == 390 {
				saveCommandSnapshot(t, tt, "mobile-command.png")
			}
		})
	}
}

func TestMobileLongCommandFitsTheComposer(t *testing.T) {
	m := testMobileApp()
	m.StartConversation("/a", appmodel.AgentClaude)
	name := "lark-workflow-meeting-summary"
	m.Commands = map[string][]appmodel.ComposerChoice{"/a": {{Name: name, ArgumentHint: "日期范围"}}}
	m.Draft = "/" + name + " "
	tt := ui.NewTester(m.ViewRoot, 320, 750)
	tt.Frame()
	r, ok := tt.Find("Message")
	a := m.InlineInput.Doc.Text().AttributesAt(0).Attachment
	if !ok || a == nil || a.Width > r.W || conversation.InlineThreadRaw(m.InlineInput.Doc.Text()) != m.Draft {
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
