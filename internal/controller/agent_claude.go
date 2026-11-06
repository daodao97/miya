package controller

import (
	"errors"
	"strings"
	"time"

	"miya/internal/agentmeta"

	"miya/internal/agent"

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
type claudeBackend struct{ C *Controller }

func (b claudeBackend) ID() string { return appmodel.AgentClaude }

func (b claudeBackend) Name() string { return model.AgentName(model.AgentClaude) }

func (b claudeBackend) Icon() agentmeta.Icon         { return agentmeta.For(b.ID()).Icon }
func (b claudeBackend) Caps() agentmeta.Capabilities { return agentmeta.For(b.ID()).Capabilities }

func (b claudeBackend) Discover() { c := b.C; c.loadClaudeSessions() }

func (b claudeBackend) Ready() bool { c := b.C; return c.ClaudeBinary != "" }

func (b claudeBackend) Problem() (string, bool) { c := b.C; return c.ClaudeError, false }

func (b claudeBackend) Reconnect() {}

func (b claudeBackend) Open(t model.Thread) { c := b.C; c.LoadClaudeHistory(t) }

func (b claudeBackend) Send(req model.SendRequest) int { c := b.C; return c.sendClaude(req) }

func (b claudeBackend) Stop(t model.Thread) {
	c := b.C
	if run := c.ClaudeRuns[t.ID]; run != nil {
		go run.Stop()
	}
}

// Archive and delete forget the app's entry; Claude Code keeps its session.
func (b claudeBackend) Archive(t model.Thread) {
	c := b.C
	b.Stop(t)
	c.removeThreadLocal(t.ID, t.ServerID)
}

func (b claudeBackend) Delete(t model.Thread) { b.Archive(t) }

func (b claudeBackend) Suspend(t model.Thread) {
	b.Stop(t)
	t.Busy = false
	claude.SaveHistory(t)
}

func (b claudeBackend) Owns(a codex.Approval) bool { return claudeApproval(a) }

func (b claudeBackend) Respond(a codex.Approval, result any) {
	c := b.C
	c.RespondClaude(a, result)
}

func (b claudeBackend) Settings() []agent.Setting {
	c := b.C
	return []agent.Setting{{
		ID:          "claude-command",
		Title:       tr("Claude Code launch command"),
		Help:        tr("Leave blank to use claude. Arguments and environment variables are supported, for example: claude --settings ~/settings.json. Changes apply to new turns."),
		Placeholder: "claude",
		Value:       func() string { return c.ClaudeCommand },
		Save:        func(v string) { c.SetClaudeCommand(v) },
		Status: func() (string, bool) {
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
func (b claudeBackend) CanRewind(t model.Thread, m model.Message) bool {
	c := b.C
	return t.ServerID != "" && c.ClaudeBinary != ""
}

func (b claudeBackend) Rewind(t model.Thread, m model.Message, text string) {
	c := b.C
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
func (b claudeBackend) Steer(t model.Thread, req model.SendRequest) bool {
	c := b.C
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
