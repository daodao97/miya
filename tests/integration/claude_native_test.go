package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	chattext "miya/internal/chat/text"
	"miya/internal/controller"
	"miya/internal/uiruntime"
)

func writeClaudeSession(t *testing.T, cwd, id string, lines ...string) string {
	t.Helper()
	dir := transcript.ProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeProjectDirEncoding(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	if got := transcript.ProjectDir("/Users/ada/work/my_app.v2"); got != "/cfg/projects/-Users-ada-work-my-app-v2" {
		t.Fatalf("encoded %q", got)
	}
}

func TestParseClaudeSessionFollowsActiveBranch(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	path := writeClaudeSession(t, cwd, "s1",
		`{"type":"user","uuid":"u1","sessionId":"s1","cwd":"/work/demo","timestamp":"2026-10-31T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>context</system-reminder>"},{"type":"text","text":"Read main.go"}]}}`,
		`{"type":"api-request","sessionId":"s1"}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-10-31T01:00:02Z","message":{"content":[{"type":"thinking","thinking":"Look first"},{"type":"tool_use","id":"tool1","name":"Read","input":{"file_path":"main.go"}}]}}`,
		`{"type":"user","uuid":"r1","parentUuid":"a1","timestamp":"2026-10-31T01:00:03Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"package main"}]}}`,
		// An abandoned reply, which a rewind replaced.
		`{"type":"assistant","uuid":"old","parentUuid":"r1","timestamp":"2026-10-31T01:00:04Z","message":{"content":[{"type":"text","text":"Abandoned"}]}}`,
		`{"type":"assistant","uuid":"side","parentUuid":"r1","isSidechain":true,"message":{"content":[{"type":"text","text":"Subagent"}]}}`,
		`{"type":"user","uuid":"meta","parentUuid":"r1","isMeta":true,"message":{"content":"Caveat: hidden"}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"meta","timestamp":"2026-10-31T01:00:05Z","message":{"content":[{"type":"text","text":"It is a main package."}]}}`,
		`not json`,
	)
	messages, err := transcript.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, m := range messages {
		roles = append(roles, m.Role)
		if strings.Contains(m.Content, "Abandoned") || strings.Contains(m.Content, "Subagent") || strings.Contains(m.Content, "Caveat") || strings.Contains(m.Content, "system-reminder") {
			t.Fatalf("off-branch or hidden content shown: %q", m.Content)
		}
	}
	if got := strings.Join(roles, ","); got != "user,activity,tool,assistant" {
		t.Fatalf("roles %s: %+v", got, messages)
	}
	if messages[0].Content != "Read main.go" || messages[0].SentAtMs == 0 {
		t.Fatalf("prompt %+v", messages[0])
	}
	tool := messages[2]
	if tool.Kind != "claudeTool" || tool.Status != "completed" || !strings.HasPrefix(tool.Content, "Read\n") || !strings.Contains(tool.Content, "package main") {
		t.Fatalf("tool %+v", tool)
	}
	if got := msgfmt.TurnElapsedLabel(messages, false); got != "Worked for 5s" {
		t.Fatalf("elapsed %q", got)
	}
}

func TestListClaudeSessionsTitlesAndWorkspace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := "/work/demo"
	writeClaudeSession(t, cwd, "prompt", `{"type":"user","uuid":"u","cwd":"/work/demo","message":{"content":"Fix   the\nflaky test"}}`)
	writeClaudeSession(t, cwd, "titled",
		`{"type":"user","uuid":"u","cwd":"/work/demo","message":{"content":"hello"}}`,
		`{"type":"ai-title","aiTitle":"Greeting"}`,
		`{"type":"custom-title","customTitle":"My session"}`)
	writeClaudeSession(t, cwd, "empty", `{"type":"mode","mode":"default"}`)
	// /work-demo encodes to the same folder; its session is not this workspace's.
	writeClaudeSession(t, cwd, "other", `{"type":"user","uuid":"u","cwd":"/work-demo","message":{"content":"elsewhere"}}`)
	titles := map[string]string{}
	for _, s := range transcript.List(cwd) {
		titles[s.ID] = s.Title
	}
	if len(titles) != 2 || titles["prompt"] != "Fix the flaky test" || titles["titled"] != "My session" {
		t.Fatalf("sessions %v", titles)
	}
	if transcript.Find("/somewhere/else", "titled") == "" || transcript.Find(cwd, "../titled") != "" {
		t.Fatal("session lookup")
	}
}

func TestNativeClaudeSessionsJoinTheSidebarAndLoad(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	uiruntime.InlineDispatch.Store(true)
	t.Cleanup(func() { uiruntime.InlineDispatch.Store(false) })
	cwd := "/work/demo"
	writeClaudeSession(t, cwd, "cli-session",
		`{"type":"user","uuid":"u","cwd":"/work/demo","timestamp":"2026-10-31T01:00:00Z","message":{"content":"Started in the CLI"}}`,
		`{"type":"assistant","uuid":"a","parentUuid":"u","timestamp":"2026-10-31T01:00:01Z","message":{"content":[{"type":"text","text":"Answer from the CLI"}]}}`)
	c := testController()
	c.Cwd = cwd
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.Agent, c.SetAgent = uiruntime.CreateSignal("codex")
	c.LoadClaudeSessionsSync()
	var native model.Thread
	for _, th := range c.Threads() {
		if th.ServerID == "cli-session" {
			native = th
		}
	}
	if native.ID == 0 || native.Agent != "claude-code" || !native.Resumed || native.Title != "Started in the CLI" {
		t.Fatalf("native session not listed: %+v", c.Threads())
	}
	before := len(c.Threads())
	c.LoadClaudeSessionsSync()
	if len(c.Threads()) != before {
		t.Fatal("session listed twice")
	}
	c.SelectThread(native)
	waitClaudeHistory(c)
	cur := c.Current()
	if !cur.HistoryLoaded || len(cur.Messages) != 2 || cur.Messages[1].Content != "Answer from the CLI" || cur.Messages[1].ID == 0 {
		t.Fatalf("history not loaded: %+v", cur)
	}
}

// TestReadRealClaudeSessions reads this machine's sessions of the working
// directory, read only: MIYA_CLAUDE_REAL=1 go test ./tests/integration -run TestReadRealClaudeSessions -v
func TestReadRealClaudeSessions(t *testing.T) {
	if os.Getenv("MIYA_CLAUDE_REAL") == "" {
		t.Skip("set MIYA_CLAUDE_REAL=1")
	}
	cwd, _ := os.Getwd()
	for _, s := range transcript.List(cwd) {
		messages, err := transcript.Parse(s.Path)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, m := range messages {
			counts[m.Role]++
		}
		groups := msgfmt.GroupMessages(messages)
		t.Logf("%s %q updated %s: %d messages %v, %d groups", s.ID, s.Title, s.UpdatedAt.Format("15:04:05"), len(messages), counts, len(groups))
		if len(messages) > 0 {
			t.Logf("first: %q", chattext.ClippedTitle(messages[0].Content))
			t.Logf("last: %s %q", messages[len(messages)-1].Role, chattext.ClippedTitle(messages[len(messages)-1].Content))
		}
	}
}

func TestClaudeCommandSettingPersistsAndResolves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_COMMAND", "")
	t.Cleanup(func() { transcript.ConfigOverride = "" })
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &controller.Controller{}
	c.SetClaudeCommand("CLAUDE_CONFIG_DIR=/cfg/work " + bin + " --settings ~/client-settings.json")
	if c.ClaudeError != "" || c.ClaudeBinary != bin || len(c.ClaudeArgs) != 2 || !strings.HasSuffix(c.ClaudeArgs[1], "/client-settings.json") || strings.HasPrefix(c.ClaudeArgs[1], "~") {
		t.Fatalf("resolved %q %q %q", c.ClaudeBinary, c.ClaudeArgs, c.ClaudeError)
	}
	if transcript.ConfigDir() != "/cfg/work" {
		t.Fatalf("config dir %q", transcript.ConfigDir())
	}
	if got := claude.LoadCommand(); got != c.ClaudeCommand {
		t.Fatalf("saved %q", got)
	}
	t.Setenv("CLAUDE_COMMAND", "claude --model opus")
	if claude.LoadCommand() != "claude --model opus" {
		t.Fatal("CLAUDE_COMMAND does not override the setting")
	}
	c.SetClaudeCommand("/missing/claude")
	if c.ClaudeError == "" || c.ClaudeBinary != "" || backendReadyFor(c, "claude-code") {
		t.Fatal("a missing executable resolved")
	}
}
