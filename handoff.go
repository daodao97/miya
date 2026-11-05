package main

import (
	"context"
	"encoding/json"
	"fmt"
	"miya/internal/textutil"
	"strconv"
	"strings"
)

// threadRefs are stable backend identities in the message's own words.
func threadRefs(text string) []ThreadRef {
	own, _ := splitReferenceContext(text)
	var refs []ThreadRef
	seen := map[ThreadRef]bool{}
	for _, mention := range threadMentions(own) {
		if !seen[mention.Ref] {
			seen[mention.Ref] = true
			refs = append(refs, mention.Ref)
		}
	}
	return refs
}

func threadRefTask(text string) string {
	own, _ := splitReferenceContext(text)
	var b strings.Builder
	end := 0
	for _, m := range threadMentions(own) {
		b.WriteString(own[end:m.Start])
		end = m.End
	}
	b.WriteString(own[end:])
	return textutil.ClipRunes(strings.Join(strings.Fields(textutil.FirstLine(strings.TrimSpace(b.String()))), " "), 80)
}

func threadRefTitle(t Thread) string {
	return textutil.FirstNonEmpty(strings.TrimSpace(t.Title), t.ServerID, tr("New conversation"))
}

// Keep the exact reference tokens. Titles never need to be reversed into IDs.
// An envelope is a send-time snapshot; replaying a queued request preserves it.
func expandThreadRefs(text string, self ThreadRef, resolve func(ThreadRef) (Thread, []Message, error)) (string, []Thread, error) {
	own, snapshots := splitReferenceContext(text)
	if len(snapshots) > 0 {
		return text, nil, nil
	}
	var sources []Thread
	titles := map[ThreadRef]string{}
	for _, m := range threadMentions(own) {
		if _, ok := titles[m.Ref]; !ok {
			titles[m.Ref] = m.Title
		}
	}
	for _, ref := range threadRefs(own) {
		if ref == self {
			return text, nil, fmt.Errorf(tr("Cannot reference the current session: %s"), titles[ref])
		}
		t, messages, err := resolve(ref)
		if err != nil {
			return text, nil, fmt.Errorf(tr("Failed to reference \"%s\": %w"), titles[ref], err)
		}
		if !t.HistoryLoaded {
			return text, nil, fmt.Errorf(tr("Context for \"%s\" has not loaded. Retry or remove the reference."), titles[ref])
		}
		snapshots = append(snapshots, referenceSnapshot{Ref: ref, Title: titles[ref], Context: threadPacket(t, messages)})
		sources = append(sources, t)
	}
	if len(snapshots) == 0 {
		return text, nil, nil
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return text, nil, err
	}
	return own + contextEnvelopeOpen + string(data) + contextEnvelopeClose, sources, nil
}

// threadPacket is a conversation's context as another agent reads it: its
// first and latest requests, its latest conclusions, the files it changed
// and its sub-agents, each bounded, without its tool output.
func threadPacket(t Thread, messages []Message) string {
	title := threadRefTitle(t)
	var b strings.Builder
	fmt.Fprintf(&b, tr("Session ID: %s\nBackend: %s\n"), t.ServerID, threadAgent(t))
	fmt.Fprintf(&b, tr("The following context from conversation \"%s\" (%s) was compiled by Miya from its history for reference.\n"), title, backendOf(t).Name())
	var requests []string
	var turns [][]Message
	for _, m := range messages {
		if m.Role == "user" {
			requests = append(requests, strings.TrimSpace(threadContextDisplay(m.Content)))
			turns = append(turns, nil)
		} else if len(turns) > 0 {
			turns[len(turns)-1] = append(turns[len(turns)-1], m)
		}
	}
	if len(messages) == 0 {
		if t.HistoryLoaded {
			b.WriteString(tr("\n(This conversation has no messages yet.)\n"))
		} else {
			b.WriteString(tr("\n(History for this conversation has not loaded yet.)\n"))
		}
	}
	if len(requests) > 0 {
		packetSection(&b, tr("Original request"), textutil.ClipRunes(requests[0], 800))
		if len(requests) > 1 {
			packetSection(&b, tr("Latest request"), textutil.ClipRunes(requests[len(requests)-1], 800))
		}
	}
	var conclusions []string
	for i := max(0, len(turns)-2); i < len(turns); i++ {
		if reply := lastReply(turns[i]); reply != "" {
			conclusions = append(conclusions, textutil.ClipRunes(reply, 1500))
		}
	}
	if len(conclusions) > 0 {
		packetSection(&b, tr("Latest conclusions"), strings.Join(conclusions, "\n\n---\n\n"))
	}
	if files := changedFiles(messages); len(files) > 0 {
		if len(files) > 30 {
			files = append(files[:30], fmt.Sprintf(tr("…and %d more files"), len(files)-30))
		}
		packetSection(&b, tr("Changed files"), "- "+strings.Join(files, "\n- "))
	}
	if agents := latestSubAgents(messages); len(agents) > 0 {
		var lines []string
		for _, a := range agents {
			line := textutil.FirstNonEmpty(a.Name, a.ID) + " · " + subAgentStatusLabel(a.Status)
			if a.Detail != "" {
				line += " · " + textutil.ClipRunes(a.Detail, 200)
			}
			lines = append(lines, line)
			if len(lines) == 10 {
				break
			}
		}
		packetSection(&b, tr("Subtasks"), "- "+strings.Join(lines, "\n- "))
	}
	return b.String()
}

func packetSection(b *strings.Builder, title, body string) {
	fmt.Fprintf(b, "\n## %s\n%s\n", title, body)
}

// lastReply is the last thing an agent said in a turn.
func lastReply(turn []Message) string {
	for i := len(turn) - 1; i >= 0; i-- {
		if turn[i].Role == "assistant" && strings.TrimSpace(turn[i].Content) != "" {
			return strings.TrimSpace(turn[i].Content)
		}
	}
	return ""
}

// changedFiles are the files a conversation's tools wrote, once each.
func changedFiles(messages []Message) []string {
	var files []string
	seen := map[string]bool{}
	add := func(path string) {
		if path = strings.TrimSpace(path); path != "" && !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	for _, m := range messages {
		switch strings.ToLower(m.Kind) {
		case "filechange":
			if m.Item != nil {
				for _, change := range m.Item.Changes {
					add(change.Path)
				}
			}
		case "claudetool":
			switch name, input := claudeToolCall(m); name {
			case "Edit", "MultiEdit", "Write", "NotebookEdit":
				add(inputString(input, "file_path", "notebook_path"))
			}
		}
	}
	return files
}

// threadContextDisplay shows the context packets of a message as the
// conversations they name, not the text the agent read.
func threadContextDisplay(s string) string {
	own, _ := splitReferenceContext(s)
	var b strings.Builder
	end := 0
	for _, m := range threadMentions(own) {
		b.WriteString(own[end:m.Start])
		b.WriteString(tr("[Referenced conversation: ") + m.Title + "]")
		end = m.End
	}
	b.WriteString(own[end:])
	return b.String()
}

// handoff links a delegating conversation to the one receiving its task;
// Item is the activity that follows the work in the delegating one.
type handoff struct {
	From, To int
	Item     string
}

// threadRef returns no public identity until the backend session exists.
func (c *chatController) threadRef(t Thread) ThreadRef {
	return ThreadRef{Source: c.referenceSource, Backend: threadAgent(t), SessionID: t.ServerID}
}

// Local numeric lookup is retained only for internal controls and MCP routing.
func (c *chatController) resolveThreadRef(id int) (Thread, []Message, bool) {
	t, _, ok := c.findThread(id)
	if !ok {
		return Thread{}, nil, false
	}
	messages := t.Messages
	if !t.HistoryLoaded {
		if cached, ok := c.threadHistory[c.threadRef(t).URL()]; ok {
			messages = cached
			t.HistoryLoaded = true
		}
	}
	return t, messages, true
}

func (c *chatController) resolveReference(ref ThreadRef) (Thread, []Message, error) {
	if !ref.valid() {
		return Thread{}, nil, fmt.Errorf("%s", tr("Invalid session identity"))
	}
	if ref.Source != c.referenceSource {
		return Thread{}, nil, fmt.Errorf("%s", tr("This reference belongs to another desktop. Connect to its source and try again."))
	}
	for _, project := range c.threadRefProjects() {
		for _, t := range project.Threads {
			if threadAgent(t) != ref.Backend || t.ServerID != ref.SessionID {
				continue
			}
			if t.HistoryLoaded {
				return t, t.Messages, nil
			}
			if messages, ok := c.threadHistory[ref.URL()]; ok {
				t.HistoryLoaded = true
				return t, messages, nil
			}
			c.prefetchReference(ref)
			return Thread{}, nil, fmt.Errorf("%s", tr("Context is loading. Try again; if loading fails, select the reference again or remove it."))
		}
	}
	if session, ok := c.forkedReferences[ref]; ok {
		t := Thread{ServerID: ref.SessionID, Agent: ref.Backend, Title: session.Title}
		if messages, loaded := c.threadHistory[ref.URL()]; loaded {
			t.HistoryLoaded = true
			return t, messages, nil
		}
		c.prefetchReference(ref)
		return Thread{}, nil, fmt.Errorf("%s", tr("The source conversation is loading. Try again."))
	}
	return Thread{}, nil, fmt.Errorf("%s", tr("The conversation no longer exists or is unavailable"))
}

// findThread is a conversation of the open project or of one in the
// background, and its project's path.
func (c *chatController) findThread(id int) (Thread, string, bool) {
	for _, t := range c.threads() {
		if t.ID == id {
			return t, c.cwd, true
		}
	}
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return t, path, true
			}
		}
	}
	return Thread{}, "", false
}

func (c *chatController) prefetchThread(id int) {
	if t, _, ok := c.findThread(id); ok {
		c.prefetchReference(c.threadRef(t))
	}
}

// Cache by backend identity. A fork or reused UI number cannot inherit history.
func (c *chatController) prefetchReference(ref ThreadRef) {
	if !ref.valid() {
		return
	}
	if c.remote != nil {
		c.forward("prefetch", ref)
		return
	}
	if ref.Source != c.referenceSource {
		return
	}
	if _, ok := c.threadHistory[ref.URL()]; ok {
		return
	}
	if c.referenceLoading == nil {
		c.referenceLoading = map[ThreadRef]bool{}
	}
	if c.referenceLoading[ref] {
		return
	}
	var pt phoneThread
	for _, project := range c.threadRefProjects() {
		for _, t := range project.Threads {
			if threadAgent(t) == ref.Backend && t.ServerID == ref.SessionID {
				if t.HistoryLoaded {
					return
				}
				pt = phoneThread{thread: t, found: true, cwd: project.Path, client: c.client}
			}
		}
	}
	if !pt.found {
		if session, ok := c.forkedReferences[ref]; ok {
			pt = phoneThread{thread: Thread{ServerID: ref.SessionID, Agent: ref.Backend, Title: session.Title}, found: true, cwd: session.Project, client: c.client}
		}
	}
	if !pt.found {
		return
	}
	c.referenceLoading[ref] = true
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		messages, status := readOnlyHistory(ctx, pt)
		c.dispatch(func() {
			delete(c.referenceLoading, ref)
			if status != "" {
				c.setStatus(tr("Failed to load reference context: ") + status + tr("; retry or remove the reference"))
				return
			}
			if c.threadHistory == nil {
				c.threadHistory = map[string][]Message{}
			}
			c.threadHistory[ref.URL()] = messages
			c.setStatus(tr("Reference context loaded. Send again."))
		})
	}()
}

// noteHandoffs records explicit delegation, independently of context references.
func (c *chatController) noteHandoffs(sources []Thread, target int, task string) {
	for _, src := range sources {
		if src.ID == 0 || src.ID == target {
			continue
		}
		item := fmt.Sprintf("handoff:%d:%d", target, len(c.handoffs))
		c.handoffs = append(c.handoffs, handoff{From: src.ID, To: target, Item: item})
		status, detail := c.handoffState(target)
		agent := SubAgent{ID: "thread:" + strconv.Itoa(target), Name: textutil.FirstNonEmpty(task, tr("Handoff task")), Status: textutil.FirstNonEmpty(status, subAgentPending), Detail: detail}
		title := threadRefTitle(c.threadWithID(target))
		c.update(src.ID, func(t *Thread) {
			// The agents list says it all; the title names the target.
			t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "activity", Kind: "handoff", HandoffTitle: title, Status: "completed", ItemID: item, Agents: []SubAgent{agent}})
		})
	}
}

// handoffState is how the work sent to a conversation goes: running while
// it runs, then what its last turn ended with; "" when it is gone.
func (c *chatController) handoffState(target int) (string, string) {
	if t, _, ok := c.findThread(target); ok {
		if t.Busy || t.Loading {
			return subAgentRunning, ""
		}
		last := -1
		for i, m := range t.Messages {
			if m.Role == "user" {
				last = i
			}
		}
		status, detail := subAgentPending, ""
		for _, m := range t.Messages[last+1:] {
			switch m.Role {
			case "assistant":
				if s := textutil.FirstLine(m.Content); s != "" {
					status, detail = subAgentCompleted, s
				}
			case "error":
				status, detail = subAgentFailed, textutil.FirstLine(m.Content)
			}
		}
		return status, detail
	}
	return "", ""
}

// refreshHandoffs brings the activities following target's work up to its
// state, touching a conversation only when its activity changes.
func (c *chatController) refreshHandoffs(target int) {
	for _, h := range c.handoffs {
		if h.To != target {
			continue
		}
		status, detail := c.handoffState(target)
		if status == "" {
			continue
		}
		if a, ok := c.handoffAgent(h); !ok || a.Status == status && a.Detail == detail {
			continue
		}
		c.update(h.From, func(t *Thread) {
			for i := range t.Messages {
				if t.Messages[i].ItemID == h.Item && len(t.Messages[i].Agents) > 0 {
					a := t.Messages[i].Agents[0]
					a.Status, a.Detail = status, detail
					t.Messages[i].Agents = []SubAgent{a}
				}
			}
		})
	}
}

func (c *chatController) handoffAgent(h handoff) (SubAgent, bool) {
	for _, t := range c.threads() {
		if t.ID != h.From {
			continue
		}
		for _, m := range t.Messages {
			if m.ItemID == h.Item && len(m.Agents) > 0 {
				return m.Agents[0], true
			}
		}
	}
	return SubAgent{}, false
}

// Conversation choices carry their identity separately from their label.
func (app *chatApp) threadChoices(query string) []composerChoice {
	var out []composerChoice
	for _, match := range matchingThreadRefs(app.c.threadRefProjects(), app.c.active(), query) {
		t := match.Thread
		ref := app.c.threadRef(t)
		if !ref.valid() {
			continue
		}
		out = append(out, composerChoice{Name: threadRefTitle(t), Detail: tr("Conversation · ") + backendOf(t).Name() + " · " + match.ProjectName, Ref: &ref})
	}
	return out
}

func insertThreadReference(value string, token composerToken, ref ThreadRef, title string) string {
	if token.Kind != '@' || token.Start < 0 || token.End < token.Start || token.End > len(value) || !ref.valid() {
		return value
	}
	return value[:token.Start] + referenceText(ref, title) + " " + value[token.End:]
}

func (app *chatApp) handoffToNewThread() {
	if app.onHandoff != nil {
		app.onHandoff()
		return
	}
	c := app.c
	t := c.current()
	ref := c.threadRef(t)
	if !ref.valid() {
		app.notice = tr("Available once the session is created")
		return
	}
	c.newThread()
	c.rememberDraft(referenceText(ref, threadRefTitle(t)) + " ")
	c.prefetchReference(ref)
	app.notice = tr("Referenced conversation \"") + threadRefTitle(t) + tr("\": choose an agent, describe the task, and send")
}
