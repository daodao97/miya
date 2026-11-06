package service

import (
	"errors"
	"fmt"
	"strings"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/textutil"
)

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

const maxDelegationDepth = 2
