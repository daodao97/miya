package main

import (
	"strings"

	"miya/internal/codex"
)

// Every agent the app talks to is an agentBackend. The interface and the
// controller know conversations, messages and approvals only; how an agent
// starts, resumes, streams and stores them stays in its own adapter
// (agent_codex.go, agent_claude.go). Adding an agent is writing an adapter
// and listing it in agentBackends.

const (
	agentCodex  = "codex"
	agentClaude = "claude-code"
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
	Send(c *chatController, req sendRequest) int
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
	Steer(c *chatController, t Thread, req sendRequest) bool
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

// threadAgent is a conversation's agent; conversations from before agents
// were recorded are Codex's.
func threadAgent(t Thread) string {
	if t.Agent == "" {
		return agentCodex
	}
	return t.Agent
}

func backendOf(t Thread) agentBackend { return backendFor(threadAgent(t)) }

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
		return threadAgent(t)
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
	if text == "" {
		return
	}
	for _, m := range t.Messages {
		if m.ID == messageID {
			if c.canRewind(t, m) {
				c.update(t.ID, func(t *Thread) { t.Busy = true; t.Status = "正在回退对话…" })
				backendOf(t).Rewind(c, t, m, text)
			}
			return
		}
	}
}

// finishRewind ends an adapter's rewind: on success it drops the message and
// what followed, lets the adapter record its new history, and sends the text.
func (c *chatController) finishRewind(id, messageID int, text string, err error, apply func(*Thread)) {
	c.update(id, func(t *Thread) {
		t.Busy = false
		if err != nil {
			t.Status = "无法重新发送：" + err.Error()
			return
		}
		t.Status = ""
		for i, m := range t.Messages {
			if m.ID == messageID {
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
	if c.submit(sendRequest{ThreadID: id, Text: text}) == 0 {
		c.returnDraft(id, text, nil)
	}
}

// sendRequest is one message to send, with what the window chose for it.
type sendRequest struct {
	// ThreadID is the conversation, 0 to start one of Agent.
	ThreadID int
	Agent    string
	Text     string
	// Project is a new conversation's project, "" the open one.
	Project     string
	Attachments []string
	// Model, Effort and Policy default to the app's settings and the
	// conversation's permission choice.
	Model, Effort, Policy string
	ExplicitSettings      bool
	// Steer adds the message to a running turn instead of queueing it.
	Steer bool
	// KeepSessionSettings is used by native task controls, which must work
	// even after the user has changed the next message's launch settings.
	KeepSessionSettings bool
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
func (c *chatController) submit(req sendRequest) int {
	if c.remote != nil {
		var id int
		_ = c.remote.call("submit", req, &id)
		return id
	}
	if _, ok, _ := parseGoalCommand(req.Text); ok && (req.ThreadID == 0 && req.Agent != agentClaude || req.ThreadID != 0 && threadAgent(c.threadWithID(req.ThreadID)) == agentCodex) {
		id, message := c.codexGoalCommand(req)
		if message != "" {
			c.setStatus(message)
		}
		return id
	}
	// References to other conversations become their context here, where
	// every conversation's messages are; the referenced ones then follow.
	task := threadRefTask(req.Text)
	var sources []Thread
	req.Text, sources = expandThreadRefs(req.Text, req.ThreadID, c.resolveThreadRef)
	id := c.submitExpanded(req)
	if id != 0 && len(sources) > 0 {
		c.noteHandoffs(sources, id, task)
	}
	return id
}

// submitExpanded starts or queues req, its references already expanded.
func (c *chatController) submitExpanded(req sendRequest) int {
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
		c.rememberDraft(text)
		c.setAttachments(attachments)
	}
}
