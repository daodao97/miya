package integration

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	desktopui "miya/internal/ui"
)

func TestAgentCommandRouting(t *testing.T) {
	for _, tc := range []struct {
		agent, command string
		want           bool
	}{
		{appmodel.AgentClaude, "review the diff", true},
		{appmodel.AgentClaude, "lark-im", true},
		{appmodel.AgentClaude, "compact", true},
		{appmodel.AgentClaude, "model", false},
		{appmodel.AgentClaude, "new", false},
		{appmodel.AgentCodex, "lark-im", false},
		{appmodel.AgentCodex, "compact", false},
	} {
		if got := agent.DefaultCommands(tc.agent).TakesCommand(tc.command); got != tc.want {
			t.Errorf("commandRouting(%q, %q) = %v, want %v", tc.agent, tc.command, got, tc.want)
		}
	}
}

func TestClaudeCommandChoices(t *testing.T) {
	c := newViewerController()
	c.SetAgent(appmodel.AgentClaude)
	c.Cwd = "/p"
	c.ClaudeCmdCache = map[string]controller.ClaudeCommandList{"/p": {At: time.Now(), Keep: time.Hour, Choice: []model.ComposerChoice{{Name: "review", Detail: "Claude review"}, {Name: "lark-im", Detail: "IM"}, {Name: "model", Detail: "Claude model"}}}}
	app := &desktopui.App{C: c}
	names := map[string]string{}
	for _, ch := range app.CommandChoices("") {
		if _, dup := names[ch.Name]; dup {
			t.Errorf("%q listed twice", ch.Name)
		}
		names[ch.Name] = ch.Detail
	}
	if names["review"] != "Claude review" || names["lark-im"] != "IM" {
		t.Errorf("Claude Code's commands should be listed: %v", names)
	}
	if names["model"] != "Choose the model" {
		t.Errorf("Miya's own /model should win: %q", names["model"])
	}
	if _, ok := names["compact"]; ok {
		t.Error("Miya's Codex-only /compact should not be listed for Claude Code")
	}
	c.SetAgent(appmodel.AgentCodex)
	if got := app.CommandChoices("lark"); len(got) != 0 {
		t.Errorf("Codex conversations should not list Claude Code's commands: %v", got)
	}
}

func TestMobileCommandMenu(t *testing.T) {
	m := testMobileApp()
	m.State.Projects[0].Threads[0].Agent = appmodel.AgentClaude
	m.Commands = map[string][]model.ComposerChoice{"/a": {{Name: "lark-im", Detail: "IM"}, {Name: "review", Detail: "Review"}}}
	m.Asked = map[string]bool{"/a": true}
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	m.Draft = "/lar"
	tt.Frame()
	if _, ok := tt.Find("/review"); ok {
		t.Error("the menu should filter by what is typed")
	}
	if err := tt.Click("/lark-im"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Draft != "/lark-im " {
		t.Fatalf("choosing a command should fill it in: %q", m.Draft)
	}
}
