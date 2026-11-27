package controller

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"miya/internal/agent/catalog"
	claude "miya/internal/agent/claude/native"
	codex "miya/internal/agent/codex/native"
	pinative "miya/internal/agent/pi/native"
	"miya/internal/agent/provider"
	"miya/internal/chat/model"
	"miya/internal/uiruntime"
)

// providerState is the providers configured in Miya and what the processes
// of each agent get appended for the one it uses. The service host keeps
// the configuration and its keys; a window mirrors views without keys.
type providerState struct {
	cfg   provider.Config
	views []provider.View
	use   map[string]string
	// claudeArgs are ClaudeArgs with the provider's --settings, made again
	// when claudeFrom changes or after a few seconds, so an edit to a
	// --settings file of the user's own reaches the next turn.
	claudeArgs []string
	claudeFrom string
	claudeAt   time.Time
	codex      provider.Launch
	// codexApplied is the Codex launch the running app-server has;
	// codexStale says it differs and the app-server restarts once idle.
	codexApplied string
	codexStale   bool
	err          string
}

// loadProviders reads the configuration as the controller starts.
func (c *Controller) loadProviders() {
	cfg, err := provider.Load()
	c.Providers.err = ""
	if err != nil {
		c.Providers.err = err.Error()
	}
	c.Providers.cfg = cfg
	c.applyProviders()
	c.Providers.codexApplied = launchKey(c.Providers.codex)
}

// applyProviders makes what the agents' next processes get from the
// configuration.
func (c *Controller) applyProviders() {
	p := &c.Providers
	p.views, p.use = p.cfg.Views(), maps.Clone(p.cfg.Use)
	p.codex = provider.CodexLaunch(p.cfg)
	p.claudeFrom, p.claudeAt = "", time.Time{}
	c.Pi.providerAt = time.Time{}
	if c.Memories().Engine != nil {
		c.Memories().Engine.Configure(c.claudeCLI())
	}
	uiruntime.Invalidate()
}

// ProviderViews are the configured providers, without their keys.
func (c *Controller) ProviderViews() []provider.View { return c.Providers.views }

// ProviderUse is the provider each agent uses, by agent; absent is the
// agent's own configuration.
func (c *Controller) ProviderUse() map[string]string { return c.Providers.use }

// ProviderError is the last failure to read, save or apply providers.
func (c *Controller) ProviderError() string { return c.Providers.err }

// SaveProvider adds or changes a provider.
func (c *Controller) SaveProvider(e provider.Edit) error {
	if c.Remote != nil {
		return c.Remote.Call("providerSave", e, nil)
	}
	cfg := cloneProviders(c.Providers.cfg)
	if _, err := cfg.Apply(e); err != nil {
		return err
	}
	return c.commitProviders(cfg)
}

// DeleteProvider removes a provider; agents that used it return to their
// own configuration.
func (c *Controller) DeleteProvider(id string) error {
	if c.Remote != nil {
		return c.Remote.Call("providerDelete", id, nil)
	}
	cfg := cloneProviders(c.Providers.cfg)
	cfg.Delete(id)
	return c.commitProviders(cfg)
}

// EnableProvider turns a provider on or off for one agent. An agent uses
// one provider at a time: turning one on turns off the one it had.
func (c *Controller) EnableProvider(agent, id string, on bool) error {
	u := provider.Use{Agent: agent, ID: id}
	if !on {
		if c.Providers.use[agent] != id {
			return nil
		}
		u.ID = ""
	}
	return c.UseProvider(u)
}

// UseProvider records the provider an agent uses; ID "" is the agent's own
// configuration.
func (c *Controller) UseProvider(u provider.Use) error {
	if c.Remote != nil {
		return c.Remote.Call("providerUse", u, nil)
	}
	cfg := cloneProviders(c.Providers.cfg)
	if err := cfg.Choose(u); err != nil {
		return err
	}
	return c.commitProviders(cfg)
}

// ProviderModelsFetch returns the request for a provider's models, as
// edited, to run off the main thread; an edit without a key uses the saved
// provider's. It is made on the main thread, which owns the configuration.
func (c *Controller) ProviderModelsFetch(e provider.Edit) func(context.Context) ([]string, error) {
	if remote := c.Remote; remote != nil {
		return func(context.Context) ([]string, error) {
			var out []string
			err := remote.Call("providerModels", e, &out)
			return out, err
		}
	}
	p := c.providerToFetch(e)
	return func(ctx context.Context) ([]string, error) { return provider.FetchModels(ctx, p) }
}

// providerToFetch is the provider an edit describes, with the saved key
// when the edit has none.
func (c *Controller) providerToFetch(e provider.Edit) provider.Provider {
	p := provider.Provider{APIKey: strings.TrimSpace(e.APIKey)}
	for _, ep := range e.Endpoints {
		if u := strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/"); u != "" {
			p.Endpoints = append(p.Endpoints, provider.Endpoint{API: ep.API, BaseURL: u})
		}
	}
	if p.APIKey == "" {
		if saved, ok := c.Providers.cfg.Find(e.ID); ok {
			p.APIKey = saved.APIKey
		}
	}
	return p
}

// commitProviders saves cfg and applies it.
func (c *Controller) commitProviders(cfg provider.Config) error {
	if err := provider.Save(cfg); err != nil {
		c.Providers.err = err.Error()
		uiruntime.Invalidate()
		return err
	}
	c.Providers.cfg, c.Providers.err = cfg, ""
	c.applyProviders()
	c.restartCodexForProviders()
	return nil
}

func cloneProviders(cfg provider.Config) provider.Config {
	out := provider.Config{Providers: slices.Clone(cfg.Providers), Use: maps.Clone(cfg.Use)}
	for i := range out.Providers {
		out.Providers[i].Models = slices.Clone(out.Providers[i].Models)
	}
	return out
}

// claudeLaunchArgs are the Claude Code command's arguments, with the
// provider chosen for Claude Code when there is one.
func (c *Controller) claudeLaunchArgs() []string {
	p, ok := c.Providers.cfg.For(catalog.Claude)
	if c.Remote != nil || !ok {
		return c.ClaudeArgs
	}
	from := p.ID + "\x00" + strings.Join(c.ClaudeArgs, "\x00")
	if from == c.Providers.claudeFrom && time.Since(c.Providers.claudeAt) < 5*time.Second {
		return c.Providers.claudeArgs
	}
	args, err := provider.ClaudeArgs(p, c.ClaudeArgs)
	if err != nil {
		// Without its settings file Claude Code would reach the user's own
		// account in place of the provider: say so, and keep the last.
		c.Providers.err = "Claude Code provider: " + err.Error()
		if c.Providers.claudeArgs != nil {
			return c.Providers.claudeArgs
		}
		return c.ClaudeArgs
	}
	c.Providers.claudeArgs, c.Providers.claudeFrom, c.Providers.claudeAt = args, from, time.Now()
	return args
}

// claudeCLI is Claude Code's command for Miya's own uses of it — memory
// extraction, automations — on the same provider as its conversations.
func (c *Controller) claudeCLI() claude.CLI {
	return claude.CLI{Binary: c.ClaudeBinary, Args: append([]string(nil), c.claudeLaunchArgs()...), Env: append([]string(nil), c.ClaudeEnv...)}
}

// piConfiguredProvider is the provider chosen for pi, as pi's extension
// registers it.
func (c *Controller) piConfiguredProvider() (pinative.Provider, bool) {
	p, ok := c.Providers.cfg.For(catalog.Pi)
	if !ok {
		return pinative.Provider{}, false
	}
	return pinative.Provider{
		BaseURL: p.BaseURL, Token: p.APIKey, Bearer: p.API == provider.Anthropic,
		Model: p.Model(), Source: p.Name, API: provider.PiAPI(p.API), Name: p.Name, Models: slices.Clone(p.Models),
	}, true
}

// codexLaunch is what the Codex app-server gets for providers.
func (c *Controller) codexLaunch() provider.Launch { return c.Providers.codex }

// codexProviderModels are the models of the provider Codex uses, which
// replace the list Codex reports: it lists OpenAI's own whatever its
// provider is.
func (c *Controller) codexProviderModels() ([]codex.Model, bool) {
	p, ok := c.Providers.cfg.For(catalog.Codex)
	if !ok || len(p.Models) == 0 {
		return nil, false
	}
	out := make([]codex.Model, len(p.Models))
	for i, m := range p.Models {
		out[i] = codex.Model{ID: m, Model: m, DisplayName: m, IsDefault: i == 0}
	}
	return out, true
}

func launchKey(l provider.Launch) string {
	return strings.Join(l.Args, "\x00") + "\x01" + strings.Join(l.Env, "\x00")
}

// codexBusy is whether a Codex conversation is running a turn.
func (c *Controller) codexBusy() bool {
	if c.Threads == nil {
		return false
	}
	for _, t := range c.Threads() {
		if t.Busy && (t.Agent == "" || t.Agent == model.AgentCodex) {
			return true
		}
	}
	return false
}

// restartCodexForProviders restarts the Codex app-server when what it was
// started with for providers changed: at once while no Codex turn runs,
// else after the last one completes (codexTurnSettled). A running turn is
// never cut off for a settings change.
func (c *Controller) restartCodexForProviders() {
	if launchKey(c.Providers.codex) == c.Providers.codexApplied {
		c.Providers.codexStale = false
		return
	}
	c.Providers.codexStale = true
	if c.Client == nil && !c.Connecting {
		// nothing started yet: the next Connect starts with the new launch
		return
	}
	if c.codexBusy() {
		return
	}
	c.restartCodex()
}

// codexTurnSettled restarts a stale app-server once Codex is idle.
func (c *Controller) codexTurnSettled() {
	if c.Providers.codexStale && !c.codexBusy() {
		c.restartCodex()
	}
}

// restartCodex closes the app-server and starts it again, as a change of
// agent environment does.
func (c *Controller) restartCodex() {
	c.connectionID++
	if c.connectionCancel != nil {
		c.connectionCancel()
	}
	if c.Client != nil {
		old := c.Client
		c.Client = nil
		go old.Close()
	}
	c.Connecting = false
	c.SetReady(false)
	c.Connect()
}
