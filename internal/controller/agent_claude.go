package controller

import (
	"errors"
	"strings"
	"time"

	"miya/internal/appmodel"
	"miya/internal/claude"
	"miya/internal/claude/transcript"
	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/uiruntime"
)

// claudeBackend runs the Claude Code CLI over stream-json, one process per
// turn, and reads its native sessions.
type claudeBackend struct{}

func (claudeBackend) ID() string { return appmodel.AgentClaude }

func (claudeBackend) Name() string { return model.AgentName(model.AgentClaude) }

func (claudeBackend) Icon() appmodel.AgentIconSpec {
	return appmodel.AgentIconSpec{SVG: "claude-code", Color: "#c46b4d"}
}

func (claudeBackend) Caps() agentCaps { return agentCaps{Models: true, Effort: true, StallHint: true} }

func (claudeBackend) Discover(c *Controller) { c.loadClaudeSessions() }

func (claudeBackend) Ready(c *Controller) bool { return c.ClaudeBinary != "" }

func (claudeBackend) Problem(c *Controller) (string, bool) { return c.ClaudeError, false }

func (claudeBackend) Reconnect(*Controller) {}

func (claudeBackend) Open(c *Controller, t model.Thread) { c.LoadClaudeHistory(t) }

func (claudeBackend) Send(c *Controller, req model.SendRequest) int { return c.sendClaude(req) }

func (claudeBackend) Stop(c *Controller, t model.Thread) {
	if run := c.ClaudeRuns[t.ID]; run != nil {
		go run.Stop()
	}
}

// Archive and delete forget the app's entry; Claude Code keeps its session.
func (b claudeBackend) Archive(c *Controller, t model.Thread) {
	b.Stop(c, t)
	c.removeThreadLocal(t.ID, t.ServerID)
}

func (b claudeBackend) Delete(c *Controller, t model.Thread) { b.Archive(c, t) }

func (b claudeBackend) Suspend(c *Controller, t model.Thread) {
	b.Stop(c, t)
	t.Busy = false
	claude.SaveHistory(t)
}

func (claudeBackend) Owns(a codex.Approval) bool { return claudeApproval(a) }

func (claudeBackend) Respond(c *Controller, a codex.Approval, result any) {
	c.RespondClaude(a, result)
}

func (claudeBackend) Settings() []AgentSetting {
	return []AgentSetting{{
		ID:          "claude-command",
		Title:       tr("Claude Code launch command"),
		Help:        tr("Leave blank to use claude. Arguments and environment variables are supported, for example: claude --settings ~/settings.json. Changes apply to new turns."),
		Placeholder: "claude",
		Value:       func(c *Controller) string { return c.ClaudeCommand },
		Save:        func(c *Controller, v string) { c.SetClaudeCommand(v) },
		Status: func(c *Controller) (string, bool) {
			if c.ClaudeError != "" {
				return c.ClaudeError, true
			}
			parts := append(append(append([]string(nil), c.ClaudeEnv...), c.ClaudeBinary), c.ClaudeArgs...)
			return "→ " + strings.Join(parts, " "), false
		},
	}}
}

// Claude Code rewinds by writing the session's history before the message
// as a new session, which the next turn resumes; the old one is left as it
// is and no longer listed.
func (claudeBackend) CanRewind(c *Controller, t model.Thread, m model.Message) bool {
	return t.ServerID != "" && c.ClaudeBinary != ""
}

func (claudeBackend) Rewind(c *Controller, t model.Thread, m model.Message, text string) {
	prompt := 0
	for _, x := range t.Messages {
		if x.ID == m.ID {
			break
		}
		if x.Role == "user" {
			prompt++
		}
	}
	cwd, old := c.ThreadWorkspace(t.ID), t.ServerID
	uiruntime.GoAsync(func() {
		var forked string
		var err error
		if prompt > 0 {
			if path := transcript.Find(cwd, old); path == "" {
				err = errors.New(tr("Claude Code conversation history not found"))
			} else {
				forked, err = transcript.ForkBefore(path, prompt)
			}
		}
		if err == nil {
			transcript.MarkSuperseded(old)
		}
		c.Dispatch(func() {
			c.finishRewind(t.ID, m.ID, text, err, func(t *model.Thread) {
				t.HistoryLoaded = true
				if forked == "" {
					// The first message: a new session.
					t.ServerID, t.Resumed = claude.NewSessionID(), false
				} else {
					t.ServerID, t.Resumed = forked, true
				}
			})
		})
	})
}

// Claude Code steers by a user message written to the running turn's
// input, which it folds into the turn.
func (claudeBackend) Steer(c *Controller, t model.Thread, req model.SendRequest) bool {
	run := c.ClaudeRuns[t.ID]
	if run == nil || !run.Steerable() {
		return false
	}
	shown, images := msgfmt.AttachmentDisplay(req.Text, req.Attachments)
	now := time.Now().UnixMilli()
	pending := c.NextID()
	localTurn := c.Memories().BeginTurn(t, req, pending)
	c.Update(t.ID, func(t *model.Thread) {
		t.LastOutputAtMs, t.LastActivityAt = now, now
		t.Messages = append(t.Messages, model.Message{ID: pending, Role: "user", Content: shown, Images: images, Attachments: append([]string(nil), req.Attachments...), Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	project, session := c.ThreadWorkspace(t.ID), c.ThreadRef(t).URL()
	go func() {
		mem := c.Memories().PrepareInput(t, req, project, session)
		content, err := claude.Input(mem.Text(req.Text), req.Attachments)
		if err == nil {
			err = run.Steer(t.ServerID, content)
		}
		mem.Finish(err == nil)
		if err != nil {
			c.Dispatch(func() {
				c.Update(t.ID, func(t *model.Thread) {
					for i, m := range t.Messages {
						if m.ID == pending {
							t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
							break
						}
					}
				})
				req.Steer = false
				if c.Submit(req) == 0 {
					c.ReturnDraft(t.ID, req.Text, req.Attachments)
				}
			})
		}
	}()
	return true
}
