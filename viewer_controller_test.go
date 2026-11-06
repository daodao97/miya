package main

import (
	"context"
	"miya/internal/codex"
)

// newViewerController holds a conversation for the message views without
// starting Codex or Claude Code.
func newViewerController() *chatController {
	c := &chatController{projectCache: map[string][]Thread{}}
	c.threads, c.setThreads = createSignal([]Thread{})

	c.active, c.setActive = createSignal(0)
	c.revision, c.setRevision = createSignal(0)
	c.draft, c.setDraft = createSignal("")
	c.attachments, c.setAttachments = createSignal([]string{})
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})

	c.status, c.setStatus = createSignal("")
	c.agent, c.setAgent = createSignal(agentCodex)
	// The shared views call these; left nil they would panic, as a Codex
	// message's rewind check once did with ready.
	c.ready, c.setReady = createSignal(false)
	c.projects, c.setProjects = createSignal([]Project{})
	c.model, c.setModel = createSignal("")
	c.effort, c.setEffort = createSignal("")
	c.models, c.setModels = createSignal([]codex.Model{})
	c.permissionPolicy, c.setPermissionPolicy = createSignal("")
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.locale, c.setLocale = createSignal("en")
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}
