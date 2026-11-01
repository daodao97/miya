package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestClaudeTakesCommand(t *testing.T) {
	for _, tc := range []struct {
		agent, command string
		want           bool
	}{
		{agentClaude, "review the diff", true},
		{agentClaude, "lark-im", true},
		{agentClaude, "compact", true},
		{agentClaude, "model", false},
		{agentClaude, "new", false},
		{agentCodex, "lark-im", false},
		{agentCodex, "compact", false},
	} {
		if got := claudeTakesCommand(tc.agent, tc.command); got != tc.want {
			t.Errorf("claudeTakesCommand(%q, %q) = %v, want %v", tc.agent, tc.command, got, tc.want)
		}
	}
}

func TestClaudeCommandChoices(t *testing.T) {
	c := newViewerController()
	c.setAgent(agentClaude)
	c.cwd = "/p"
	c.claudeCmdCache = map[string]claudeCommandList{"/p": {at: time.Now(), keep: time.Hour, choice: []composerChoice{{Name: "review", Detail: "Claude review"}, {Name: "lark-im", Detail: "IM"}, {Name: "model", Detail: "Claude model"}}}}
	app := &chatApp{c: c}
	names := map[string]string{}
	for _, ch := range app.commandChoices("") {
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
	c.setAgent(agentCodex)
	if got := app.commandChoices("lark"); len(got) != 0 {
		t.Errorf("Codex conversations should not list Claude Code's commands: %v", got)
	}
}

func TestMobileCommandMenu(t *testing.T) {
	m := testMobileApp()
	m.state.Projects[0].Threads[0].Agent = agentClaude
	m.commands = map[string][]composerChoice{"/a": {{Name: "lark-im", Detail: "IM"}, {Name: "review", Detail: "Review"}}}
	m.asked = map[string]bool{"/a": true}
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	m.draft = "/lar"
	tt.Frame()
	if _, ok := tt.Find("/review"); ok {
		t.Error("the menu should filter by what is typed")
	}
	if err := tt.Click("/lark-im"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.draft != "/lark-im " {
		t.Fatalf("choosing a command should fill it in: %q", m.draft)
	}
}
