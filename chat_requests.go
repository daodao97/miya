package main

import (
	"encoding/json"
	"fmt"
	"miya/internal/codex"
	"strings"
	"time"
)

func (c *chatController) removeRequest(id json.RawMessage) {
	out := []codex.Approval{}
	for _, v := range c.approvals() {
		if string(v.ID) != string(id) {
			out = append(out, v)
		}
	}
	c.setApprovals(out)
}

func (c *chatController) serverRequest(f codex.Frame) {
	switch f.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		var a codex.Approval
		if json.Unmarshal(f.Params, &a) != nil {
			go c.client.Reject(f.ID, "Malformed server request")
			return
		}
		a.ID = f.ID
		a.Method = f.Method
		a.Payload = f.Params
		for _, v := range c.approvals() {
			if string(v.ID) == string(a.ID) {
				return
			}
		}
		if c.client != nil && c.rememberedMCPAccess(a) {
			client := c.client
			go func() {
				if err := client.Reply(a.ID, mcpAccessAccepted()); err != nil {
					c.dispatch(func() { c.setStatus("Unable to respond to conversation approval: " + err.Error()) })
				}
			}()
			return
		}
		c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
	default:
		client := c.client
		go client.Reject(f.ID, "This client does not yet support "+f.Method)
		c.setStatus("Unsupported server request: " + f.Method)
	}
}

func (c *chatController) respond(a codex.Approval, result any) {
	if c.remote != nil {
		c.removeRequest(a.ID)
		c.forward("respond", svcRespond{ID: a.ID, Result: result})
		return
	}
	// A stale card may have been resolved by interruption while the UI was open.
	found := false
	for _, v := range c.approvals() {
		if string(v.ID) == string(a.ID) {
			found = true
		}
	}
	if !found {
		return
	}
	if isMiyaApproval(a) {
		c.respondMiya(a, result)
		return
	}
	approvalOwner(a).Respond(c, a, result)
}

func (c *chatController) approvalDetail(a codex.Approval) string {
	text := a.Reason + "\n" + a.Command
	if a.Cwd != "" {
		text += "\nDirectory: " + a.Cwd
	}
	if a.GrantRoot != "" {
		text += "\nRequested root: " + a.GrantRoot
	}
	if len(a.Permissions) > 0 {
		text += "\n" + jsonText(a.Permissions)
	}
	for _, t := range c.threads() {
		if t.ServerID == a.ThreadID {
			text = "Thread: " + t.Title + "\n" + text
			if a.Method == "item/fileChange/requestApproval" {
				for _, m := range t.Messages {
					if m.ItemID == a.ItemID {
						text += "\n" + m.Content
					}
				}
			}
		}
	}
	return strings.TrimSpace(text)
}

// askUser shows a card in caller's conversation and returns its id and the
// channel its answer arrives on.
func (c *chatController) askUser(caller Thread, reason, detail string) (json.RawMessage, chan bool) {
	c.miyaSerial++
	id, _ := json.Marshal(fmt.Sprintf("miya:%d:%d", caller.ID, c.miyaSerial))
	ch := make(chan bool, 1)
	if c.miyaPending == nil {
		c.miyaPending = map[string]chan bool{}
	}
	c.miyaPending[string(id)] = ch
	a := codex.Approval{ID: id, Method: "miya/automation", ThreadID: caller.ServerID, Reason: reason, Command: detail}
	c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
	return id, ch
}

func (c *chatController) respondMiya(a codex.Approval, result any) {
	c.removeRequest(a.ID)
	ch := c.miyaPending[string(a.ID)]
	delete(c.miyaPending, string(a.ID))
	if ch == nil {
		return
	}
	data, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(data, &r)
	ch <- r.Decision == "accept"
}

// waitUser waits off the main thread for the answer to a card.
func (c *chatController) waitUser(id json.RawMessage, ch chan bool) bool {
	select {
	case ok := <-ch:
		return ok
	case <-time.After(miyaRequestTimeout):
		c.dispatch(func() {
			delete(c.miyaPending, string(id))
			c.removeRequest(id)
		})
		return false
	}
}

// approvalThread is the conversation an approval waits in, Codex's by its
// thread id and Claude Code's by the conversation in its request id.
func (c *chatController) approvalThread(a codex.Approval) (Thread, bool) {
	if claudeApproval(a) {
		var s string
		_ = json.Unmarshal(a.ID, &s)
		var id int
		if _, err := fmt.Sscanf(s, "claude:%d:", &id); err != nil {
			return Thread{}, false
		}
		t := c.threadWithID(id)
		return t, t.ID != 0
	}
	if a.ThreadID == "" {
		return Thread{}, false
	}
	for _, t := range c.threads() {
		if t.ServerID == a.ThreadID {
			return t, true
		}
	}
	for path, threads := range c.projectCache {
		if path == c.cwd {
			continue
		}
		for _, t := range threads {
			if t.ServerID == a.ThreadID {
				return t, true
			}
		}
	}
	return Thread{}, false
}

// threadAwaitsUser reports whether a conversation waits for an approval or
// an answer.
func (c *chatController) threadAwaitsUser(t Thread) bool {
	if c.approvals == nil {
		return false
	}
	for _, a := range c.approvals() {
		if at, ok := c.approvalThread(a); ok && at.ID == t.ID {
			return true
		}
	}
	return false
}
