package service

import (
	"errors"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/chat/subagent"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/textutil"
)

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
