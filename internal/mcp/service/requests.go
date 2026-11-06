package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"miya/internal/agent/presentation"

	"miya/internal/async"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/runtime/tracker"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

// mcpHandle answers a miya tool call the service received.

func (api *Service) Handle(op string, r mcpproto.Request) (any, error) {
	c := api.host
	if op == "mcpCollaboration" {
		return api.collaboration(r)
	}
	if c.Collaboration() != nil && (op == "mcpThreads" || op == "mcpRead" || op == "mcpStatus" || op == "mcpDelegate" || op == "mcpLegacyTask") {
		if len(r.Arguments) == 0 {
			return nil, errors.New("CALLER_UNAVAILABLE: Restart the agent to use verified conversation tools")
		}
		caller, _, err := api.CallerWithArguments(r.Tool, r.Arguments)
		if err != nil {
			return nil, err
		}
		if op == "mcpThreads" {
			return api.mcpThreadsFor(caller, c.ThreadWorkspace(caller.ID)), nil
		}
		if op == "mcpDelegate" {
			return api.LegacyDelegate(r, caller)
		}
		if op == "mcpLegacyTask" {
			return api.LegacyTaskRead(r, caller)
		}
	}
	if op == "mcpRuntime" {
		return api.Runtime(r)
	}
	if r.Reference != "" {
		ref, ok := model.ParseThreadURL(r.Reference)
		if !ok || ref.Source != c.ReferenceSource() {
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
			if known, _ := c.ReferenceState(ref); known && op == "mcpRead" {
				t, messages, err := c.ResolveReference(ref)
				if err != nil {
					if _, loading := c.ReferenceState(ref); loading {
						return mcpproto.ReadResult{}, nil
					}
					return nil, err
				}
				return mcpproto.ReadResult{Loaded: true, Packet: c.ThreadPacket(t, messages)}, nil
			}
			return nil, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	// The MCP server's workspace argument is not trusted: Codex runs one
	// app-server for every project, started wherever the service was. The
	// calling conversation's own project is used instead.
	switch op {
	case "mcpDesktopControl":
		return api.DesktopControl(r)
	case "mcpMemory":
		return api.Memory(r)
	case "mcpThreads":
		return api.Threads(r), nil
	case "mcpRead":
		return api.Read(r)
	case "mcpStatus":
		return api.Status(r)
	case "mcpDelegate":
		return api.Delegate(r)
	case "mcpReportResult":
		return api.reportResult(r)
	case "mcpAutomationList", "mcpAutomationCreate", "mcpAutomationUpdate":
		return api.automation(op, r)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

// allThreads are the conversations of every project, the open one first.

func (api *Service) Caller(tool string) (model.Thread, bool) {
	c := api.host
	var caller model.Thread
	var at int64
	found := false
	// The caller may be in a background project.
	for _, t := range c.AllThreads() {
		for i := len(t.Messages) - 1; i >= 0; i-- {
			m := t.Messages[i]
			if (m.Status == "inProgress" || m.Status == "in_progress") && isToolCall(m, tool) {
				if !found || m.StartedAtMs > at {
					caller, at, found = t, m.StartedAtMs, true
				}
				break
			}
		}
	}
	return caller, found
}

func (api *Service) Threads(r mcpproto.Request) mcpproto.ThreadsResult {
	c := api.host
	caller, ok := api.Caller(r.Tool)
	project := c.Cwd()
	if ok {
		project = c.ThreadWorkspace(caller.ID)
	}
	return api.mcpThreadsFor(caller, project)
}

func (api *Service) mcpThreadsFor(caller model.Thread, project string) mcpproto.ThreadsResult {
	c := api.host
	res := mcpproto.ThreadsResult{Threads: []mcpproto.ThreadInfo{}}
	for _, t := range c.ProjectThreads(project) {
		if t.ID == 0 || t.ID == caller.ID || !c.ThreadRef(t).Valid() {
			continue
		}
		status := "idle"
		if t.Busy || t.Loading {
			status = subagent.Running
		}
		res.Threads = append(res.Threads, mcpproto.ThreadInfo{Reference: c.ThreadRef(t).URL(), ID: t.ID, Title: c.ThreadTitle(t), Agent: model.ThreadAgent(t), Status: status})
	}
	return res
}

func (api *Service) Read(r mcpproto.Request) (mcpproto.ReadResult, error) {
	c := api.host
	t, messages, ok := c.ResolveThread(r.Thread)
	if !ok {
		return mcpproto.ReadResult{}, errors.New("Session not found. Use list_threads to see available session links.")
	}
	loaded := t.HistoryLoaded
	if !loaded {
		c.PrefetchThread(t.ID)
	}
	return mcpproto.ReadResult{Loaded: loaded, Packet: c.ThreadPacket(t, messages)}, nil
}

func (api *Service) Status(r mcpproto.Request) (mcpproto.StatusResult, error) {
	c := api.host
	t, _, ok := c.ResolveThread(r.Thread)
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
	return mcpproto.StatusResult{Reference: c.ThreadRef(t).URL(), ID: t.ID, Title: c.ThreadTitle(t), Agent: model.ThreadAgent(t), Status: status, Reply: textutil.ClipRunes(msgfmt.LastReply(t.Messages[last+1:]), 4000)}, nil
}

// mcpDelegate sends a task to another conversation, new or existing, with
// the caller's context as a reference, so the caller follows the work.

func (api *Service) Delegate(r mcpproto.Request) (mcpproto.DelegateResult, error) {
	c := api.host
	caller, ok := api.Caller(r.Tool)
	if !ok {
		return mcpproto.DelegateResult{}, errors.New(mcpproto.CallerUnknown)
	}
	task := strings.TrimSpace(r.Task)
	if task == "" {
		return mcpproto.DelegateResult{}, errors.New("task cannot be empty")
	}
	if api.delegationDepth(caller.ID) >= maxDelegationDepth {
		return mcpproto.DelegateResult{}, fmt.Errorf("Delegation is limited to %d levels. This conversation is itself a delegated task; complete it directly.", maxDelegationDepth)
	}
	if r.Thread != 0 {
		if r.Thread == caller.ID {
			return mcpproto.DelegateResult{}, errors.New("Cannot delegate a task to yourself")
		}
		if api.delegatedFrom(caller.ID, r.Thread) {
			return mcpproto.DelegateResult{}, errors.New("The target session delegated this task to you. You cannot delegate it back.")
		}
		if _, _, ok := c.ResolveThread(r.Thread); !ok {
			return mcpproto.DelegateResult{}, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	agent := model.ThreadAgent(caller)
	if r.Thread == 0 && r.Agent != "" {
		if agent = model.NormalizeAgentID(r.Agent); agent == "" {
			return mcpproto.DelegateResult{}, fmt.Errorf("Agent %q not found. Choose %s or %s.", r.Agent, model.AgentCodex, model.AgentClaude)
		}
	}
	text := task
	if !r.NoContext {
		ref := c.ThreadRef(caller)
		if !ref.Valid() {
			return mcpproto.DelegateResult{}, errors.New("The session must be created before its context can be handed off")
		}
		text += "\n\n" + model.ReferenceText(ref, c.ThreadTitle(caller))
	}
	id := c.Submit(model.SendRequest{ThreadID: r.Thread, Agent: agent, Project: c.ThreadWorkspace(caller.ID), Text: text, MemoryControl: true})
	if id == 0 {
		return mcpproto.DelegateResult{}, errors.New(textutil.FirstNonEmpty(c.LastSubmitError(), "Handoff failed: the target agent is not ready, or the conversation is read-only"))
	}
	c.NoteDelegation(caller, id, msgfmt.ThreadRefTask(task))
	return mcpproto.DelegateResult{Reference: c.ThreadRef(c.ThreadWithID(id)).URL(), Thread: id, Agent: model.ThreadAgent(c.ThreadWithID(id))}, nil
}

// delegationDepth is how many hand-offs led to a conversation.

func (api *Service) delegationDepth(id int) int {
	c := api.host
	depth := 0
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.ParentOf(id)
		if !ok || seen[parent] {
			return depth
		}
		seen[parent] = true
		id, depth = parent, depth+1
	}
}

// delegatedFrom reports whether ancestor handed work, directly or through
// others, to id.

func (api *Service) delegatedFrom(id, ancestor int) bool {
	c := api.host
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.ParentOf(id)
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

func (api *Service) Runtime(r mcpproto.Request) (any, error) {
	c := api.host
	var caller model.Thread
	count := 0
	for _, t := range c.AllThreads() {
		for _, message := range t.Messages {
			if (message.Status == "inProgress" || message.Status == "in_progress") && isToolCall(message, "runtime_info") {
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

func (api *Service) CallerWithArguments(tool string, raw json.RawMessage) (model.Thread, model.Message, error) {
	c := api.host
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
			if (m.Status != "inProgress" && m.Status != "in_progress") || !isToolCall(m, tool) {
				continue
			}
			var got map[string]any
			invocation, _ := presentation.ToolCall(m)
			_ = json.Unmarshal(invocation.Arguments, &got)
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

const maxDelegationDepth = 2

func isToolCall(message model.Message, name string) bool {
	call, ok := presentation.ToolCall(message)
	return ok && call.Server == "miya" && call.Name == name
}
