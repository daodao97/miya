package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"miya/internal/agent/claude/native/transcript"
	"miya/internal/appmodel"
	"miya/internal/async"
	mcpconfig "miya/internal/mcp/config"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/runtime/tracker"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

// mcpHandle answers a miya tool call the service received.
func (c *Controller) McpHandle(op string, r mcpproto.Request) (any, error) {
	if op == "mcpCollaboration" {
		return c.collaborationMCP(r)
	}
	if c.Collaboration != nil && (op == "mcpThreads" || op == "mcpRead" || op == "mcpStatus" || op == "mcpDelegate" || op == "mcpLegacyTask") {
		if len(r.Arguments) == 0 {
			return nil, errors.New("CALLER_UNAVAILABLE: Restart the agent to use verified conversation tools")
		}
		caller, _, err := c.McpCallerWithArguments(r.Tool, r.Arguments)
		if err != nil {
			return nil, err
		}
		if op == "mcpThreads" {
			return c.mcpThreadsFor(caller, c.ThreadWorkspace(caller.ID)), nil
		}
		if op == "mcpDelegate" {
			return c.LegacyDelegate(r, caller)
		}
		if op == "mcpLegacyTask" {
			return c.LegacyTaskRead(r, caller)
		}
	}
	if op == "mcpRuntime" {
		return c.McpRuntime(r)
	}
	if r.Reference != "" {
		ref, ok := model.ParseThreadURL(r.Reference)
		if !ok || ref.Source != c.ReferenceSource {
			return nil, errors.New("Session link is invalid or belongs to another desktop")
		}
		r.Thread = 0
		for _, t := range c.AllThreads() {
			if c.ThreadRef(t) == ref {
				r.Thread = t.ID
				break
			}
		}
		if r.Thread == 0 {
			if _, known := c.ForkedReferences[ref]; known && op == "mcpRead" {
				t, messages, err := c.ResolveReference(ref)
				if err != nil {
					if c.referenceLoading[ref] {
						return mcpproto.ReadResult{}, nil
					}
					return nil, err
				}
				return mcpproto.ReadResult{Loaded: true, Packet: threadPacket(t, messages)}, nil
			}
			return nil, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	// The MCP server's workspace argument is not trusted: Codex runs one
	// app-server for every project, started wherever the service was. The
	// calling conversation's own project is used instead.
	switch op {
	case "mcpDesktopControl":
		return c.McpDesktopControl(r)
	case "mcpMemory":
		return c.McpMemory(r)
	case "mcpThreads":
		return c.McpThreads(r), nil
	case "mcpRead":
		return c.McpRead(r)
	case "mcpStatus":
		return c.McpStatus(r)
	case "mcpDelegate":
		return c.McpDelegate(r)
	case "mcpReportResult":
		return c.mcpReportResult(r)
	case "mcpAutomationList", "mcpAutomationCreate", "mcpAutomationUpdate":
		return c.mcpAutomation(op, r)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

// allThreads are the conversations of every project, the open one first.
func (c *Controller) AllThreads() []model.Thread {
	out := append([]model.Thread(nil), c.Threads()...)
	for path, threads := range c.ProjectCache {
		if path != c.Cwd {
			out = append(out, threads...)
		}
	}
	return out
}

// projectThreads are the conversations of one project.
func (c *Controller) projectThreads(project string) []model.Thread {
	if project == "" || project == c.Cwd {
		return c.Threads()
	}
	return c.ProjectCache[project]
}

// mcpCaller is the conversation making a miya tool call: the one whose
// call of it is in progress, whichever agent runs it.
func (c *Controller) McpCaller(tool string) (model.Thread, bool) {
	var caller model.Thread
	var at int64
	found := false
	// The caller may be in a background project.
	for _, t := range c.AllThreads() {
		for i := len(t.Messages) - 1; i >= 0; i-- {
			m := t.Messages[i]
			if (m.Status == "inProgress" || m.Status == "in_progress") && mcpconfig.IsMiyaToolCall(m, tool) {
				if !found || m.StartedAtMs > at {
					caller, at, found = t, m.StartedAtMs, true
				}
				break
			}
		}
	}
	return caller, found
}

func (c *Controller) McpThreads(r mcpproto.Request) mcpproto.ThreadsResult {
	caller, ok := c.McpCaller(r.Tool)
	project := c.Cwd
	if ok {
		project = c.ThreadWorkspace(caller.ID)
	}
	return c.mcpThreadsFor(caller, project)
}

func (c *Controller) mcpThreadsFor(caller model.Thread, project string) mcpproto.ThreadsResult {
	res := mcpproto.ThreadsResult{Threads: []mcpproto.ThreadInfo{}}
	for _, t := range c.projectThreads(project) {
		if t.ID == 0 || t.ID == caller.ID || !c.ThreadRef(t).Valid() {
			continue
		}
		status := "idle"
		if t.Busy || t.Loading {
			status = subagent.Running
		}
		res.Threads = append(res.Threads, mcpproto.ThreadInfo{Reference: c.ThreadRef(t).URL(), ID: t.ID, Title: appmodel.ThreadRefTitle(t), Agent: model.ThreadAgent(t), Status: status})
	}
	return res
}

func (c *Controller) McpRead(r mcpproto.Request) (mcpproto.ReadResult, error) {
	t, messages, ok := c.resolveThreadRef(r.Thread)
	if !ok {
		return mcpproto.ReadResult{}, errors.New("Session not found. Use list_threads to see available session links.")
	}
	loaded := t.HistoryLoaded
	if !loaded {
		c.prefetchThread(t.ID)
	}
	return mcpproto.ReadResult{Loaded: loaded, Packet: threadPacket(t, messages)}, nil
}

func (c *Controller) McpStatus(r mcpproto.Request) (mcpproto.StatusResult, error) {
	t, _, ok := c.resolveThreadRef(r.Thread)
	if !ok {
		return mcpproto.StatusResult{}, errors.New("The conversation no longer exists or is unavailable")
	}
	status, _ := c.HandoffState(t.ID)
	last := -1
	for i, m := range t.Messages {
		if m.Role == "user" {
			last = i
		}
	}
	return mcpproto.StatusResult{Reference: c.ThreadRef(t).URL(), ID: t.ID, Title: appmodel.ThreadRefTitle(t), Agent: model.ThreadAgent(t), Status: status, Reply: textutil.ClipRunes(msgfmt.LastReply(t.Messages[last+1:]), 4000)}, nil
}

// mcpDelegate sends a task to another conversation, new or existing, with
// the caller's context as a reference, so the caller follows the work.
func (c *Controller) McpDelegate(r mcpproto.Request) (mcpproto.DelegateResult, error) {
	caller, ok := c.McpCaller(r.Tool)
	if !ok {
		return mcpproto.DelegateResult{}, errors.New(mcpproto.CallerUnknown)
	}
	task := strings.TrimSpace(r.Task)
	if task == "" {
		return mcpproto.DelegateResult{}, errors.New("task cannot be empty")
	}
	if c.delegationDepth(caller.ID) >= mcpconfig.MaxDelegationDepth {
		return mcpproto.DelegateResult{}, fmt.Errorf("Delegation is limited to %d levels. This conversation is itself a delegated task; complete it directly.", mcpconfig.MaxDelegationDepth)
	}
	if r.Thread != 0 {
		if r.Thread == caller.ID {
			return mcpproto.DelegateResult{}, errors.New("Cannot delegate a task to yourself")
		}
		if c.delegatedFrom(caller.ID, r.Thread) {
			return mcpproto.DelegateResult{}, errors.New("The target session delegated this task to you. You cannot delegate it back.")
		}
		if _, _, ok := c.resolveThreadRef(r.Thread); !ok {
			return mcpproto.DelegateResult{}, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	agent := model.ThreadAgent(caller)
	if r.Thread == 0 && r.Agent != "" {
		if agent = model.NormalizeAgentID(r.Agent); agent == "" {
			return mcpproto.DelegateResult{}, fmt.Errorf("Agent %q not found. Choose %s or %s.", r.Agent, appmodel.AgentCodex, appmodel.AgentClaude)
		}
	}
	text := task
	if !r.NoContext {
		ref := c.ThreadRef(caller)
		if !ref.Valid() {
			return mcpproto.DelegateResult{}, errors.New("The session must be created before its context can be handed off")
		}
		text += "\n\n" + model.ReferenceText(ref, appmodel.ThreadRefTitle(caller))
	}
	id := c.Submit(model.SendRequest{ThreadID: r.Thread, Agent: agent, Project: c.ThreadWorkspace(caller.ID), Text: text, MemoryControl: true})
	if id == 0 {
		return mcpproto.DelegateResult{}, errors.New(textutil.FirstNonEmpty(c.LastSubmitError, "Handoff failed: the target agent is not ready, or the conversation is read-only"))
	}
	c.noteHandoffs([]model.Thread{caller}, id, msgfmt.ThreadRefTask(task))
	if c.DelegationParent == nil {
		c.DelegationParent = map[int]int{}
	}
	if _, ok := c.DelegationParent[id]; !ok && id != caller.ID {
		c.DelegationParent[id] = caller.ID
	}
	return mcpproto.DelegateResult{Reference: c.ThreadRef(c.ThreadWithID(id)).URL(), Thread: id, Agent: model.ThreadAgent(c.ThreadWithID(id))}, nil
}

// delegationDepth is how many hand-offs led to a conversation.
func (c *Controller) delegationDepth(id int) int {
	depth := 0
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.DelegationParent[id]
		if !ok || seen[parent] {
			return depth
		}
		seen[parent] = true
		id, depth = parent, depth+1
	}
}

// delegatedFrom reports whether ancestor handed work, directly or through
// others, to id.
func (c *Controller) delegatedFrom(id, ancestor int) bool {
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.DelegationParent[id]
		if !ok || seen[parent] {
			return false
		}
		if parent == ancestor {
			return true
		}
		seen[parent] = true
		id = parent
	}
}

func (c *Controller) McpRuntime(r mcpproto.Request) (any, error) {
	var caller model.Thread
	count := 0
	for _, t := range c.AllThreads() {
		for _, message := range t.Messages {
			if (message.Status == "inProgress" || message.Status == "in_progress") && mcpconfig.IsMiyaToolCall(message, "runtime_info") {
				caller = t
				count++
				break
			}
		}
	}
	if count == 0 {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	if count != 1 {
		return nil, errors.New("Multiple conversations are querying runtime information. Unable to identify the caller uniquely. Try again shortly.")
	}
	work, err := c.PrepareRuntime(c.ThreadRef(caller), c.ThreadWorkspace(caller.ID))
	if err != nil {
		return nil, err
	}
	return async.Reply(func() (any, error) {
		data, err := work()
		if err != nil {
			return nil, err
		}
		return data.(tracker.ConversationRuntime).View, nil
	}), nil
}

// Strict identity for every memory operation, including reads: the shared
// Codex app-server's cwd is not the caller's workspace. No time-based guess.
func (c *Controller) McpCallerWithArguments(tool string, raw json.RawMessage) (model.Thread, model.Message, error) {
	var want map[string]any
	if json.Unmarshal(raw, &want) != nil || want == nil {
		return model.Thread{}, model.Message{}, errors.New("Tool arguments must be an object")
	}
	type candidate struct {
		thread  model.Thread
		message model.Message
	}
	var matches []candidate
	for _, t := range c.AllThreads() {
		for _, m := range t.Messages {
			if (m.Status != "inProgress" && m.Status != "in_progress") || !mcpconfig.IsMiyaToolCall(m, tool) {
				continue
			}
			var got map[string]any
			if strings.EqualFold(m.Kind, "claudetool") {
				_, got = transcript.ToolCall(m)
			} else if m.Item != nil {
				_ = json.Unmarshal(m.Item.Arguments, &got)
			}
			if got != nil && reflect.DeepEqual(want, got) {
				matches = append(matches, candidate{t, m})
			}
		}
	}
	if len(matches) == 0 {
		return model.Thread{}, model.Message{}, errors.New(mcpproto.CallerUnknown)
	}
	if len(matches) != 1 {
		return model.Thread{}, model.Message{}, errors.New("The calling conversation is ambiguous. Stagger calls with identical arguments and retry.")
	}
	return matches[0].thread, matches[0].message, nil
}
