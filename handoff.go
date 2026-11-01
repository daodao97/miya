package main

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// A message can reference another conversation of the workspace as @#<id>,
// whichever agents run the two. As the message is sent, the service expands
// each reference into that conversation's context packet, which Miya builds
// from its normalized messages, and the referenced conversation then follows
// the work handed off to the other as a sub-agent.

const (
	threadContextOpen  = "<miya-thread-context"
	threadContextClose = "</miya-thread-context>"
)

var (
	threadRefPattern     = regexp.MustCompile(`(^|[^0-9A-Za-z_@#])@#(\d+)\b`)
	threadContextPattern = regexp.MustCompile(`(?s)<miya-thread-context[^>]*?title="([^"]*)"[^>]*>.*?</miya-thread-context>`)
)

// threadRefs are the conversations a message references, in order, once
// each. Only the message's own words count, not context already expanded.
func threadRefs(text string) []int {
	own, _, _ := strings.Cut(text, threadContextOpen)
	var ids []int
	seen := map[int]bool{}
	for _, m := range threadRefPattern.FindAllStringSubmatch(own, -1) {
		if id, err := strconv.Atoi(m[2]); err == nil && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// threadRefTask is what a message asks, without its references: the name
// of the work it hands off.
func threadRefTask(text string) string {
	own, _, _ := strings.Cut(text, threadContextOpen)
	own = threadRefPattern.ReplaceAllString(own, "$1")
	return clipRunes(strings.Join(strings.Fields(firstLine(strings.TrimSpace(own))), " "), 80)
}

func threadRefTitle(t Thread) string {
	return firstNonEmpty(strings.TrimSpace(t.Title), "#"+strconv.Itoa(t.ID))
}

// expandThreadRefs turns the references of a message sent to conversation
// self into names in its words and context packets after them; resolve
// gives a conversation and its messages. It returns the conversations it
// expanded; references to unknown ones and to self stay as they are.
func expandThreadRefs(text string, self int, resolve func(int) (Thread, []Message, bool)) (string, []Thread) {
	own, rest, expanded := strings.Cut(text, threadContextOpen)
	var packets []string
	var sources []Thread
	for _, id := range threadRefs(text) {
		if id == self {
			continue
		}
		t, messages, ok := resolve(id)
		if !ok {
			continue
		}
		name := strings.ReplaceAll("「对话："+threadRefTitle(t)+"」", "$", "$$")
		own = regexp.MustCompile(`(^|[^0-9A-Za-z_@#])@#`+strconv.Itoa(id)+`\b`).ReplaceAllString(own, "${1}"+name)
		packets = append(packets, threadPacket(t, messages))
		sources = append(sources, t)
	}
	if len(packets) == 0 {
		return text, nil
	}
	out := strings.TrimRight(own, " \t\r\n") + "\n\n" + strings.Join(packets, "\n\n")
	if expanded {
		out += "\n\n" + threadContextOpen + rest
	}
	return out, sources
}

// threadPacket is a conversation's context as another agent reads it: its
// first and latest requests, its latest conclusions, the files it changed
// and its sub-agents, each bounded, without its tool output.
func threadPacket(t Thread, messages []Message) string {
	title := threadRefTitle(t)
	var b strings.Builder
	fmt.Fprintf(&b, "%s id=\"%d\" agent=\"%s\" title=\"%s\">\n", threadContextOpen, t.ID, html.EscapeString(threadAgent(t)), html.EscapeString(title))
	fmt.Fprintf(&b, "以下是对话「%s」（%s）的上下文，由 Miya 从它的记录整理，供参考。\n", title, backendOf(t).Name())
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
		b.WriteString("\n（这个对话的记录还没有加载，只有标题可供参考。）\n")
	}
	if len(requests) > 0 {
		packetSection(&b, "最初的请求", clipRunes(requests[0], 800))
		if len(requests) > 1 {
			packetSection(&b, "最近的请求", clipRunes(requests[len(requests)-1], 800))
		}
	}
	var conclusions []string
	for i := max(0, len(turns)-2); i < len(turns); i++ {
		if reply := lastReply(turns[i]); reply != "" {
			conclusions = append(conclusions, clipRunes(reply, 1500))
		}
	}
	if len(conclusions) > 0 {
		packetSection(&b, "最近的结论", strings.Join(conclusions, "\n\n---\n\n"))
	}
	if files := changedFiles(messages); len(files) > 0 {
		if len(files) > 30 {
			files = append(files[:30], fmt.Sprintf("…另外 %d 个文件", len(files)-30))
		}
		packetSection(&b, "改动的文件", "- "+strings.Join(files, "\n- "))
	}
	if agents := latestSubAgents(messages); len(agents) > 0 {
		var lines []string
		for _, a := range agents {
			line := firstNonEmpty(a.Name, a.ID) + " · " + subAgentStatusLabel(a.Status)
			if a.Detail != "" {
				line += " · " + clipRunes(a.Detail, 200)
			}
			lines = append(lines, line)
			if len(lines) == 10 {
				break
			}
		}
		packetSection(&b, "子任务", "- "+strings.Join(lines, "\n- "))
	}
	b.WriteString(threadContextClose)
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

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// threadContextDisplay shows the context packets of a message as the
// conversations they name, not the text the agent read.
func threadContextDisplay(s string) string {
	return threadContextPattern.ReplaceAllStringFunc(s, func(block string) string {
		m := threadContextPattern.FindStringSubmatch(block)
		return "〔引用对话：" + html.UnescapeString(m[1]) + "〕"
	})
}

// handoff links a referenced conversation to the one its context was sent
// to; Item is the activity that follows the work in the referenced one.
type handoff struct {
	From, To int
	Item     string
}

// resolveThreadRef is a conversation of the workspace and its messages, as
// loaded or as prefetched.
func (c *chatController) resolveThreadRef(id int) (Thread, []Message, bool) {
	t, path, ok := c.findThread(id)
	if !ok {
		return Thread{}, nil, false
	}
	messages := t.Messages
	if len(messages) == 0 {
		messages = c.threadHistory[id]
	}
	// A Claude Code session is a local file, quick to read now; a phone,
	// which cannot prefetch, references such conversations too.
	if len(messages) == 0 && threadAgent(t) == agentClaude && t.ServerID != "" {
		if messages, _ = readOnlyHistory(c.ctx, phoneThread{thread: t, found: true, cwd: path}); len(messages) > 0 {
			if c.threadHistory == nil {
				c.threadHistory = map[int][]Message{}
			}
			c.threadHistory[id] = messages
		}
	}
	return t, messages, true
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

// prefetchThread reads a referenced conversation's history in the
// background, so its context is at hand when the message is sent.
func (c *chatController) prefetchThread(id int) {
	if c.remote != nil {
		c.forward("prefetch", id)
		return
	}
	if _, messages, ok := c.resolveThreadRef(id); !ok || len(messages) > 0 {
		return
	}
	pt := c.phoneThread(id)
	go func() {
		messages, _ := readOnlyHistory(c.ctx, pt)
		if len(messages) == 0 {
			return
		}
		c.dispatch(func() {
			if c.threadHistory == nil {
				c.threadHistory = map[int][]Message{}
			}
			c.threadHistory[id] = messages
		})
	}()
}

// noteHandoffs adds to each referenced conversation an activity that
// follows the work sent to target as a sub-agent.
func (c *chatController) noteHandoffs(sources []Thread, target int, task string) {
	for _, src := range sources {
		if src.ID == target {
			continue
		}
		item := fmt.Sprintf("handoff:%d:%d", target, len(c.handoffs))
		c.handoffs = append(c.handoffs, handoff{From: src.ID, To: target, Item: item})
		status, detail := c.handoffState(target)
		agent := SubAgent{ID: "thread:" + strconv.Itoa(target), Name: firstNonEmpty(task, "对话 #"+strconv.Itoa(target)), Status: firstNonEmpty(status, subAgentPending), Detail: detail}
		c.update(src.ID, func(t *Thread) {
			// The agents list says it all; the title names the target.
			t.Messages = append(t.Messages, Message{ID: c.nextID(), Role: "activity", Kind: "handoff", Status: "completed", ItemID: item, Agents: []SubAgent{agent}})
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
				if s := firstLine(m.Content); s != "" {
					status, detail = subAgentCompleted, s
				}
			case "error":
				status, detail = subAgentFailed, firstLine(m.Content)
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

// threadChoices are the conversations an @ in the composer offers, before
// the project files.
func (app *chatApp) threadChoices(query string) []composerChoice {
	var out []composerChoice
	for _, ref := range matchingThreadRefs(app.c.threadRefProjects(), app.c.active(), query) {
		t := ref.Thread
		out = append(out, composerChoice{Name: fmt.Sprintf("#%d %s", t.ID, threadRefTitle(t)), Detail: "对话 · " + backendOf(t).Name() + " · " + ref.ProjectName})
	}
	return out
}

// threadChoiceID is the conversation a composer choice names, if it names one.
func threadChoiceID(name string) (int, bool) {
	if !strings.HasPrefix(name, "#") {
		return 0, false
	}
	n, _, _ := strings.Cut(name[1:], " ")
	id, err := strconv.Atoi(n)
	return id, err == nil
}

func insertThreadReference(value string, token composerToken, id int) string {
	if token.Kind != '@' || token.Start < 0 || token.End > len(value) {
		return value
	}
	return value[:token.Start] + "@#" + strconv.Itoa(id) + " " + value[token.End:]
}

// handoffToNewThread starts a conversation that begins with the open one's
// context; the composer's agent picker chooses who takes the work.
func (app *chatApp) handoffToNewThread() {
	c := app.c
	source := c.active()
	if source == 0 {
		return
	}
	c.newThread()
	c.rememberDraft(fmt.Sprintf("@#%d ", source))
	app.notice = fmt.Sprintf("已引用对话 #%d：选择 agent，写下要交付的任务后发送", source)
}
