package main

import (
	"strings"

	"miya/internal/codex"
	"miya/internal/model"
)

// Every agent the app talks to is an agentBackend. The interface and the
// controller know conversations, messages and approvals only; how an agent
// starts, resumes, streams and stores them stays in its own adapter
// (agent_codex.go, agent_claude.go). Adding an agent is writing an adapter
// and listing it in agentBackends.

const (
	agentCodex  = model.AgentCodex
	agentClaude = model.AgentClaude
)

// agentCaps says which shared controls apply to an agent's conversations.
type agentCaps struct {
	Models         bool // the model picker
	Effort         bool // the reasoning effort picker
	Quota          bool // the account usage ring
	ThreadCommands bool // /compact and /review
	StallHint      bool // "no answer for a while" beside a run
}

// agentIconSpec is an agent's icon: an embedded SVG in a color, or a bitmap.
type agentIconSpec struct {
	SVG, Color string
	PNG        []byte
}

// agentSetting is a text setting an agent declares; the settings page shows
// it without knowing the agent.
type agentSetting struct {
	ID, Title, Help, Placeholder string
	Value                        func(c *chatController) string
	Save                         func(c *chatController, value string)
	// Status returns a line under the field, and whether it is an error.
	Status func(c *chatController) (string, bool)
}

type agentBackend interface {
	ID() string
	Runtime(c *chatController, t Thread) runtimeSource
	Name() string
	Icon() agentIconSpec
	Caps() agentCaps

	// Discover finds the workspace's conversations, as the app starts and
	// as the workspace changes.
	Discover(c *chatController)
	// Ready reports whether a message can be sent now.
	Ready(c *chatController) bool
	// Problem is shown above the composer, with a Reconnect button when
	// reconnect is true.
	Problem(c *chatController) (message string, reconnect bool)
	Reconnect(c *chatController)

	// Open loads a conversation's history as it is selected.
	Open(c *chatController, t Thread)
	// Send starts a turn of req, which submit checked, and returns the
	// conversation running it.
	Send(c *chatController, req SendRequest) int
	Stop(c *chatController, t Thread)
	Archive(c *chatController, t Thread)
	Delete(c *chatController, t Thread)
	// Suspend runs as the app leaves the conversation's workspace or quits.
	Suspend(c *chatController, t Thread)

	// Owns reports whether an approval came from this agent.
	Owns(a codex.Approval) bool
	Respond(c *chatController, a codex.Approval, result any)

	Settings() []agentSetting

	// ModelChoices and EffortChoices are what the pickers offer, "" the
	// agent's default; Model and Effort the current choices.
	ModelChoices(c *chatController) []string
	// ModelLabel is how the pickers show a model choice.
	ModelLabel(c *chatController, v string) string
	EffortChoices(c *chatController) []string
	Model(c *chatController) string
	Effort(c *chatController) string
	SetModel(c *chatController, v string)
	SetEffort(c *chatController, v string)

	// CanRewind reports whether a user message can be edited and sent
	// again; Rewind drops it and what followed from the agent's history,
	// then calls finishRewind.
	CanRewind(c *chatController, t Thread, m Message) bool
	Rewind(c *chatController, t Thread, m Message, text string)

	// Steer adds a message to the running turn of t; false when it cannot,
	// and the message waits in the queue instead. A steer the agent refuses
	// later goes to the queue too.
	Steer(c *chatController, t Thread, req SendRequest) bool
}

// agentBackends lists the agents in the order the picker shows them; the
// first is the default.
var agentBackends = []agentBackend{codexBackend{}, claudeBackend{}}

func backendFor(id string) agentBackend {
	for _, b := range agentBackends {
		if b.ID() == id {
			return b
		}
	}
	return agentBackends[0]
}

func backendOf(t Thread) agentBackend { return backendFor(model.ThreadAgent(t)) }

func approvalOwner(a codex.Approval) agentBackend {
	for _, b := range agentBackends {
		if b.Owns(a) {
			return b
		}
	}
	return agentBackends[0]
}

// currentAgent is the open conversation's agent, or the one chosen for a
// new conversation.
func (c *chatController) currentAgent() string {
	if t := c.current(); t.ID != 0 {
		return model.ThreadAgent(t)
	}
	if c.agent != nil {
		return backendFor(c.agent()).ID()
	}
	return agentBackends[0].ID()
}

func (c *chatController) currentBackend() agentBackend { return backendFor(c.currentAgent()) }

func (c *chatController) backendReady() bool { return c.currentBackend().Ready(c) }

func (c *chatController) discoverAll() {
	for _, b := range agentBackends {
		b.Discover(c)
	}
}

func claudeApproval(a codex.Approval) bool { return strings.Contains(string(a.ID), "claude:") }

func (c *chatController) canRewind(t Thread, m Message) bool {
	return m.Role == "user" && !t.Busy && !t.Loading && !t.ReadOnly && backendOf(t).CanRewind(c, t, m)
}

// rewind sends an edited user message of the open conversation in place of
// it and everything after it.
func (c *chatController) rewind(threadID, messageID int, text string) {
	if c.remote != nil {
		c.forward("rewind", svcRewind{Thread: threadID, Message: messageID, Text: text})
		return
	}
	text = strings.TrimSpace(text)
	t := c.threadWithID(threadID)
	for _, m := range t.Messages {
		if m.ID == messageID {
			if text == "" && len(messageAttachments(m)) == 0 {
				return
			}
			if c.canRewind(t, m) {
				c.update(t.ID, func(t *Thread) { t.Busy = true; t.Status = tr("Rewinding conversation…") })
				backendOf(t).Rewind(c, t, m, text)
			}
			return
		}
	}
}

// finishRewind ends an adapter's rewind: on success it drops the message and
// what followed, lets the adapter record its new history, and sends the text.
func (c *chatController) finishRewind(id, messageID int, text string, err error, apply func(*Thread)) {
	req := SendRequest{ThreadID: id, Text: text}
	c.update(id, func(t *Thread) {
		t.Busy = false
		if err != nil {
			t.Status = tr("Unable to resend: ") + err.Error()
			return
		}
		t.Status = ""
		for i, m := range t.Messages {
			if m.ID == messageID {
				req.Attachments = messageAttachments(m)
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
	if c.submit(req) == 0 {
		c.returnDraft(id, text, req.Attachments)
	}
}

func (c *chatController) threadWithID(id int) Thread {
	if id == 0 {
		return Thread{}
	}
	for _, t := range c.threads() {
		if t.ID == id {
			return t
		}
	}
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return t
			}
		}
	}
	return Thread{}
}

func (c *chatController) threadWorkspace(id int) string {
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return path
			}
		}
	}
	return c.cwd
}

// addThread adds a new conversation to a project's list: the open
// project's, or a background one's, whose runs go on unseen.
func (c *chatController) addThread(project string, t Thread) {
	if project == "" || project == c.cwd {
		c.setThreads(append([]Thread{t}, c.threads()...))
		return
	}
	if c.projectCache == nil {
		c.projectCache = map[string][]Thread{}
	}
	c.projectCache[project] = append([]Thread{t}, c.projectCache[project]...)
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}

// submit sends a message without reading any window's state. It returns
// the conversation that took it, or 0 when it cannot be sent now.
func (c *chatController) submit(req SendRequest) int {
	c.lastSubmitError = ""
	if model.HasAgentReferences(req.Text, req.Parts) {
		capability := c.collaborationCapabilities()
		if capability == nil || capability.Version < 1 {
			c.lastSubmitError = tr("Update the Miya background service to use Agent tags")
			if c.setStatus != nil {
				c.setStatus(c.lastSubmitError)
			}
			return 0
		}
	}
	if c.remote != nil {
		var id int
		if c.remoteHost() && len(req.Attachments) > 0 {
			paths, err := c.uploadRemoteAttachments(req.Attachments)
			if err != nil {
				c.lastSubmitError = err.Error()
				return 0
			}
			req.Attachments = paths
		}
		if err := c.remote.call("submit", req, &id); err != nil {
			c.lastSubmitError = err.Error()
			c.setStatus(err.Error())
		}
		return id
	}
	_, existingSnapshots := model.SplitReferenceContext(req.Text)
	if len(req.Parts) > 0 && len(existingSnapshots) == 0 {
		var err error
		req.Text, err = PartsText(req.Parts)
		if err != nil {
			c.lastSubmitError = err.Error()
			c.setStatus(err.Error())
			return 0
		}
	}
	if _, ok, _ := parseGoalCommand(req.Text); ok && (req.ThreadID == 0 && req.Agent != agentClaude || req.ThreadID != 0 && model.ThreadAgent(c.threadWithID(req.ThreadID)) == agentCodex) {
		id, message := c.codexGoalCommand(req)
		if message != "" {
			c.setStatus(message)
		}
		return id
	}
	// References supply a send-time context snapshot without changing the
	// conversations they refer to. Delegation records its own handoff.
	own, _ := model.SplitReferenceContext(req.Text)
	req.Parts = MessageParts(own)
	var err error
	req.Text, _, err = expandThreadRefs(req.Text, c.threadRef(c.threadWithID(req.ThreadID)), c.resolveReference)
	if err != nil {
		c.lastSubmitError = err.Error()
		c.setStatus(err.Error())
		return 0
	}
	return c.submitExpanded(req)
}

// submitExpanded starts or queues req, its references already expanded.
func (c *chatController) submitExpanded(req SendRequest) int {
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" && len(req.Attachments) == 0 {
		return 0
	}
	if req.Text == "" {
		req.Text = "Please inspect the attached file(s)."
	}
	t := c.threadWithID(req.ThreadID)
	if req.ThreadID != 0 && t.ID == 0 || t.ReadOnly || t.TaskPending {
		return 0
	}
	// A managed input is one independent execution, never an ordinary queued
	// message. In particular resume may reveal a still-running native turn.
	if req.ExecutionID != "" && (t.Busy || t.Loading || c.managedTargetUnavailable(t) || c.managedReservations[t.ID] != "" && c.managedReservations[t.ID] != req.ExecutionID) {
		return 0
	}
	if reservation := c.managedReservations[t.ID]; reservation != "" && reservation != req.ExecutionID {
		return c.enqueue(t.ID, req)
	}
	if t.Busy || t.Loading {
		if req.Steer && t.Busy && !t.Loading && backendOf(t).Steer(c, t, req) {
			return t.ID
		}
		return c.enqueue(t.ID, req)
	}
	b := backendOf(t)
	if t.ID == 0 {
		b = backendFor(req.Agent)
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
func (c *chatController) returnDraft(id int, text string, attachments []string) {
	if c.onDraftReturned != nil {
		c.onDraftReturned(id, text, attachments)
	}
}

// restoreDraft puts a returned message back in the window's composer when
// it shows that conversation and holds nothing newer.
func (c *chatController) restoreDraft(id int, text string, attachments []string) {
	if c.active() == id && strings.TrimSpace(c.draft()) == "" {
		if c.remoteHost() {
			attachments = append([]string(nil), attachments...)
			for i, path := range attachments {
				attachments[i] = "miya-server:" + path
			}
		}
		c.rememberDraft(text)
		c.setAttachments(attachments)
	}
}

func registeredBackend(id string) bool {
	for _, backend := range agentBackends {
		if backend.ID() == id {
			return true
		}
	}
	return false
}
