package controller

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/provider"
)

func TestProvidersReachEachAgentsLaunch(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := &Controller{}
	c.loadProviders()
	c.ClaudeBinary, c.ClaudeArgs = "/bin/claude", []string{"--verbose"}

	// Nothing configured: every agent keeps its own configuration.
	if got := claudeArgs(c); !slices.Equal(got, []string{"--verbose"}) {
		t.Fatalf("claude args = %q", got)
	}
	if l := c.codexLaunch(); len(l.Args) != 0 || len(l.Env) != 0 {
		t.Fatalf("codex launch = %+v", l)
	}

	if err := c.SaveProvider(provider.Edit{Name: "Relay", Endpoints: []provider.Endpoint{{API: provider.Anthropic, BaseURL: "https://relay.example"}}, APIKey: "sk-relay", Models: []string{"glm-4.6"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveProvider(provider.Edit{Name: "OpenAI-ish", Endpoints: []provider.Endpoint{{API: provider.Responses, BaseURL: "https://oa.example/v1"}}, APIKey: "sk-oa", Models: []string{"gpt-x"}}); err != nil {
		t.Fatal(err)
	}
	views := c.ProviderViews()
	if len(views) != 2 {
		t.Fatalf("views = %+v", views)
	}
	view, _ := json.Marshal(views)
	if strings.Contains(string(view), "sk-") {
		t.Fatalf("views carry a key: %s", view)
	}
	anthropic, responses := views[0].ID, views[1].ID

	if err := c.EnableProvider(agentmeta.Codex, anthropic, true); err == nil {
		t.Fatal("Codex took an Anthropic provider")
	}
	for _, u := range []provider.Use{{Agent: agentmeta.Claude, ID: anthropic}, {Agent: agentmeta.Codex, ID: responses}, {Agent: agentmeta.Pi, ID: responses}} {
		if err := c.EnableProvider(u.Agent, u.ID, true); err != nil {
			t.Fatal(err)
		}
	}

	args := claudeArgs(c)
	if len(args) != 3 || args[0] != "--verbose" || args[1] != "--settings" {
		t.Fatalf("claude args = %q", args)
	}
	data, err := os.ReadFile(args[2])
	if err != nil || !strings.Contains(string(data), `"ANTHROPIC_BASE_URL": "https://relay.example"`) || !strings.Contains(string(data), `"ANTHROPIC_AUTH_TOKEN": "sk-relay"`) {
		t.Fatalf("claude settings = %s, %v", data, err)
	}
	if cli := c.claudeCLI(); !slices.Equal(cli.Args, args) {
		t.Fatalf("memory and automations use %q, conversations %q", cli.Args, args)
	}

	codexArgs := strings.Join(c.codexLaunch().Args, " ")
	if !strings.Contains(codexArgs, `model_provider="miya-`+responses+`"`) || !strings.Contains(codexArgs, `model="gpt-x"`) || strings.Contains(codexArgs, "sk-oa") {
		t.Fatalf("codex args = %s", codexArgs)
	}
	if models, ok := c.codexProviderModels(); !ok || len(models) != 1 || models[0].Model != "gpt-x" || !models[0].IsDefault {
		t.Fatalf("codex models = %+v", models)
	}

	pi := c.piProvider()
	if pi.BaseURL != "https://oa.example/v1" || pi.Token != "sk-oa" || pi.API != "openai-responses" || !slices.Equal(pi.Models, []string{"gpt-x"}) || pi.Bearer {
		t.Fatalf("pi provider = %+v", pi)
	}

	// Turning a provider on for an agent replaces the one it had; off
	// returns the agent to its own configuration.
	if err := c.EnableProvider(agentmeta.Pi, anthropic, true); err != nil {
		t.Fatal(err)
	}
	if c.ProviderUse()[agentmeta.Pi] != anthropic || c.piProvider().API != "anthropic-messages" {
		t.Fatalf("pi use = %q, %+v", c.ProviderUse()[agentmeta.Pi], c.piProvider())
	}
	if err := c.EnableProvider(agentmeta.Claude, anthropic, false); err != nil {
		t.Fatal(err)
	}
	if got := claudeArgs(c); !slices.Equal(got, []string{"--verbose"}) {
		t.Fatalf("claude args after off = %q", got)
	}

	// Deleting a provider frees the agents on it, and the configuration
	// survives a restart.
	if err := c.DeleteProvider(responses); err != nil {
		t.Fatal(err)
	}
	fresh := &Controller{}
	fresh.loadProviders()
	if len(fresh.ProviderViews()) != 1 || fresh.ProviderUse()[agentmeta.Codex] != "" || fresh.ProviderUse()[agentmeta.Pi] != anthropic {
		t.Fatalf("reloaded views = %+v use = %v", fresh.ProviderViews(), fresh.ProviderUse())
	}
	if l := fresh.codexLaunch(); len(l.Args) != 0 {
		t.Fatalf("codex launch after delete = %q", l.Args)
	}
}

func TestCodexRestartsForProvidersOnlyWhenIdle(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := &Controller{}
	c.loadProviders()
	// No app-server running: the next Connect starts with the new launch.
	if err := c.SaveProvider(provider.Edit{Name: "R", Endpoints: []provider.Endpoint{{API: provider.Responses, BaseURL: "https://r.example/v1"}}, APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if !c.Providers.codexStale {
		t.Fatal("a changed launch was not marked for Codex")
	}
	c.Providers.codexApplied = launchKey(c.codexLaunch())
	c.restartCodexForProviders()
	if c.Providers.codexStale {
		t.Fatal("an unchanged launch was marked stale")
	}
}

// claudeArgs are the arguments Claude Code's conversations start with.
func claudeArgs(c *Controller) []string {
	return claudeAgentHost{agentWorkspace{c}}.Snapshot().Args
}

func TestOneProviderServesClaudeAndCodexThroughItsOwnEndpoints(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := &Controller{}
	c.loadProviders()
	c.ClaudeBinary = "/bin/claude"
	if err := c.SaveProvider(provider.Edit{Name: "Both", APIKey: "sk-both", Models: []string{"m"}, Endpoints: []provider.Endpoint{
		{API: provider.Anthropic, BaseURL: "https://both.example/anthropic"},
		{API: provider.Responses, BaseURL: "https://both.example/v1"},
	}}); err != nil {
		t.Fatal(err)
	}
	id := c.ProviderViews()[0].ID
	for _, agent := range []string{agentmeta.Claude, agentmeta.Codex, agentmeta.Pi} {
		if err := c.EnableProvider(agent, id, true); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
	}
	args := claudeArgs(c)
	data, err := os.ReadFile(args[len(args)-1])
	if err != nil || !strings.Contains(string(data), `"ANTHROPIC_BASE_URL": "https://both.example/anthropic"`) {
		t.Fatalf("claude settings = %s, %v", data, err)
	}
	if got := strings.Join(c.codexLaunch().Args, " "); !strings.Contains(got, `base_url="https://both.example/v1"`) || !strings.Contains(got, `model_provider="miya-`+id+`"`) {
		t.Fatalf("codex args = %s", got)
	}
	if pi := c.piProvider(); pi.API != "anthropic-messages" || pi.BaseURL != "https://both.example/anthropic" || !pi.Bearer {
		t.Fatalf("pi = %+v", pi)
	}
}
