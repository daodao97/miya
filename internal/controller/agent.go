package controller

import (
	"strings"

	"miya/internal/appmodel"
	"miya/internal/codex"
	"miya/internal/composer"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/runtime/tracker"
	"miya/internal/svcproto"
)

// agentCaps says which shared controls apply to an agent's conversations.
type agentCaps = appmodel.AgentCaps

// agentSetting is a text setting an agent declares; the settings page shows
// it without knowing the agent.
type AgentSetting struct {
	ID, Title, Help, Placeholder string
	Value                        func(c *Controller) string
	Save                         func(c *Controller, value string)
	// Status returns a line under the field, and whether it is an error.
	Status func(c *Controller) (string, bool)
}

type agentBackend interface {
	ID() string
	Runtime(c *Controller, t model.Thread) tracker.SnapshotSource
	Name() string
	Icon() appmodel.AgentIconSpec
	Caps() agentCaps

	// Discover finds the workspace's conversations, as the app starts and
	// as the workspace changes.
	Discover(c *Controller)
	// Ready reports whether a message can be sent now.
	Ready(c *Controller) bool
	// Problem is shown above the composer, with a Reconnect button when
	// reconnect is true.
	Problem(c *Controller) (message string, reconnect bool)
	Reconnect(c *Controller)

	// Open loads a conversation's history as it is selected.
	Open(c *Controller, t model.Thread)
	// Send starts a turn of req, which submit checked, and returns the
	// conversation running it.
	Send(c *Controller, req model.SendRequest) int
	Stop(c *Controller, t model.Thread)
	Archive(c *Controller, t model.Thread)
	Delete(c *Controller, t model.Thread)
	// Suspend runs as the app leaves the conversation's workspace or quits.
	Suspend(c *Controller, t model.Thread)

	// Owns reports whether an approval came from this agent.
	Owns(a codex.Approval) bool
	Respond(c *Controller, a codex.Approval, result any)

	Settings() []AgentSetting

	// ModelChoices and EffortChoices are what the pickers offer, "" the
	// agent's default; Model and Effort the current choices.
	ModelChoices(c *Controller) []string
	// ModelLabel is how the pickers show a model choice.
	ModelLabel(c *Controller, v string) string
	EffortChoices(c *Controller) []string
	Model(c *Controller) string
	Effort(c *Controller) string
	SetModel(c *Controller, v string)
	SetEffort(c *Controller, v string)

	// CanRewind reports whether a user message can be edited and sent
	// again; Rewind drops it and what followed from the agent's history,
	// then calls finishRewind.
	CanRewind(c *Controller, t model.Thread, m model.Message) bool
	Rewind(c *Controller, t model.Thread, m model.Message, text string)

	// Steer adds a message to the running turn of t; false when it cannot,
	// and the message waits in the queue instead. A steer the agent refuses
	// later goes to the queue too.
	Steer(c *Controller, t model.Thread, req model.SendRequest) bool
}

// agentBackends lists the agents in the order the picker shows them; the
// first is the default.
var AgentBackends = []agentBackend{CodexBackend{}, claudeBackend{}}

func BackendFor(id string) agentBackend {
	for _, b := range AgentBackends {
		if b.ID() == id {
			return b
		}
	}
	return AgentBackends[0]
}

func BackendOf(t model.Thread) agentBackend { return BackendFor(model.ThreadAgent(t)) }

// agentStatus is an agent's state now, for features outside the controller.
func (c *Controller) agentStatus(id string) model.AgentStatus {
	b := BackendFor(id)
	return model.AgentStatus{ID: b.ID(), Name: b.Name(), Ready: b.Ready(c), Model: b.Model(c), Effort: b.Effort(c),
		ModelChoices: b.ModelChoices(c), EffortChoices: b.EffortChoices(c)}
}

// agentStatuses are every agent's states now, in agentBackends' order.
func (c *Controller) agentStatuses() []model.AgentStatus {
	var out []model.AgentStatus
	for _, b := range AgentBackends {
		out = append(out, c.agentStatus(b.ID()))
	}
	return out
}

// agentList is list_agents' result: each agent, whether it is ready, and its
// model and thinking choices.
func (c *Controller) agentList() map[string]any {
	var agents []map[string]any
	for _, st := range c.agentStatuses() {
		agents = append(agents, map[string]any{"id": st.ID, "name": st.Name, "ready": st.Ready, "models": st.ModelChoices, "efforts": st.EffortChoices})
	}
	return map[string]any{"agents": agents}
}

// stopAgentRun asks a conversation's agent to stop its turn.
func (c *Controller) stopAgentRun(t model.Thread) { BackendOf(t).Stop(c, t) }

func ApprovalOwner(a codex.Approval) agentBackend {
	for _, b := range AgentBackends {
		if b.Owns(a) {
			return b
		}
	}
	return AgentBackends[0]
}

// currentAgent is the open conversation's agent, or the one chosen for a
// new conversation.
func (c *Controller) CurrentAgent() string {
	if t := c.Current(); t.ID != 0 {
		return model.ThreadAgent(t)
	}
	if c.Agent != nil {
		return BackendFor(c.Agent()).ID()
	}
	return AgentBackends[0].ID()
}

func (c *Controller) CurrentBackend() agentBackend { return BackendFor(c.CurrentAgent()) }

func (c *Controller) BackendReady() bool { return c.CurrentBackend().Ready(c) }

func (c *Controller) discoverAll() {
	for _, b := range AgentBackends {
		b.Discover(c)
	}
}

func claudeApproval(a codex.Approval) bool { return strings.Contains(string(a.ID), "claude:") }

func (c *Controller) CanRewind(t model.Thread, m model.Message) bool {
	return m.Role == "user" && !t.Busy && !t.Loading && !t.ReadOnly && BackendOf(t).CanRewind(c, t, m)
}

// rewind sends an edited user message of the open conversation in place of
// it and everything after it.
func (c *Controller) Rewind(threadID, messageID int, text string) {
	if c.Remote != nil {
		c.forward("rewind", svcproto.Rewind{Thread: threadID, Message: messageID, Text: text})
		return
	}
	text = strings.TrimSpace(text)
	t := c.ThreadWithID(threadID)
	for _, m := range t.Messages {
		if m.ID == messageID {
			if text == "" && len(msgfmt.MessageAttachments(m)) == 0 {
				return
			}
			if c.CanRewind(t, m) {
				c.Update(t.ID, func(t *model.Thread) { t.Busy = true; t.Status = tr("Rewinding conversation…") })
				BackendOf(t).Rewind(c, t, m, text)
			}
			return
		}
	}
}

// finishRewind ends an adapter's rewind: on success it drops the message and
// what followed, lets the adapter record its new history, and sends the text.
func (c *Controller) finishRewind(id, messageID int, text string, err error, apply func(*model.Thread)) {
	req := model.SendRequest{ThreadID: id, Text: text}
	c.Update(id, func(t *model.Thread) {
		t.Busy = false
		if err != nil {
			t.Status = tr("Unable to resend: ") + err.Error()
			return
		}
		t.Status = ""
		for i, m := range t.Messages {
			if m.ID == messageID {
				req.Attachments = msgfmt.MessageAttachments(m)
				req.MemoryControl = m.MemoryControl
				t.Messages = t.Messages[:i]
				break
			}
		}
		if apply != nil {
			apply(t)
		}
	})
	if err != nil {
		return
	}
	if c.Submit(req) == 0 {
		c.ReturnDraft(id, text, req.Attachments)
	}
}

func (c *Controller) ThreadWithID(id int) model.Thread {
	if id == 0 {
		return model.Thread{}
	}
	for _, t := range c.Threads() {
		if t.ID == id {
			return t
		}
	}
	for path, threads := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return t
			}
		}
	}
	return model.Thread{}
}

func (c *Controller) ThreadWorkspace(id int) string {
	for path, threads := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return path
			}
		}
	}
	return c.Cwd
}

// addThread adds a new conversation to a project's list: the open
// project's, or a background one's, whose runs go on unseen.
func (c *Controller) AddThread(project string, t model.Thread) {
	if project == "" || project == c.Cwd {
		c.SetThreads(append([]model.Thread{t}, c.Threads()...))
		return
	}
	if c.ProjectCache == nil {
		c.ProjectCache = map[string][]model.Thread{}
	}
	c.ProjectCache[project] = append([]model.Thread{t}, c.ProjectCache[project]...)
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}

// submit sends a message without reading any window's state. It returns
// the conversation that took it, or 0 when it cannot be sent now.
func (c *Controller) Submit(req model.SendRequest) int {
	c.LastSubmitError = ""
	if model.HasAgentReferences(req.Text, req.Parts) {
		capability := c.CollaborationCapabilities()
		if capability == nil || capability.Version < 1 {
			c.LastSubmitError = tr("Update the Miya background service to use Agent tags")
			if c.SetStatus != nil {
				c.SetStatus(c.LastSubmitError)
			}
			return 0
		}
	}
	if c.Remote != nil {
		var id int
		if c.RemoteHost() && len(req.Attachments) > 0 {
			paths, err := c.UploadRemoteAttachments(req.Attachments)
			if err != nil {
				c.LastSubmitError = err.Error()
				return 0
			}
			req.Attachments = paths
		}
		if err := c.Remote.Call("submit", req, &id); err != nil {
			c.LastSubmitError = err.Error()
			c.SetStatus(err.Error())
		}
		return id
	}
	_, existingSnapshots := model.SplitReferenceContext(req.Text)
	if len(req.Parts) > 0 && len(existingSnapshots) == 0 {
		var err error
		req.Text, err = composer.PartsText(req.Parts)
		if err != nil {
			c.LastSubmitError = err.Error()
			c.SetStatus(err.Error())
			return 0
		}
	}
	if _, ok, _ := codex.ParseGoalCommand(req.Text); ok && (req.ThreadID == 0 && req.Agent != appmodel.AgentClaude || req.ThreadID != 0 && model.ThreadAgent(c.ThreadWithID(req.ThreadID)) == appmodel.AgentCodex) {
		id, message := c.CodexGoalCommand(req)
		if message != "" {
			c.SetStatus(message)
		}
		return id
	}
	// References supply a send-time context snapshot without changing the
	// conversations they refer to. Delegation records its own handoff.
	own, _ := model.SplitReferenceContext(req.Text)
	req.Parts = model.MessageParts(own)
	var err error
	req.Text, _, err = ExpandThreadRefs(req.Text, c.ThreadRef(c.ThreadWithID(req.ThreadID)), c.ResolveReference)
	if err != nil {
		c.LastSubmitError = err.Error()
		c.SetStatus(err.Error())
		return 0
	}
	return c.SubmitExpanded(req)
}

// submitExpanded starts or queues req, its references already expanded.
func (c *Controller) SubmitExpanded(req model.SendRequest) int {
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" && len(req.Attachments) == 0 {
		return 0
	}
	if req.Text == "" {
		req.Text = "Please inspect the attached file(s)."
	}
	t := c.ThreadWithID(req.ThreadID)
	if req.ThreadID != 0 && t.ID == 0 || t.ReadOnly || t.TaskPending {
		return 0
	}
	// A managed input is one independent execution, never an ordinary queued
	// message. In particular resume may reveal a still-running native turn.
	if req.ExecutionID != "" && (t.Busy || t.Loading || c.managedTargetUnavailable(t) || c.Collaboration.Reservation(t.ID) != "" && c.Collaboration.Reservation(t.ID) != req.ExecutionID) {
		return 0
	}
	if reservation := c.Collaboration.Reservation(t.ID); reservation != "" && reservation != req.ExecutionID {
		return c.enqueue(t.ID, req)
	}
	if t.Busy || t.Loading {
		if req.Steer && t.Busy && !t.Loading && BackendOf(t).Steer(c, t, req) {
			return t.ID
		}
		return c.enqueue(t.ID, req)
	}
	b := BackendOf(t)
	if t.ID == 0 {
		b = BackendFor(req.Agent)
	}
	if !b.Ready(c) {
		return 0
	}
	if req.Policy == "" {
		req.Policy = c.permissionForThread(t)
	}
	if req.Model == "" && !req.ExplicitSettings {
		req.Model = b.Model(c)
	}
	if req.Effort == "" && !req.ExplicitSettings {
		req.Effort = b.Effort(c)
	}
	return b.Send(c, req)
}

// returnDraft gives back a message that could not be sent.
func (c *Controller) ReturnDraft(id int, text string, attachments []string) {
	if c.OnDraftReturned != nil {
		c.OnDraftReturned(id, text, attachments)
	}
}

// restoreDraft puts a returned message back in the window's composer when
// it shows that conversation and holds nothing newer.
func (c *Controller) RestoreDraft(id int, text string, attachments []string) {
	if c.Active() == id && strings.TrimSpace(c.Draft()) == "" {
		if c.RemoteHost() {
			attachments = append([]string(nil), attachments...)
			for i, path := range attachments {
				attachments[i] = "miya-server:" + path
			}
		}
		c.RememberDraft(text)
		c.SetAttachments(attachments)
	}
}

func registeredBackend(id string) bool {
	for _, backend := range AgentBackends {
		if backend.ID() == id {
			return true
		}
	}
	return false
}
