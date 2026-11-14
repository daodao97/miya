package integration

import (
	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"strings"
	"testing"
)

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
