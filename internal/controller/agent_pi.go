package controller

import (
	"strings"
	"time"

	claude "miya/internal/agent/claude/native"
	piagent "miya/internal/agent/pi"
	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

// piState is pi's launch command and its choices, as Claude's are kept.
type piState struct {
	Command, Binary, Error string
	Args, Env              []string
	Model, Effort          string
	Runs                   map[int]*pinative.Client

	provider   pinative.Provider
	providerAt time.Time
}

type piAgentHost struct{ agentWorkspace }

var _ piagent.Host = piAgentHost{}

func (h piAgentHost) Snapshot() piagent.State {
	c := h.Controller
	return piagent.State{Binary: c.Pi.Binary, Error: c.Pi.Error, Command: c.Pi.Command, Args: c.Pi.Args, Env: c.Pi.Env, Model: c.Pi.Model, Effort: c.Pi.Effort, Provider: c.piProvider(), ClaudeModel: textFirst(c.ClaudeModel, c.claudeDefaultModel)}
}
func (h piAgentHost) Run(id int) *pinative.Client { return h.Pi.Runs[id] }
func (h piAgentHost) SetRun(id int, run *pinative.Client) {
	if run == nil {
		delete(h.Pi.Runs, id)
		return
	}
	if h.Pi.Runs == nil {
		h.Pi.Runs = map[int]*pinative.Client{}
	}
	h.Pi.Runs[id] = run
}
func (h piAgentHost) SetCommand(v string) { h.SetPiCommand(v) }
func (h piAgentHost) SetModel(v string)   { h.setPiChoice("piModel", &h.Pi.Model, v) }
func (h piAgentHost) SetEffort(v string)  { h.setPiChoice("piEffort", &h.Pi.Effort, v) }
func (h piAgentHost) ReserveExecutionEpoch(id, epoch string) string {
	return claudeAgentHost(h).ReserveExecutionEpoch(id, epoch)
}
func (h piAgentHost) NoteRuntime(t model.Thread) { h.noteRuntime(t) }

// Pi conversations report delegated work as Claude Code's do: by the
// conversation's turns.
func (h piAgentHost) ManagedStarted(id int) { h.managedClaudeStarted(id) }
func (h piAgentHost) ManagedResult(id int, state dg.State, turn string) {
	h.managedClaudeResult(id, state, turn)
}
func (h piAgentHost) RemoveThread(id int, session string) { h.removeThreadLocal(id, session) }

// piProvider is the Claude Code provider pi reuses. The settings files are
// read again after a few seconds, so edits apply without a restart.
func (c *Controller) piProvider() pinative.Provider {
	if time.Since(c.Pi.providerAt) > 5*time.Second {
		c.Pi.provider = pinative.ProviderFromClaude(c.ClaudeArgs, c.ClaudeEnv)
		c.Pi.providerAt = time.Now()
	}
	return c.Pi.provider
}

// applyPiCommand resolves pi's command for the processes started after it.
func (c *Controller) applyPiCommand(line string) {
	c.agentUpdatesAt = time.Time{}
	c.Pi.Command = strings.TrimSpace(line)
	cmd, err := claude.ParseCommandFor(c.Pi.Command, "pi")
	c.Pi.Binary, c.Pi.Args, c.Pi.Env, c.Pi.Error = cmd.Path, cmd.Args, cmd.Env, ""
	if err != nil {
		c.Pi.Binary, c.Pi.Error = "", err.Error()
	}
	c.forgetInstalls()
	uiruntime.Invalidate()
}

func (c *Controller) SetPiCommand(line string) {
	if c.Remote != nil {
		c.forward("piCommand", line)
		return
	}
	c.applyPiCommand(line)
	prefs.Save("piCommand", c.Pi.Command)
}

func (c *Controller) setPiChoice(key string, field *string, v string) {
	*field = v
	if c.Remote != nil {
		c.forward(key, v)
	} else {
		prefs.Save(key, v)
	}
	uiruntime.Invalidate()
}

func (c *Controller) loadPiSettings() {
	c.applyPiCommand(prefs.Load("piCommand"))
	c.Pi.Model, c.Pi.Effort = prefs.Load("piModel"), prefs.Load("piEffort")
}

func textFirst(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
