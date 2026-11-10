package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"miya/internal/agent"
	claudeagent "miya/internal/agent/claude"
	claude "miya/internal/agent/claude/native"
	codexagent "miya/internal/agent/codex"
	codexnative "miya/internal/agent/codex/native"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/interaction"
)

type agentWorkspace struct{ *Controller }

func (h agentWorkspace) Context() context.Context         { return h.parentContext() }
func (h agentWorkspace) Cwd() string                      { return h.Controller.Cwd }
func (h agentWorkspace) Threads() []model.Thread          { return h.Controller.Threads() }
func (h agentWorkspace) SetThreads(v []model.Thread)      { h.Controller.SetThreads(v) }
func (h agentWorkspace) SetStatus(v string)               { h.Controller.SetStatus(v) }
func (h agentWorkspace) RemoveRequest(id json.RawMessage) { h.removeRequest(id) }
func (h agentWorkspace) FinishRewind(id, message int, text string, err error, apply func(*model.Thread)) {
	h.finishRewind(id, message, text, err, apply)
}
func (h agentWorkspace) PermissionForThread(t model.Thread) string { return h.permissionForThread(t) }

var _ agent.Workspace = agentWorkspace{}

type codexAgentHost struct{ agentWorkspace }

func (h codexAgentHost) Snapshot() codexagent.State {
	c := h.Controller
	s := codexagent.State{Client: c.Client}
	if c.Ready != nil {
		s.Ready = c.Ready()
	}
	if c.Status != nil {
		s.Status = c.Status()
	}
	if c.Model != nil {
		s.Model = c.Model()
	}
	if c.Effort != nil {
		s.Effort = c.Effort()
	}
	if c.Models != nil {
		s.ModelsKnown = true
		s.Models = c.Models()
	}
	return s
}
func (h codexAgentHost) SelectEffort(v string)                         { h.setCodexEffort(v) }
func (h codexAgentHost) GoalCommand(r model.SendRequest) (int, string) { return h.CodexGoalCommand(r) }
func (h codexAgentHost) SetFastMode(v bool)                            { h.fastMode = v }

var _ codexagent.Host = codexAgentHost{}

type claudeAgentHost struct{ agentWorkspace }

func (h claudeAgentHost) Snapshot() claudeagent.State {
	c := h.Controller
	return claudeagent.State{Binary: c.ClaudeBinary, Error: c.ClaudeError, Command: c.ClaudeCommand, Model: c.ClaudeModel, Effort: c.ClaudeEffort, DefaultModel: c.claudeDefaultModel, Models: c.claudeModels, Args: c.ClaudeArgs, Env: c.ClaudeEnv}
}
func (h claudeAgentHost) Run(id int) *claude.Client           { return h.ClaudeRuns[id] }
func (h claudeAgentHost) Discover()                           { h.loadClaudeSessions() }
func (h claudeAgentHost) RemoveThread(id int, session string) { h.removeThreadLocal(id, session) }
func (h claudeAgentHost) SetCommand(v string)                 { h.SetClaudeCommand(v) }
func (h claudeAgentHost) SetModel(v string)                   { h.setClaudeModel(v) }
func (h claudeAgentHost) SetEffort(v string)                  { h.setClaudeEffort(v) }
func (h claudeAgentHost) Commands(project string, load bool) ([]model.ComposerChoice, bool) {
	if load {
		return h.ClaudeCommandsFor(project)
	}
	list, known := h.ClaudeCmdCache[project]
	return list.Choice, known
}

var _ claudeagent.Host = claudeAgentHost{}

func (h agentWorkspace) Enqueue(id int, r model.SendRequest) int { return h.enqueue(id, r) }
func (h agentWorkspace) DrainQueue(id int)                       { h.drainQueue(id) }
func (h agentWorkspace) Upsert(t *model.Thread, m model.Message) { h.upsert(t, m) }
func (h agentWorkspace) Approvals() []interaction.Request        { return h.Controller.Approvals() }
func (h agentWorkspace) SetApprovals(v []interaction.Request)    { h.Controller.SetApprovals(v) }
func (h agentWorkspace) BindExecution(ctx context.Context, id, target, epoch string) error {
	return h.persistManagedExecutionBinding(ctx, id, target, epoch)
}
func (h agentWorkspace) Cancelled(err error) bool { return errors.Is(err, errManagedCancelled) }
func (h agentWorkspace) FinishPausedDelivery(id string, err error) bool {
	if !errors.Is(err, errManagedPaused) || !strings.HasPrefix(id, "delivery-") {
		return false
	}
	s := h.Collaboration
	s.Post(func() {
		if e := s.Manager.DeliveryOutcome(id, "", "not_started", err.Error()); e == nil {
			s.Release(id)
		}
	})
	return true
}
func (h claudeAgentHost) SetRun(id int, run *claude.Client) {
	if run == nil {
		delete(h.ClaudeRuns, id)
		return
	}
	if h.ClaudeRuns == nil {
		h.ClaudeRuns = map[int]*claude.Client{}
	}
	h.ClaudeRuns[id] = run
}
func (h claudeAgentHost) HistoryLoading(done chan struct{}) { h.ClaudeHistoryDone = done }
func (h claudeAgentHost) ReserveExecutionEpoch(id, epoch string) string {
	if execution := h.Collaboration.Execution(id); execution != nil {
		execution.Epoch = epoch
		return epoch
	}
	return ""
}
func (h claudeAgentHost) NoteRuntime(t model.Thread) { h.noteRuntime(t) }
func (h claudeAgentHost) ManagedStarted(id int)      { h.managedClaudeStarted(id) }
func (h claudeAgentHost) ManagedResult(id int, state dg.State, turn string) {
	h.managedClaudeResult(id, state, turn)
}
func (h claudeAgentHost) NoteModel(model string) { h.NoteClaudeModel(model) }
func (h claudeAgentHost) RefreshModels()         { h.RefreshClaudeModels() }

func (c *Controller) codexAdapter() *codexagent.Adapter {
	return codexagent.New(codexAgentHost{agentWorkspace{c}})
}
func (h codexAgentHost) RemoveThread(id int, session string) { h.removeThreadLocal(id, session) }
func (h codexAgentHost) FindThread(session string) model.Thread {
	for _, t := range h.Controller.Threads() {
		if t.ServerID != "" && t.ServerID == session {
			return t
		}
	}
	for path, threads := range h.ProjectCache {
		if path == h.Controller.Cwd {
			continue
		}
		for _, t := range threads {
			if t.ServerID != "" && t.ServerID == session && model.ThreadAgent(t) == model.AgentCodex {
				return t
			}
		}
	}
	return model.Thread{}
}
func (h codexAgentHost) NoteRuntime(t model.Thread)          { h.noteRuntime(t) }
func (h codexAgentHost) AccountUpdated(f codexnative.Frame)  { h.quotaFrame(f); h.RefreshAccount() }
func (h codexAgentHost) BeforeInputFailed(id, reason string) { h.managedBeforeInputFailed(id, reason) }
func (h codexAgentHost) RefreshGoal(id int)                  { h.refreshCodexGoal(id) }
