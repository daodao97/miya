package integration

import (
	"context"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/uiruntime"
)

// newViewerController holds a conversation for the message views without
// starting Codex or Claude Code.
func newViewerController() *controller.Controller {
	c := &controller.Controller{WindowState: appmodel.WindowState{ProjectCache: map[string][]model.Thread{}}}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})

	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	c.Draft, c.SetDraft = uiruntime.CreateSignal("")
	c.Attachments, c.SetAttachments = uiruntime.CreateSignal([]string{})
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})

	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.Agent, c.SetAgent = uiruntime.CreateSignal(appmodel.AgentCodex)
	// The shared views call these; left nil they would panic, as a Codex
	// message's rewind check once did with ready.
	c.Ready, c.SetReady = uiruntime.CreateSignal(false)
	c.Projects, c.SetProjects = uiruntime.CreateSignal([]model.Project{})
	c.Model, c.SetModel = uiruntime.CreateSignal("")
	c.Effort, c.SetEffort = uiruntime.CreateSignal("")
	c.Models, c.SetModels = uiruntime.CreateSignal([]codex.Model{})
	c.PermissionPolicy, c.SetPermissionPolicy = uiruntime.CreateSignal("")
	c.DraftPermission, c.SetDraftPermission = uiruntime.CreateSignal("")
	c.Locale, c.SetLocale = uiruntime.CreateSignal("en")
	c.HistoryStatus, c.SetHistoryStatus = uiruntime.CreateSignal("")
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	return c
}
