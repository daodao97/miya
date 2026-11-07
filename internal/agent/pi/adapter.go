// Package pi integrates the pi coding agent. Each conversation has one pi
// process in RPC mode that lives across its turns; Miya keeps a copy of the
// conversation and the session file pi resumes from.
package pi

import (
	"context"
	"slices"
	"strings"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/interaction"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	"miya/internal/textutil"
	"miya/locales"
)

func tr(s string) string { return locales.T(s) }

// State is what the workspace knows about pi.
type State struct {
	Binary, Error, Command string
	Args, Env              []string
	Model, Effort          string
	// Provider is the endpoint of Miya's Claude Code settings, which pi uses
	// when it is usable; otherwise pi uses its own configuration.
	Provider pinative.Provider
	// ClaudeModel is the model Claude Code runs by default.
	ClaudeModel string
}

type Host interface {
	agent.Workspace
	agent.Executions
	Snapshot() State
	Run(int) *pinative.Client
	SetRun(int, *pinative.Client)
	SetCommand(string)
	SetModel(string)
	SetEffort(string)
	ReserveExecutionEpoch(string, string) string
	NoteRuntime(model.Thread)
	ManagedStarted(int)
	ManagedResult(int, dg.State, string)
	RemoveThread(int, string)
}

type Adapter struct{ host Host }

func New(host Host) *Adapter { return &Adapter{host: host} }

var _ agent.Adapter = (*Adapter)(nil)

func (b Adapter) ID() string                   { return agentmeta.Pi }
func (b Adapter) Name() string                 { return model.AgentName(agentmeta.Pi) }
func (b Adapter) Icon() agentmeta.Icon         { return agentmeta.For(agentmeta.Pi).Icon }
func (b Adapter) Caps() agentmeta.Capabilities { return agentmeta.For(agentmeta.Pi).Capabilities }
func (b Adapter) Discover()                    {}
func (b Adapter) Ready() bool                  { return b.host.Snapshot().Binary != "" }
func (b Adapter) Problem() (string, bool)      { return b.host.Snapshot().Error, false }
func (b Adapter) Reconnect()                   {}
func (b Adapter) Open(t model.Thread)          { b.LoadHistory(t) }

// Stop aborts the running turn; the process stays for the next one.
func (b Adapter) Stop(t model.Thread) {
	if run := b.host.Run(t.ID); run != nil {
		go func() {
			if run.Abort() != nil {
				run.Stop()
			}
		}()
	}
}

func (b Adapter) stopProcess(t model.Thread) {
	if run := b.host.Run(t.ID); run != nil {
		b.host.SetRun(t.ID, nil)
		go run.Stop()
	}
}

// Archive and delete forget Miya's entry; pi keeps its session file.
func (b Adapter) Archive(t model.Thread) {
	b.stopProcess(t)
	b.host.RemoveThread(t.ID, t.ServerID)
}

func (b Adapter) Delete(t model.Thread) {
	b.Archive(t)
	pinative.RemoveRecord(t.ServerID)
}

func (b Adapter) Suspend(t model.Thread) {
	b.stopProcess(t)
	t.Busy = false
	pinative.SaveRecord(t, "")
}

// Pi runs its tools without asking, so there is nothing to reply to.
func (b Adapter) Reply(interaction.Request, interaction.Response) {}

func (b Adapter) Settings() []agent.Setting {
	c := b.host
	return []agent.Setting{{
		ID:          "pi-command",
		Title:       tr("Pi launch command"),
		Help:        tr("Leave blank to use pi. Arguments and environment variables are supported. Pi uses the Anthropic provider of the Claude Code launch command when it sets ANTHROPIC_BASE_URL and a token, otherwise its own configuration. Pi runs tools without asking for approval."),
		Placeholder: "pi",
		Value:       func() string { return c.Snapshot().Command },
		Save:        func(v string) { c.SetCommand(v) },
		Status: func() (string, bool) {
			s := c.Snapshot()
			if s.Error != "" {
				return s.Error, true
			}
			parts := append(append(append([]string(nil), s.Env...), s.Binary), s.Args...)
			line := "→ " + strings.Join(parts, " ")
			if s.Provider.Usable() {
				line += "\n" + tr("Provider: ") + s.Provider.BaseURL + " (" + tr("from Claude Code settings") + ")"
			} else {
				line += "\n" + tr("Provider: pi's own configuration")
			}
			return line, false
		},
	}}
}

func (b Adapter) Commands(string, bool) agent.CommandCatalog {
	var host []model.ComposerChoice
	for _, cmd := range composer.ComposerCommands {
		if agent.IsHostCommand(cmd.Name) {
			host = append(host, cmd)
		}
	}
	return agent.CommandCatalog{Choices: host, Known: true}
}

func (b Adapter) RunCommand(_ int, command, _ string) (int, string) {
	if command == "fast" || command == "fast on" || command == "fast off" {
		return 0, tr("/fast is only supported by Codex.")
	}
	return 0, b.Name() + tr(" does not support this command.")
}

func (b Adapter) CanRewind(model.Thread, model.Message) bool { return false }
func (b Adapter) Rewind(model.Thread, model.Message, string) {}

// Steer adds a message to the running turn; pi delivers it before its next
// model call.
func (b Adapter) Steer(t model.Thread, req model.SendRequest) bool {
	run := b.host.Run(t.ID)
	if run == nil || !run.Streaming() {
		return false
	}
	req.Steer = true
	b.Send(req)
	return true
}

func (b Adapter) Runtime(t model.Thread) tracker.SnapshotSource {
	caps := rt.Capabilities{Snapshot: true, SessionRoot: true, RequiresHost: true}
	run := b.host.Run(t.ID)
	if run == nil {
		return tracker.SnapshotSource{Epoch: "disconnected", Caps: caps, Alive: func() bool { return false }}
	}
	return tracker.SnapshotSource{Epoch: run.Epoch(), Caps: caps, Alive: run.Alive, Snapshot: func(context.Context) (tracker.TaskSnapshot, error) {
		return tracker.TaskSnapshot{Known: true, Tasks: []rt.Task{}, Roots: []rt.Root{{PID: run.PID(), TaskID: "_session"}}}, nil
	}}
}

// Models are those of the Claude provider, which Miya registers with pi.
// Without it pi chooses from its own configuration.
var piModels = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"}

var piEfforts = []string{"", "off", "minimal", "low", "medium", "high", "xhigh"}

func providerModels(s State) []string {
	out := append([]string(nil), piModels...)
	for _, m := range []string{s.Provider.Model, s.ClaudeModel} {
		if m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// defaultModel is what a turn runs when no model is chosen.
func defaultModel(s State) string {
	if !s.Provider.Usable() {
		return ""
	}
	return textutil.FirstNonEmpty(s.Provider.Model, s.ClaudeModel, piModels[1])
}

func (b Adapter) ModelChoices() []string {
	s := b.host.Snapshot()
	out := []string{""}
	if s.Provider.Usable() {
		out = append(out, providerModels(s)...)
	}
	if s.Model != "" && !slices.Contains(out, s.Model) {
		out = append(out, s.Model)
	}
	return out
}

func (b Adapter) ModelLabel(v string) string {
	if v != "" {
		return v
	}
	if m := defaultModel(b.host.Snapshot()); m != "" {
		return tr("Default · ") + m
	}
	return tr("Default model")
}

func (b Adapter) EffortChoices() []string { return piEfforts }
func (b Adapter) Model() string           { return b.host.Snapshot().Model }
func (b Adapter) Effort() string          { return b.host.Snapshot().Effort }
func (b Adapter) SetModel(v string)       { b.host.SetModel(v) }
func (b Adapter) SetEffort(v string)      { b.host.SetEffort(v) }

func (b Adapter) ModelOptions() []agent.ModelOption {
	var out []agent.ModelOption
	for _, v := range b.ModelChoices() {
		out = append(out, agent.ModelOption{Value: v, Label: b.ModelLabel(v), Efforts: append([]string(nil), piEfforts...)})
	}
	return out
}
