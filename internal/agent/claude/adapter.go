package claude

import (
	"errors"
	"strings"
	"time"

	"miya/internal/agent"
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	"miya/internal/agentmeta"
	"miya/internal/interaction"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/uiruntime"
)

func (b Adapter) ID() string { return agentmeta.Claude }

func (b Adapter) Name() string { return model.AgentName(model.AgentClaude) }

func (b Adapter) Icon() agentmeta.Icon         { return agentmeta.For(b.ID()).Icon }
func (b Adapter) Caps() agentmeta.Capabilities { return agentmeta.For(b.ID()).Capabilities }

func (b Adapter) Discover() { b.host.Discover() }

func (b Adapter) Ready() bool { return b.host.Snapshot().Binary != "" }

func (b Adapter) Problem() (string, bool) { return b.host.Snapshot().Error, false }

func (b Adapter) Reconnect() {}

func (b Adapter) Open(t model.Thread) { b.LoadHistory(t) }

func (b Adapter) Stop(t model.Thread) {
	c := b.host
	if run := c.Run(t.ID); run != nil {
		go run.Stop()
	}
}

// Archive and delete forget the app's entry; Claude Code keeps its session.
func (b Adapter) Archive(t model.Thread) {
	c := b.host
	b.Stop(t)
	c.RemoveThread(t.ID, t.ServerID)
}

func (b Adapter) Delete(t model.Thread) { b.Archive(t) }

func (b Adapter) Suspend(t model.Thread) {
	b.Stop(t)
	t.Busy = false
	claude.SaveHistory(t)
}

func (b Adapter) RespondNative(a interaction.Request, result any) { b.RespondLegacy(a, result) }

func (b Adapter) Settings() []agent.Setting {
	c := b.host
	return []agent.Setting{{
		ID:          "claude-command",
		Title:       tr("Claude Code launch command"),
		Help:        tr("Leave blank to use claude. Arguments and environment variables are supported, for example: claude --settings ~/settings.json. Changes apply to new turns."),
		Placeholder: "claude",
		Value:       func() string { return c.Snapshot().Command },
		Save:        func(v string) { c.SetCommand(v) },
		Status: func() (string, bool) {
			if c.Snapshot().Error != "" {
				return c.Snapshot().Error, true
			}
			parts := append(append(append([]string(nil), c.Snapshot().Env...), c.Snapshot().Binary), c.Snapshot().Args...)
			return "→ " + strings.Join(parts, " "), false
		},
	}}
}

// Claude Code rewinds by writing the session's history before the message
// as a new session, which the next turn resumes; the old one is left as it
// is and no longer listed.
func (b Adapter) CanRewind(t model.Thread, m model.Message) bool {
	c := b.host
	return t.ServerID != "" && c.Snapshot().Binary != ""
}

func (b Adapter) Rewind(t model.Thread, m model.Message, text string) {
	c := b.host
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
			c.FinishRewind(t.ID, m.ID, text, err, func(t *model.Thread) {
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
func (b Adapter) Steer(t model.Thread, req model.SendRequest) bool {
	c := b.host
	run := c.Run(t.ID)
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

func (b Adapter) Reply(a interaction.Request, reply interaction.Response) {
	b.host.Respond(a, encodeLegacyResponse(reply))
}

func (b Adapter) RunCommand(_ int, command, _ string) (int, string) {
	if command == "fast" || command == "fast on" || command == "fast off" {
		return 0, tr("/fast is only supported by Codex.")
	}
	return 0, b.Name() + tr(" does not support this command.")
}
