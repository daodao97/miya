package main

import (
	"errors"
	"strings"
	"time"

	"miya/internal/codex"
)

// claudeBackend runs the Claude Code CLI over stream-json, one process per
// turn, and reads its native sessions.
type claudeBackend struct{}

func (claudeBackend) ID() string          { return agentClaude }
func (claudeBackend) Name() string        { return "Claude Code" }
func (claudeBackend) Icon() agentIconSpec { return agentIconSpec{SVG: "claude-code", Color: "#c46b4d"} }
func (claudeBackend) Caps() agentCaps     { return agentCaps{Models: true, Effort: true, StallHint: true} }

func (claudeBackend) Discover(c *chatController)                  { c.loadClaudeSessions() }
func (claudeBackend) Ready(c *chatController) bool                { return c.claudeBinary != "" }
func (claudeBackend) Problem(c *chatController) (string, bool)    { return c.claudeError, false }
func (claudeBackend) Reconnect(*chatController)                   {}
func (claudeBackend) Open(c *chatController, t Thread)            { c.loadClaudeHistory(t) }
func (claudeBackend) Send(c *chatController, req sendRequest) int { return c.sendClaude(req) }

func (claudeBackend) Stop(c *chatController, t Thread) {
	if run := c.claudeRuns[t.ID]; run != nil {
		go run.Stop()
	}
}

// Archive and delete forget the app's entry; Claude Code keeps its session.
func (b claudeBackend) Archive(c *chatController, t Thread) {
	b.Stop(c, t)
	c.removeThreadLocal(t.ID, t.ServerID)
}

func (b claudeBackend) Delete(c *chatController, t Thread) { b.Archive(c, t) }

func (b claudeBackend) Suspend(c *chatController, t Thread) {
	b.Stop(c, t)
	t.Busy = false
	saveClaudeHistory(t)
}

func (claudeBackend) Owns(a codex.Approval) bool { return claudeApproval(a) }

func (claudeBackend) Respond(c *chatController, a codex.Approval, result any) {
	c.respondClaude(a, result)
}

func (claudeBackend) Settings() []agentSetting {
	return []agentSetting{{
		ID:          "claude-command",
		Title:       "Claude Code 启动命令",
		Help:        "留空使用 claude。可以带参数和环境变量，例如 claude --settings ~/settings.json。新的对话轮次生效。",
		Placeholder: "claude",
		Value:       func(c *chatController) string { return c.claudeCommand },
		Save:        func(c *chatController, v string) { c.setClaudeCommand(v) },
		Status: func(c *chatController) (string, bool) {
			if c.claudeError != "" {
				return c.claudeError, true
			}
			parts := append(append(append([]string(nil), c.claudeEnv...), c.claudeBinary), c.claudeArgs...)
			return "→ " + strings.Join(parts, " "), false
		},
	}}
}

// Claude Code rewinds by writing the session's history before the message
// as a new session, which the next turn resumes; the old one is left as it
// is and no longer listed.
func (claudeBackend) CanRewind(c *chatController, t Thread, m Message) bool {
	return t.ServerID != "" && c.claudeBinary != ""
}

func (claudeBackend) Rewind(c *chatController, t Thread, m Message, text string) {
	prompt := 0
	for _, x := range t.Messages {
		if x.ID == m.ID {
			break
		}
		if x.Role == "user" {
			prompt++
		}
	}
	cwd, old := c.cwd, t.ServerID
	goAsync(func() {
		var forked string
		var err error
		if prompt > 0 {
			if path := findClaudeSession(cwd, old); path == "" {
				err = errors.New("找不到 Claude Code 的对话记录")
			} else {
				forked, err = forkClaudeSessionBefore(path, prompt)
			}
		}
		if err == nil {
			markClaudeSuperseded(old)
		}
		c.dispatch(func() {
			c.finishRewind(t.ID, m.ID, text, err, func(t *Thread) {
				t.HistoryLoaded = true
				if forked == "" {
					// The first message: a new session.
					t.ServerID, t.Resumed = newClaudeSessionID(), false
				} else {
					t.ServerID, t.Resumed = forked, true
				}
			})
		})
	})
}

// Claude Code steers by a user message written to the running turn's
// input, which it folds into the turn.
func (claudeBackend) Steer(c *chatController, t Thread, req sendRequest) bool {
	run := c.claudeRuns[t.ID]
	if run == nil || !run.Steerable() {
		return false
	}
	shown, images := attachmentDisplay(req.Text, req.Attachments)
	now := time.Now().UnixMilli()
	pending := c.nextID()
	localTurn := c.beginMemoryTurn(t, req, pending)
	c.update(t.ID, func(t *Thread) {
		t.LastOutputAtMs, t.LastActivityAt = now, now
		t.Messages = append(t.Messages, Message{ID: pending, Role: "user", Content: shown, Images: images, Parts: req.Parts, SentAtMs: now, MemoryTurnID: localTurn, MemoryControl: req.MemoryControl || req.KeepSessionSettings})
	})
	project, session := c.threadWorkspace(t.ID), c.threadRef(t).URL()
	go func() {
		mem := c.prepareMemoryInput(t, req, project, session)
		content, err := claudeInput(mem.text(req.Text), req.Attachments)
		if err == nil {
			err = run.Steer(t.ServerID, content)
		}
		mem.finish(err == nil)
		if err != nil {
			c.dispatch(func() {
				c.update(t.ID, func(t *Thread) {
					for i, m := range t.Messages {
						if m.ID == pending {
							t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
							break
						}
					}
				})
				req.Steer = false
				if c.submit(req) == 0 {
					c.returnDraft(t.ID, req.Text, req.Attachments)
				}
			})
		}
	}()
	return true
}
