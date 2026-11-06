package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/subagent"
	"miya/internal/textutil"
)

// Keep the exact reference tokens. Titles never need to be reversed into IDs.
// An envelope is a send-time snapshot; replaying a queued request preserves it.
func ExpandThreadRefs(text string, self model.ThreadRef, resolve func(model.ThreadRef) (model.Thread, []model.Message, error)) (string, []model.Thread, error) {
	own, snapshots := model.SplitReferenceContext(text)
	if len(snapshots) > 0 {
		return text, nil, nil
	}
	var sources []model.Thread
	titles := map[model.ThreadRef]string{}
	for _, m := range model.ThreadMentions(own) {
		if _, ok := titles[m.Ref]; !ok {
			titles[m.Ref] = m.Title
		}
	}
	for _, ref := range msgfmt.ThreadRefs(own) {
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
		snapshots = append(snapshots, model.ReferenceSnapshot{Ref: ref, Title: titles[ref], Context: threadPacket(t, messages)})
		sources = append(sources, t)
	}
	if len(snapshots) == 0 {
		return text, nil, nil
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return text, nil, err
	}
	return own + model.ContextEnvelopeOpen + string(data) + model.ContextEnvelopeClose, sources, nil
}

// threadPacket is a conversation's context as another agent reads it: its
// first and latest requests, its latest conclusions, the files it changed
// and its sub-agents, each bounded, without its tool output.
func threadPacket(t model.Thread, messages []model.Message) string {
	title := appmodel.ThreadRefTitle(t)
	var b strings.Builder
	fmt.Fprintf(&b, tr("Session ID: %s\nBackend: %s\n"), t.ServerID, model.ThreadAgent(t))
	fmt.Fprintf(&b, tr("The following context from conversation \"%s\" (%s) was compiled by Miya from its history for reference.\n"), title, BackendOf(t).Name())
	var requests []string
	var turns [][]model.Message
	for _, m := range messages {
		if m.Role == "user" {
			requests = append(requests, strings.TrimSpace(appmodel.ThreadContextDisplay(m.Content)))
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
		msgfmt.PacketSection(&b, tr("Original request"), textutil.ClipRunes(requests[0], 800))
		if len(requests) > 1 {
			msgfmt.PacketSection(&b, tr("Latest request"), textutil.ClipRunes(requests[len(requests)-1], 800))
		}
	}
	var conclusions []string
	for i := max(0, len(turns)-2); i < len(turns); i++ {
		if reply := msgfmt.LastReply(turns[i]); reply != "" {
			conclusions = append(conclusions, textutil.ClipRunes(reply, 1500))
		}
	}
	if len(conclusions) > 0 {
		msgfmt.PacketSection(&b, tr("Latest conclusions"), strings.Join(conclusions, "\n\n---\n\n"))
	}
	if files := msgfmt.ChangedFiles(messages); len(files) > 0 {
		if len(files) > 30 {
			files = append(files[:30], fmt.Sprintf(tr("…and %d more files"), len(files)-30))
		}
		msgfmt.PacketSection(&b, tr("Changed files"), "- "+strings.Join(files, "\n- "))
	}
	if agents := subagent.Latest(messages); len(agents) > 0 {
		var lines []string
		for _, a := range agents {
			line := textutil.FirstNonEmpty(a.Name, a.ID) + " · " + subagent.StatusLabel(a.Status)
			if a.Detail != "" {
				line += " · " + textutil.ClipRunes(a.Detail, 200)
			}
			lines = append(lines, line)
			if len(lines) == 10 {
				break
			}
		}
		msgfmt.PacketSection(&b, tr("Subtasks"), "- "+strings.Join(lines, "\n- "))
	}
	return b.String()
}

// handoff links a delegating conversation to the one receiving its task;
// Item is the activity that follows the work in the delegating one.
type handoff struct {
	From, To int
	Item     string
}

// threadRef returns no public identity until the backend session exists.
func (c *Controller) ThreadRef(t model.Thread) model.ThreadRef {
	return model.ThreadRef{Source: c.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
}

// Local numeric lookup is retained only for internal controls and MCP routing.
func (c *Controller) resolveThreadRef(id int) (model.Thread, []model.Message, bool) {
	t, _, ok := c.findThread(id)
	if !ok {
		return model.Thread{}, nil, false
	}
	messages := t.Messages
	if !t.HistoryLoaded {
		if cached, ok := c.threadHistory[c.ThreadRef(t).URL()]; ok {
			messages = cached
			t.HistoryLoaded = true
		}
	}
	return t, messages, true
}

func (c *Controller) ResolveReference(ref model.ThreadRef) (model.Thread, []model.Message, error) {
	if !ref.Valid() {
		return model.Thread{}, nil, fmt.Errorf("%s", tr("Invalid session identity"))
	}
	if ref.Source != c.ReferenceSource {
		return model.Thread{}, nil, fmt.Errorf("%s", tr("This reference belongs to another desktop. Connect to its source and try again."))
	}
	for _, project := range c.ThreadRefProjects() {
		for _, t := range project.Threads {
			if model.ThreadAgent(t) != ref.Backend || t.ServerID != ref.SessionID {
				continue
			}
			if t.HistoryLoaded {
				return t, t.Messages, nil
			}
			if messages, ok := c.threadHistory[ref.URL()]; ok {
				t.HistoryLoaded = true
				return t, messages, nil
			}
			c.PrefetchReference(ref)
			return model.Thread{}, nil, fmt.Errorf("%s", tr("Context is loading. Try again; if loading fails, select the reference again or remove it."))
		}
	}
	if session, ok := c.ForkedReferences[ref]; ok {
		t := model.Thread{ServerID: ref.SessionID, Agent: ref.Backend, Title: session.Title}
		if messages, loaded := c.threadHistory[ref.URL()]; loaded {
			t.HistoryLoaded = true
			return t, messages, nil
		}
		c.PrefetchReference(ref)
		return model.Thread{}, nil, fmt.Errorf("%s", tr("The source conversation is loading. Try again."))
	}
	return model.Thread{}, nil, fmt.Errorf("%s", tr("The conversation no longer exists or is unavailable"))
}

// findThread is a conversation of the open project or of one in the
// background, and its project's path.
func (c *Controller) findThread(id int) (model.Thread, string, bool) {
	for _, t := range c.Threads() {
		if t.ID == id {
			return t, c.Cwd, true
		}
	}
	for path, threads := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for _, t := range threads {
			if t.ID == id {
				return t, path, true
			}
		}
	}
	return model.Thread{}, "", false
}

func (c *Controller) prefetchThread(id int) {
	if t, _, ok := c.findThread(id); ok {
		c.PrefetchReference(c.ThreadRef(t))
	}
}

// Cache by backend identity. A fork or reused UI number cannot inherit history.
func (c *Controller) PrefetchReference(ref model.ThreadRef) {
	if !ref.Valid() {
		return
	}
	if c.Remote != nil {
		c.forward("prefetch", ref)
		return
	}
	if ref.Source != c.ReferenceSource {
		return
	}
	if _, ok := c.threadHistory[ref.URL()]; ok {
		return
	}
	if c.referenceLoading == nil {
		c.referenceLoading = map[model.ThreadRef]bool{}
	}
	if c.referenceLoading[ref] {
		return
	}
	var pt phoneThread
	for _, project := range c.ThreadRefProjects() {
		for _, t := range project.Threads {
			if model.ThreadAgent(t) == ref.Backend && t.ServerID == ref.SessionID {
				if t.HistoryLoaded {
					return
				}
				pt = phoneThread{Thread: t, Found: true, cwd: project.Path, client: c.Client}
			}
		}
	}
	if !pt.Found {
		if session, ok := c.ForkedReferences[ref]; ok {
			pt = phoneThread{Thread: model.Thread{ServerID: ref.SessionID, Agent: ref.Backend, Title: session.Title}, Found: true, cwd: session.Project, client: c.Client}
		}
	}
	if !pt.Found {
		return
	}
	c.referenceLoading[ref] = true
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		messages, status := readOnlyHistory(ctx, pt)
		c.Dispatch(func() {
			delete(c.referenceLoading, ref)
			if status != "" {
				c.SetStatus(tr("Failed to load reference context: ") + status + tr("; retry or remove the reference"))
				return
			}
			if c.threadHistory == nil {
				c.threadHistory = map[string][]model.Message{}
			}
			c.threadHistory[ref.URL()] = messages
			c.SetStatus(tr("Reference context loaded. Send again."))
		})
	}()
}

// noteHandoffs records explicit delegation, independently of context references.
func (c *Controller) noteHandoffs(sources []model.Thread, target int, task string) {
	for _, src := range sources {
		if src.ID == 0 || src.ID == target {
			continue
		}
		item := fmt.Sprintf("handoff:%d:%d", target, len(c.Handoffs))
		c.Handoffs = append(c.Handoffs, handoff{From: src.ID, To: target, Item: item})
		status, detail := c.HandoffState(target)
		agent := model.SubAgent{ID: "thread:" + strconv.Itoa(target), Name: textutil.FirstNonEmpty(task, tr("Handoff task")), Status: textutil.FirstNonEmpty(status, subagent.Pending), Detail: detail}
		title := appmodel.ThreadRefTitle(c.ThreadWithID(target))
		c.Update(src.ID, func(t *model.Thread) {
			// The agents list says it all; the title names the target.
			t.Messages = append(t.Messages, model.Message{ID: c.NextID(), Role: "activity", Kind: "handoff", HandoffTitle: title, Status: "completed", ItemID: item, Agents: []model.SubAgent{agent}})
		})
	}
}

// handoffState is how the work sent to a conversation goes: running while
// it runs, then what its last turn ended with; "" when it is gone.
func (c *Controller) HandoffState(target int) (string, string) {
	if t, _, ok := c.findThread(target); ok {
		if t.Busy || t.Loading {
			return subagent.Running, ""
		}
		last := -1
		for i, m := range t.Messages {
			if m.Role == "user" {
				last = i
			}
		}
		status, detail := subagent.Pending, ""
		for _, m := range t.Messages[last+1:] {
			switch m.Role {
			case "assistant":
				if s := textutil.FirstLine(m.Content); s != "" {
					status, detail = subagent.Completed, s
				}
			case "error":
				status, detail = subagent.Failed, textutil.FirstLine(m.Content)
			}
		}
		return status, detail
	}
	return "", ""
}

// refreshHandoffs brings the activities following target's work up to its
// state, touching a conversation only when its activity changes.
func (c *Controller) refreshHandoffs(target int) {
	for _, h := range c.Handoffs {
		if h.To != target {
			continue
		}
		status, detail := c.HandoffState(target)
		if status == "" {
			continue
		}
		if a, ok := c.handoffAgent(h); !ok || a.Status == status && a.Detail == detail {
			continue
		}
		c.Update(h.From, func(t *model.Thread) {
			for i := range t.Messages {
				if t.Messages[i].ItemID == h.Item && len(t.Messages[i].Agents) > 0 {
					a := t.Messages[i].Agents[0]
					a.Status, a.Detail = status, detail
					t.Messages[i].Agents = []model.SubAgent{a}
				}
			}
		})
	}
}

func (c *Controller) handoffAgent(h handoff) (model.SubAgent, bool) {
	for _, t := range c.Threads() {
		if t.ID != h.From {
			continue
		}
		for _, m := range t.Messages {
			if m.ItemID == h.Item && len(m.Agents) > 0 {
				return m.Agents[0], true
			}
		}
	}
	return model.SubAgent{}, false
}
