package integration

import (
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"miya/internal/appmodel"
	"miya/internal/composer"
	"miya/internal/controller"
	"miya/internal/conversation"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	"miya/internal/svcproto"
)

func TestAgentReferencesRoundTripMixedWithThreadsAndCommands(t *testing.T) {
	agent := composer.AgentReferenceText(appmodel.AgentClaude, "Claude [review] `code` 👋")
	ref := model.ReferenceText(testReference(handoffSource()), "Context")
	raw := "/goal Use " + agent + " with " + ref + " please"
	parts := model.MessageParts(raw)
	got, err := composer.PartsText(parts)
	if err != nil || got != raw {
		t.Fatalf("parts roundtrip: %q %v", got, err)
	}
	var agents, threads int
	for _, part := range parts {
		if part.Type == "agent_ref" {
			agents++
			if part.Agent != appmodel.AgentClaude {
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
	s := conversation.InlineThreadInput{Commands: composer.ComposerCommands}
	s.SetRaw(raw)
	if got := conversation.InlineThreadRaw(s.Doc.Text()); got != raw {
		t.Fatalf("rich-text roundtrip: %q", got)
	}
	objects := 0
	for _, run := range s.Doc.Text().Runs(0, s.Doc.Text().Len()) {
		if run.Attributes.Attachment != nil {
			objects++
		}
	}
	if objects != 3 {
		t.Fatalf("command, agent and thread did not coexist: %d", objects)
	}
}

func TestAgentReferencesLeaveCodeEscapesHeadingsAndFileHashesLiteral(t *testing.T) {
	tag := composer.AgentReferenceText(appmodel.AgentCodex, "Codex")
	for _, raw := range []string{"`" + tag + "`", "```\n" + tag + "\n```", `\` + tag} {
		if len(model.AgentMentions(raw)) != 0 {
			t.Fatalf("literal became an Agent tag: %q", raw)
		}
	}
	for _, raw := range []string{"#123", "# Heading", "## Codex", "x#codex", "`#codex`", "```\n#codex", "\\#codex"} {
		if token := composer.ComposerTokenAt(raw, len(raw)); token.Kind == '#' {
			t.Fatalf("literal opened Agent menu: %q", raw)
		}
	}
	for _, raw := range []string{"#", "#c", "Please #Claude"} {
		if token := composer.ComposerTokenAt(raw, len(raw)); token.Kind != '#' {
			t.Fatalf("missing Agent completion: %q %+v", raw, token)
		}
	}
	if token := composer.ComposerTokenAt("@file#part", len("@file#part")); token.Kind != '@' || token.Query != "file#part" {
		t.Fatal("Agent completion broke a file reference containing #", token)
	}
	if _, err := composer.PartsText([]model.MessagePart{{Type: "agent_ref", Agent: "unregistered"}}); err == nil {
		t.Fatal("unknown Agent silently fell back")
	}
}

func TestAgentTagControlOnlyUsesExplicitMessageParts(t *testing.T) {
	tag := composer.AgentReferenceText(appmodel.AgentClaude, "Claude")
	context := "User task\n\nReferenced material: " + tag
	if got := composer.CollaborationTaggedInput(context, []model.MessagePart{{Type: "text", Text: "User task"}}); got != context {
		t.Fatal("a context reference caused delegation instructions")
	}
	parts := model.MessageParts("Please " + tag + " review")
	got := composer.CollaborationTaggedInput("native input", parts)
	if !strings.Contains(got, "<miya_collaboration_v1>") || !strings.Contains(got, "- claude-code\n") {
		t.Fatal("missing canonical Agent instruction")
	}
	if raw, err := composer.PartsText(parts); err != nil || strings.Contains(raw, "miya_collaboration_v1") {
		t.Fatal("internal control leaked into visible parts")
	}
}

func TestComposerAgentSelectionStagesEditableChipWithoutStartingTask(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).CollaborationRemote = &model.CollaborationStatus{Version: 1}
	tt.Frame()
	tt.Type("#claude")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	raw := composer.AgentReferenceText(appmodel.AgentClaude, "Claude Code") + " "
	if app.C.(*controller.Controller).Draft() != raw || app.Complete.Open {
		t.Fatalf("agent selection: %q", app.C.(*controller.Controller).Draft())
	}
	doc := app.Complete.Input.Doc
	if attr := doc.Text().AttributesAt(0); attr.Attachment == nil || attr.Attachment.URL != model.AgentURL(appmodel.AgentClaude) || doc.Text().String() != string(richtext.ObjectReplacement)+" " {
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
	if strings.Contains(app.C.(*controller.Controller).Draft(), "miya-agent") || !strings.Contains(app.C.(*controller.Controller).Draft(), "review 👋") {
		t.Fatal("deleting Agent chip damaged task text", app.C.(*controller.Controller).Draft())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != raw+"review 👋" {
		t.Fatal("undo lost Agent tag", app.C.(*controller.Controller).Draft())
	}
	if app.C.(*controller.Controller).Current().Busy || len(app.C.(*controller.Controller).Current().Messages) != 0 {
		t.Fatal("selecting a tag executed a task")
	}
}

func TestMobileAgentSelectionUsesSameChipAndClipboardFormat(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := testMobileApp()
			m.State.Collaboration = &model.CollaborationStatus{Version: 1}
			m.State.Agents = []phoneapi.AgentOptions{{Agent: appmodel.AgentCodex}, {Agent: appmodel.AgentClaude}}
			m.StartConversation("/a", appmodel.AgentCodex)
			m.Draft = "#claude"
			tt := ui.NewTester(m.ViewRoot, width, 750)
			tt.Frame()
			if err := tt.Click("#Claude Code"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if m.Draft != composer.AgentReferenceText(appmodel.AgentClaude, "Claude Code")+" " {
				t.Fatal("phone Agent selection lost canonical identity", m.Draft)
			}
			if a := m.InlineInput.Doc.Text().AttributesAt(0).Attachment; a == nil || a.URL != model.AgentURL(appmodel.AgentClaude) {
				t.Fatal("phone did not render a structured Agent chip")
			}
			saveCommandSnapshot(t, tt, "mobile-agent-"+strconv.Itoa(width)+".png")
			if err := tt.Click("Message"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Key(ui.Cmd, ui.KeyC)
			tt.Frame()
			if tt.Clipboard() != m.Draft {
				t.Fatal("phone clipboard used a different format", tt.Clipboard())
			}
		})
	}
}

func TestAgentTagsDoNotFallBackToOrdinaryMessagesOnOlderService(t *testing.T) {
	tag := composer.AgentReferenceText(appmodel.AgentCodex, "Codex")
	app, _ := testApp(t)
	app.C.(*controller.Controller).Remote = &svcproto.Client{}
	if id := app.C.(*controller.Controller).Submit(model.SendRequest{Text: tag + " review"}); id != 0 || !strings.Contains(app.C.(*controller.Controller).LastSubmitError, "Update the Miya") {
		t.Fatal("old service received unsupported Agent input", id, app.C.(*controller.Controller).LastSubmitError)
	}
	m := testMobileApp()
	m.Conn = &remote.Conn{}
	m.State.Collaboration = nil
	m.Draft = tag + " review"
	m.Send()
	if m.Sending || !strings.Contains(m.SendErr, "Update the Miya") || m.Draft != tag+" review" {
		t.Fatal("phone sent an unsupported tag or lost its draft", m.SendErr)
	}
}

func TestComposerAtMenuOffersAgentsForDelegation(t *testing.T) {
	app, tt := testApp(t)
	tt.Frame()
	tt.Type("@claude")
	tt.Frame()
	for _, choice := range app.Complete.Choices {
		if choice.Agent != "" {
			t.Fatal("offered Agents to a service without collaboration")
		}
	}
	tt.Key(0, ui.KeyEscape)
	app.C.(*controller.Controller).RememberDraft("")
	app.Complete.Close()
	app.C.(*controller.Controller).CollaborationRemote = &model.CollaborationStatus{Version: 1}
	tt.Frame()
	tt.Type("@claude")
	tt.Frame()
	if len(app.Complete.Choices) == 0 || app.Complete.Choices[0].Agent != appmodel.AgentClaude {
		t.Fatalf("@ menu did not list the Agent first: %+v", app.Complete.Choices)
	}
	if !tt.HasText("@Claude Code") || !tt.HasText("Agents, conversations and project files") {
		t.Fatal("Agent row or menu title missing", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.C.(*controller.Controller).Draft() != composer.AgentReferenceText(appmodel.AgentClaude, "Claude Code")+" " || app.Complete.Open {
		t.Fatalf("@ Agent selection: %q", app.C.(*controller.Controller).Draft())
	}
	if app.C.(*controller.Controller).Current().Busy || len(app.C.(*controller.Controller).Current().Messages) != 0 {
		t.Fatal("selecting an Agent executed a task")
	}
}

func TestMobileAtMenuOffersDesktopAgents(t *testing.T) {
	m := testMobileApp()
	m.State.Collaboration = &model.CollaborationStatus{Version: 1}
	m.State.Agents = []phoneapi.AgentOptions{{Agent: appmodel.AgentClaude}}
	m.StartConversation("/a", appmodel.AgentCodex)
	m.Draft = "@"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if tt.HasText("@Codex") {
		t.Fatal("listed an Agent the desktop does not offer")
	}
	if err := tt.Click("@Claude Code"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Draft != composer.AgentReferenceText(appmodel.AgentClaude, "Claude Code")+" " {
		t.Fatal("phone @ Agent selection lost canonical identity", m.Draft)
	}
}
