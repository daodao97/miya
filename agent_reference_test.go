package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"miya/internal/remote"
)

func TestAgentReferencesRoundTripMixedWithThreadsAndCommands(t *testing.T) {
	agent := agentReferenceText(agentClaude, "Claude [review] `code` 👋")
	ref := referenceText(testReference(handoffSource()), "Context")
	raw := "/goal Use " + agent + " with " + ref + " please"
	parts := messageParts(raw)
	got, err := partsText(parts)
	if err != nil || got != raw {
		t.Fatalf("parts roundtrip: %q %v", got, err)
	}
	var agents, threads int
	for _, part := range parts {
		if part.Type == "agent_ref" {
			agents++
			if part.Agent != agentClaude {
				t.Fatal("agent restored from label instead of identity")
			}
		}
		if part.Type == "thread_ref" {
			threads++
		}
	}
	if agents != 1 || threads != 1 {
		t.Fatal("mixed references missing", parts)
	}
	s := inlineThreadInput{commands: composerCommands}
	s.setRaw(raw)
	if got := inlineThreadRaw(s.doc.Text()); got != raw {
		t.Fatalf("rich-text roundtrip: %q", got)
	}
	objects := 0
	for _, run := range s.doc.Text().Runs(0, s.doc.Text().Len()) {
		if run.Attributes.Attachment != nil {
			objects++
		}
	}
	if objects != 3 {
		t.Fatalf("command, agent and thread did not coexist: %d", objects)
	}
}

func TestAgentReferencesLeaveCodeEscapesHeadingsAndFileHashesLiteral(t *testing.T) {
	tag := agentReferenceText(agentCodex, "Codex")
	for _, raw := range []string{"`" + tag + "`", "```\n" + tag + "\n```", `\` + tag} {
		if len(agentMentions(raw)) != 0 {
			t.Fatalf("literal became an Agent tag: %q", raw)
		}
	}
	for _, raw := range []string{"#123", "# Heading", "## Codex", "x#codex", "`#codex`", "```\n#codex", "\\#codex"} {
		if token := composerTokenAt(raw, len(raw)); token.Kind == '#' {
			t.Fatalf("literal opened Agent menu: %q", raw)
		}
	}
	for _, raw := range []string{"#", "#c", "Please #Claude"} {
		if token := composerTokenAt(raw, len(raw)); token.Kind != '#' {
			t.Fatalf("missing Agent completion: %q %+v", raw, token)
		}
	}
	if token := composerTokenAt("@file#part", len("@file#part")); token.Kind != '@' || token.Query != "file#part" {
		t.Fatal("Agent completion broke a file reference containing #", token)
	}
	if _, err := partsText([]MessagePart{{Type: "agent_ref", Agent: "unregistered"}}); err == nil {
		t.Fatal("unknown Agent silently fell back")
	}
}

func TestAgentTagControlOnlyUsesExplicitMessageParts(t *testing.T) {
	tag := agentReferenceText(agentClaude, "Claude")
	context := "User task\n\nReferenced material: " + tag
	if got := collaborationTaggedInput(context, []MessagePart{{Type: "text", Text: "User task"}}); got != context {
		t.Fatal("a context reference caused delegation instructions")
	}
	parts := messageParts("Please " + tag + " review")
	got := collaborationTaggedInput("native input", parts)
	if !strings.Contains(got, "<miya_collaboration_v1>") || !strings.Contains(got, "- claude-code\n") {
		t.Fatal("missing canonical Agent instruction")
	}
	if raw, err := partsText(parts); err != nil || strings.Contains(raw, "miya_collaboration_v1") {
		t.Fatal("internal control leaked into visible parts")
	}
}

func TestComposerAgentSelectionStagesEditableChipWithoutStartingTask(t *testing.T) {
	app, tt := testApp(t)
	app.c.collaborationRemote = &collaborationStatus{Version: 1}
	tt.Frame()
	tt.Type("#claude")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	raw := agentReferenceText(agentClaude, "Claude Code") + " "
	if app.c.draft() != raw || app.complete.open {
		t.Fatalf("agent selection: %q", app.c.draft())
	}
	doc := app.complete.input.doc
	if attr := doc.Text().AttributesAt(0); attr.Attachment == nil || attr.Attachment.URL != agentURL(agentClaude) || doc.Text().String() != string(richtext.ObjectReplacement)+" " {
		t.Fatal("Agent tag is not an editable object")
	}
	saveCommandSnapshot(t, tt, "desktop-agent.png")
	tt.Type("review 👋")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if tt.Clipboard() != raw+"review 👋" {
		t.Fatal("copy lost Agent identity", tt.Clipboard())
	}
	doc.SetSelection(1, 1)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if strings.Contains(app.c.draft(), "miya-agent") || !strings.Contains(app.c.draft(), "review 👋") {
		t.Fatal("deleting Agent chip damaged task text", app.c.draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if app.c.draft() != raw+"review 👋" {
		t.Fatal("undo lost Agent tag", app.c.draft())
	}
	if app.c.current().Busy || len(app.c.current().Messages) != 0 {
		t.Fatal("selecting a tag executed a task")
	}
}

func TestMobileAgentSelectionUsesSameChipAndClipboardFormat(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testMobileApp()
			m.state.Collaboration = &collaborationStatus{Version: 1}
			m.state.Agents = []phoneAgentOptions{{Agent: agentCodex}, {Agent: agentClaude}}
			m.startConversation("/a", agentCodex)
			m.draft = "#claude"
			tt := ui.NewTester(m.viewRoot, width, 750)
			tt.Frame()
			if err := tt.Click("#Claude Code"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if m.draft != agentReferenceText(agentClaude, "Claude Code")+" " {
				t.Fatal("phone Agent selection lost canonical identity", m.draft)
			}
			if a := m.inlineInput.doc.Text().AttributesAt(0).Attachment; a == nil || a.URL != agentURL(agentClaude) {
				t.Fatal("phone did not render a structured Agent chip")
			}
			saveCommandSnapshot(t, tt, "mobile-agent-"+strconv.Itoa(width)+".png")
			if err := tt.Click("Message"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Key(ui.Cmd, ui.KeyC)
			tt.Frame()
			if tt.Clipboard() != m.draft {
				t.Fatal("phone clipboard used a different format", tt.Clipboard())
			}
		})
	}
}

func TestAgentTagsDoNotFallBackToOrdinaryMessagesOnOlderService(t *testing.T) {
	tag := agentReferenceText(agentCodex, "Codex")
	app, _ := testApp(t)
	app.c.remote = &serviceClient{}
	if id := app.c.submit(sendRequest{Text: tag + " review"}); id != 0 || !strings.Contains(app.c.lastSubmitError, "Update the Miya") {
		t.Fatal("old service received unsupported Agent input", id, app.c.lastSubmitError)
	}
	m := testMobileApp()
	m.conn = &remote.Conn{}
	m.state.Collaboration = nil
	m.draft = tag + " review"
	m.send()
	if m.sending || !strings.Contains(m.sendErr, "Update the Miya") || m.draft != tag+" review" {
		t.Fatal("phone sent an unsupported tag or lost its draft", m.sendErr)
	}
}

func TestComposerAtMenuOffersAgentsForDelegation(t *testing.T) {
	app, tt := testApp(t)
	tt.Frame()
	tt.Type("@claude")
	tt.Frame()
	for _, choice := range app.complete.choices {
		if choice.Agent != "" {
			t.Fatal("offered Agents to a service without collaboration")
		}
	}
	tt.Key(0, ui.KeyEscape)
	app.c.rememberDraft("")
	app.complete.close()
	app.c.collaborationRemote = &collaborationStatus{Version: 1}
	tt.Frame()
	tt.Type("@claude")
	tt.Frame()
	if len(app.complete.choices) == 0 || app.complete.choices[0].Agent != agentClaude {
		t.Fatalf("@ menu did not list the Agent first: %+v", app.complete.choices)
	}
	if !tt.HasText("@Claude Code") || !tt.HasText("Agents, conversations and project files") {
		t.Fatal("Agent row or menu title missing", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.c.draft() != agentReferenceText(agentClaude, "Claude Code")+" " || app.complete.open {
		t.Fatalf("@ Agent selection: %q", app.c.draft())
	}
	if app.c.current().Busy || len(app.c.current().Messages) != 0 {
		t.Fatal("selecting an Agent executed a task")
	}
}

func TestMobileAtMenuOffersDesktopAgents(t *testing.T) {
	m := testMobileApp()
	m.state.Collaboration = &collaborationStatus{Version: 1}
	m.state.Agents = []phoneAgentOptions{{Agent: agentClaude}}
	m.startConversation("/a", agentCodex)
	m.draft = "@"
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if tt.HasText("@Codex") {
		t.Fatal("listed an Agent the desktop does not offer")
	}
	if err := tt.Click("@Claude Code"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.draft != agentReferenceText(agentClaude, "Claude Code")+" " {
		t.Fatal("phone @ Agent selection lost canonical identity", m.draft)
	}
}
