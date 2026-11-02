package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
)

func newClaudeSessionID() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func claudeHistoryPath(id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "quickgui-claude", id+".json")
}
func saveClaudeHistory(t Thread) {
	p := claudeHistoryPath(t.ServerID)
	if p == "" {
		return
	}
	data, err := json.Marshal(t)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".history-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(name, p)
	}
}
func (c *chatController) loadClaudeHistory(t Thread) {
	if t.HistoryLoaded || t.Loading {
		return
	}
	cwd := c.cwd
	c.update(t.ID, func(t *Thread) { t.Loading = true })
	done := make(chan struct{})
	c.claudeHistoryDone = done
	go func() {
		defer close(done)
		// Claude Code's own record is the truth, as the CLI may have continued
		// the session; the app's copy remains for sessions it cannot find.
		var stored Thread
		var err error
		if path := findClaudeSession(cwd, t.ServerID); path != "" {
			stored.Messages, err = parseClaudeSession(path)
			stored.Resumed = true
		}
		if len(stored.Messages) == 0 {
			var data []byte
			data, err = os.ReadFile(claudeHistoryPath(t.ServerID))
			if err == nil {
				err = json.Unmarshal(data, &stored)
			}
		}
		c.dispatch(func() {
			if c.cwd != cwd {
				return
			}
			c.update(t.ID, func(t *Thread) {
				t.Loading = false
				t.HistoryLoaded = true
				if err != nil {
					t.Status = "本地记录不可用。发送消息可继续此对话。"
					return
				}
				t.Messages = stored.Messages
				for i := range t.Messages {
					t.Messages[i].ID = c.nextID()
				}
				t.Resumed = stored.Resumed
				t.Loaded = true
			})
		})
	}()
}

func claudeInput(text string, paths []string) ([]map[string]any, error) {
	blocks := []map[string]any{{"type": "text", "text": text}}
	for _, p := range paths {
		typ := mime.TypeByExtension(filepath.Ext(p))
		if strings.HasPrefix(typ, "image/") {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if len(data) > 20*1024*1024 {
				return nil, fmt.Errorf("图片过大: %s", filepath.Base(p))
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": typ, "data": base64.StdEncoding.EncodeToString(data)}})
		} else {
			blocks = append(blocks, map[string]any{"type": "text", "text": "Attached file: " + p})
		}
	}
	return blocks, nil
}
func (c *chatController) sendClaude(req sendRequest) int {
	text, paths, policy := req.Text, req.Attachments, req.Policy
	t := c.threadWithID(req.ThreadID)
	cwd := c.cwd
	if t.ID != 0 {
		cwd = c.threadWorkspace(t.ID)
	} else {
		if req.Project != "" {
			cwd = req.Project
		}
		t = Thread{ID: c.nextID(), Agent: agentClaude, PermissionPolicy: policy, ServerID: newClaudeSessionID(), Title: conversationTitle("", claudePromptTitle(text), ""), Project: filepath.Base(cwd), Loaded: true, HistoryLoaded: true}
		c.addThread(cwd, t)
	}
	id := t.ID
	pendingUserID := c.nextID()
	now := time.Now().UnixMilli()
	c.update(id, func(t *Thread) {
		t.Busy = true
		t.StopRequested = false
		t.StartedAtMs = now
		t.LastOutputAtMs = now
		t.LastActivityAt = now
		t.Status = "Claude Code 正在运行…"
		shown, images := attachmentDisplay(text, paths)
		t.Messages = append(t.Messages, Message{ID: pendingUserID, Role: "user", Content: shown, Images: images, Parts: req.Parts, SentAtMs: now})
		name, _, _ := strings.Cut(text, " ")
		if name == "/loop" || name == "/proactive" {
			setThreadTask(t, ThreadTask{ID: "loop:pending", Kind: "loop", Title: strings.TrimSpace(strings.TrimPrefix(text, name)), Status: "starting"})
		}
	})
	if c.claudeRuns == nil {
		c.claudeRuns = map[int]*claude.Client{}
	}
	existing := c.claudeRuns[id]
	reject := func(err error) {
		c.dispatch(func() {
			if existing == nil {
				c.finishClaude(id, cwd, err)
				return
			}
			c.update(id, func(t *Thread) {
				t.Busy, t.TaskStopping = existing.Steerable(), false
				t.Status = err.Error()
				for i, m := range t.Messages {
					if m.ID == pendingUserID {
						t.Messages = append(t.Messages[:i:i], t.Messages[i+1:]...)
						break
					}
				}
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: err.Error()})
			})
			c.returnDraft(id, text, paths)
		})
	}
	go func() {
		content, err := claudeInput(text, paths)
		if err != nil {
			reject(err)
			return
		}
		if existing != nil {
			ctx, cancel := context.WithTimeout(c.parentContext(), 10*time.Second)
			defer cancel()
			if req.KeepSessionSettings {
				err = existing.Send(t.ServerID, content)
			} else {
				err = existing.SendWithOptions(ctx, claude.Options{SessionID: t.ServerID, Content: content, Model: req.Model, Effort: req.Effort, PermissionMode: claudePermissionMode(policy)})
			}
			if err != nil {
				reject(err)
			}
			return
		}
		run, err := claude.Start(c.ctx, claude.Options{Binary: c.claudeBinary, Args: append(append([]string(nil), c.claudeArgs...), claudeMCPArgs(cwd)...), Env: append(append([]string(nil), c.claudeEnv...), claudeMCPEnv()...), Cwd: cwd, SessionID: t.ServerID, Resume: t.Resumed, PermissionMode: claudePermissionMode(policy), Model: req.Model, Effort: req.Effort, Content: content}, func(f claude.Frame) {
			c.dispatch(func() {
				if c.threadWorkspace(id) == cwd {
					c.handleClaude(id, f)
				}
			})
		}, func(err error) { c.dispatch(func() { c.finishClaude(id, cwd, err) }) })
		c.dispatch(func() {
			if err != nil {
				c.finishClaude(id, cwd, err)
				return
			}
			if c.threadWorkspace(id) != cwd {
				go run.Stop()
				return
			}
			c.claudeRuns[id] = run
			for _, t := range c.threads() {
				if t.ID == id && t.StopRequested {
					go run.Stop()
				}
			}
		})
	}()
	return id
}

func (c *chatController) finishClaude(id int, cwd string, err error) {
	// The run's conversation may be in a background project.
	here := c.threadWorkspace(id) == cwd
	if here {
		sid := c.threadWithID(id).ServerID
		go func() {
			path := findClaudeSession(cwd, sid)
			if path == "" {
				return
			}
			session, ok := summarizeClaudeSession(path)
			if !ok {
				return
			}
			c.dispatch(func() {
				if c.threadWorkspace(id) != cwd {
					return
				}
				c.update(id, func(t *Thread) {
					if t.ServerID == sid {
						t.Title = session.Title
					}
				})
			})
		}()
	}
	delete(c.claudeRuns, id)
	if !here {
		return
	}
	c.update(id, func(t *Thread) {
		t.Busy = false
		t.TaskStopping = false
		t.CompletedAtMs = time.Now().UnixMilli()
		// A result frame can report an API error even when the CLI exits
		// successfully. Keep that turn's error after the process is reaped.
		resultError := ""
		for i := len(t.Messages) - 1; i >= 0; i-- {
			if t.Messages[i].Role == "user" {
				break
			}
			if t.Messages[i].Role == "error" {
				resultError = t.Messages[i].Content
				break
			}
		}
		state := "interrupted"
		if t.StopRequested {
			state = "stopped"
		} else if err != nil || resultError != "" {
			state = "failed"
		}
		stopClaudeTasks(t, state)
		t.Status = ""
		if t.StopRequested {
			t.Status = "Stopped"
		} else if err != nil {
			t.Status = err.Error()
			t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: err.Error(), StartedAtMs: t.CompletedAtMs})
		} else {
			t.Status = resultError
		}
		for i := range t.Messages {
			if t.Messages[i].Status == "inProgress" {
				t.Messages[i].Status = "completed"
				if t.StopRequested {
					t.Messages[i].Status = "interrupted"
				} else if err != nil || resultError != "" {
					t.Messages[i].Status = "failed"
				}
			}
		}
		saveClaudeHistory(*t)
	})
	pending := []codex.Approval{}
	prefix := fmt.Sprintf("\"claude:%d:", id)
	for _, a := range c.approvals() {
		if !strings.HasPrefix(string(a.ID), prefix) {
			pending = append(pending, a)
		}
	}
	c.setApprovals(pending)
}
func (c *chatController) handleClaude(id int, f claude.Frame) {
	if f.Type == "system" && f.Subtype == "init" {
		c.noteClaudeModel(f.Model)
	}
	if f.Type == "control_cancel_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		c.removeRequest(rid)
		return
	}

	if f.Type == "control_request" {
		rid, _ := json.Marshal(fmt.Sprintf("claude:%d:%s", id, f.RequestID))
		if f.Request.Subtype != "can_use_tool" {
			if run := c.claudeRuns[id]; run != nil {
				go run.Reply(f.RequestID, map[string]any{"behavior": "deny", "message": "Unsupported request"})
			}
			return
		}
		a := codex.Approval{ID: rid, Method: "claude/permission", Command: f.Request.ToolName, Reason: string(f.Request.Input), Payload: f.Request.Input}
		if f.Request.ToolName == "AskUserQuestion" {
			var input struct {
				Questions []struct {
					Question, Header string
					Options          []struct{ Label, Description string }
				}
			}
			_ = json.Unmarshal(f.Request.Input, &input)
			a.Method = "item/tool/requestUserInput"
			for _, q := range input.Questions {
				question := codex.Question{ID: q.Question, Header: q.Header, Question: q.Question}
				for _, o := range q.Options {
					question.Options = append(question.Options, struct {
						Label       string `json:"label"`
						Description string `json:"description"`
					}{Label: o.Label, Description: o.Description})
				}
				a.Questions = append(a.Questions, question)
			}
		}
		c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
		return
	}
	c.update(id, func(t *Thread) {
		now := time.Now().UnixMilli()
		t.LastOutputAtMs = now
		if f.SchedulesKnown {
			applyClaudeSchedules(t, f.Schedules)
		}
		if f.Type == "command_lifecycle" && f.CommandState == "started" {
			t.Busy, t.StopRequested = true, false
			t.StartedAtMs, t.CompletedAtMs = time.Now().UnixMilli(), 0
			t.Status = "Claude Code 正在运行…"
		}
		if f.SessionID != "" {
			t.ServerID = f.SessionID
			t.Resumed = true
		}
		if f.Type == "system" && strings.HasPrefix(f.Subtype, "task_") {
			claudeTaskFrame(t, f)
			return
		}
		// A sub-agent's own frames are steps of its Task call, not messages
		// of the conversation.
		if f.ParentToolUseID != "" {
			if f.Type == "assistant" {
				claudeSubAgentStep(t, f.ParentToolUseID, f.Message.Content)
			}
			return
		}
		switch f.Type {
		case "stream_event":
			var e struct {
				Type    string
				Index   int
				Message struct{ ID string }
				Delta   struct{ Type, Text string }
			}
			if json.Unmarshal(f.Event, &e) != nil {
				return
			}
			if e.Type == "message_start" {
				t.ClaudeMessageID = e.Message.ID
			}
			if e.Type == "content_block_delta" && e.Delta.Type == "text_delta" {
				key := fmt.Sprintf("%s:%d", t.ClaudeMessageID, e.Index)
				for i := range t.Messages {
					if t.Messages[i].ItemID == key {
						t.Messages[i].Content += e.Delta.Text
						return
					}
				}
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "assistant", Kind: "agentMessage", ItemID: key, Content: e.Delta.Text, SentAtMs: now, StartedAtMs: now})
			}
		case "assistant":
			for i, b := range f.Message.Content {
				if b.Type == "text" {
					key := claudeTextKey(t, f.Message.ID, i, b.Text)
					c.upsert(t, Message{Role: "assistant", Kind: "agentMessage", ItemID: key, Content: b.Text, SentAtMs: now, StartedAtMs: now})
				}
				if b.Type == "tool_use" {
					m := Message{Role: "tool", Kind: "claudeTool", ItemID: b.ID, Content: b.Name + "\n" + string(b.Input), Status: "inProgress", Agents: claudeSubAgent(b.ID, b.Name, b.Input, "inProgress")}
					keepSubAgents(t, &m)
					c.upsert(t, m)
				}
			}
		case "user":
			for _, b := range f.Message.Content {
				if b.Type == "tool_result" {
					for i := range t.Messages {
						if t.Messages[i].ItemID == b.ToolUseID {
							t.Messages[i].Status = "completed"
							if b.IsError {
								t.Messages[i].Status = "failed"
							}
							output := claudeVisibleText(claudeBlocks(b.Content))
							if shown := claudeToolOutput(t.Messages[i], output); shown != "" {
								t.Messages[i].Content += "\n" + bounded(shown)
							}
							claudeFinishSubAgents(&t.Messages[i], output)
						}
					}
				}
			}
		case "result":
			t.TaskStopping = false
			hadOutput := turnHasOutput(t.Messages)
			emptyResult := !f.IsError && !f.KeepingAlive && !t.StopRequested && !hadOutput && strings.TrimSpace(f.Result) == ""
			if f.IsError || emptyResult {
				stopClaudeTasks(t, "failed")
			}
			if f.KeepingAlive {
				t.Busy = false
				t.CompletedAtMs = time.Now().UnixMilli()
				t.Status = ""
				saveClaudeHistory(*t)
			}
			if f.IsError || emptyResult {
				message := strings.Join(f.Errors, "\n")
				if message == "" {
					message = f.Result
				}
				if message == "" {
					message = "Claude Code 执行失败"
				}
				if emptyResult {
					message = "Claude Code 已结束本轮，但没有返回回复。"
				}
				message = claudeErrorMessage(message, hadOutput, c.claudeCommand != "")
				t.Status = message
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "error", Content: message, StartedAtMs: now})
			} else if !hadOutput && strings.TrimSpace(f.Result) != "" {
				// Some CLI transports only provide the final result text.
				t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "assistant", Kind: "agentMessage", Content: f.Result, SentAtMs: now, StartedAtMs: now})
			}
		}
	})
}
func (c *chatController) respondClaude(a codex.Approval, result any) bool {
	var id string
	_ = json.Unmarshal(a.ID, &id)
	if !strings.HasPrefix(id, "claude:") {
		return false
	}
	var thread int
	_, _ = fmt.Sscanf(id, "claude:%d:", &thread)
	parts := strings.SplitN(id, ":", 3)
	c.removeRequest(a.ID)
	run := c.claudeRuns[thread]
	if run == nil || len(parts) != 3 {
		return true
	}
	encoded, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
		Answers  map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	_ = json.Unmarshal(encoded, &r)
	response := map[string]any{"behavior": "deny", "message": "用户拒绝此操作"}
	if r.Decision == "accept" || r.Answers != nil {
		var input map[string]any
		_ = json.Unmarshal(a.Payload, &input)
		if input == nil {
			input = map[string]any{}
		}
		if r.Answers != nil {
			answers := map[string]string{}
			for k, v := range r.Answers {
				answers[k] = strings.Join(v.Answers, ", ")
			}
			input["answers"] = answers
		}
		response = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	go func() { _ = run.Reply(parts[2], response) }()
	return true
}

// claudePermissionMode maps the app's permission choices to Claude Code's
// modes, so one picker serves both agents.
func claudePermissionMode(policy string) string {
	switch normalizePermissionPolicy(policy) {
	case "never":
		return "bypassPermissions"
	case "on-failure":
		return "auto"
	default:
		return "manual"
	}
}

// turnHasOutput reports whether the turn since the last prompt produced a
// reply or a tool call.
func turnHasOutput(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "user":
			return false
		case "assistant", "tool":
			return true
		}
	}
	return false
}

// claudeErrorMessage explains an empty result, which Claude Code reports as
// a diagnostic such as "[ede_diagnostic] … stop_reason=null" when the
// request got no answer, keeping its own words below.
func claudeErrorMessage(message string, hadOutput, customCommand bool) string {
	if hadOutput && !strings.Contains(message, "ede_diagnostic") {
		return message
	}
	hint := "Claude Code 没有返回回复。请检查网络和登录状态后重试。"
	if customCommand {
		hint = "Claude Code 没有返回回复。请在「设置 → 常规」中检查自定义启动命令的代理或 API 设置，或清空启动命令后重试。"
	}
	return hint + "\n\n原始信息：" + message
}

// claudeStallAfter is how long a run may send nothing before the app says
// it is still waiting.
const claudeStallAfter = 60 * time.Second

// stalledHint tells that Claude Code has sent nothing for a while. A tool
// that runs, or an approval the user has not answered, is not a stall.
func stalledHint(t Thread, waitingForUser bool, now time.Time) string {
	if !t.Busy || !backendOf(t).Caps().StallHint || t.LastOutputAtMs == 0 || waitingForUser {
		return ""
	}
	if n := len(t.Messages); n > 0 && t.Messages[n-1].Role == "tool" && t.Messages[n-1].Status == "inProgress" {
		return ""
	}
	quiet := now.Sub(time.UnixMilli(t.LastOutputAtMs))
	if quiet < claudeStallAfter {
		return ""
	}
	return fmt.Sprintf("已 %s 未收到回应。可能是网络或代理问题，可停止后重试", formatElapsed(quiet.Milliseconds()))
}

// claudeTextKey is the message a complete text block of a reply fills in.
// Claude Code sends each block of a reply in its own assistant frame, where
// it is at index 0, while streamed deltas carry the block's index in the
// whole reply; so the block completes the streamed message of its text, and
// an unstreamed block takes the first key no other text holds.
func claudeTextKey(t *Thread, messageID string, index int, text string) string {
	prefix := messageID + ":"
	partial := ""
	used := map[string]bool{}
	for _, m := range t.Messages {
		if m.Role != "assistant" || !strings.HasPrefix(m.ItemID, prefix) {
			continue
		}
		if m.Content == text {
			return m.ItemID
		}
		if partial == "" && m.Content != "" && strings.HasPrefix(text, m.Content) {
			partial = m.ItemID
		}
		used[m.ItemID] = true
	}
	if partial != "" {
		return partial
	}
	for k := index; ; k++ {
		if key := fmt.Sprintf("%s:%d", messageID, k); !used[key] {
			return key
		}
	}
}
