package controller

import (
	"encoding/json"
	"fmt"
	"time"

	"miya/internal/codex"
	"miya/internal/mcpconfig"
	"miya/internal/model"
	"miya/internal/svcproto"
)

func (c *Controller) removeRequest(id json.RawMessage) {
	out := []codex.Approval{}
	for _, v := range c.Approvals() {
		if string(v.ID) != string(id) {
			out = append(out, v)
		}
	}
	c.SetApprovals(out)
}

func (c *Controller) ServerRequest(f codex.Frame) {
	switch f.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		var a codex.Approval
		if json.Unmarshal(f.Params, &a) != nil {
			go c.Client.Reject(f.ID, "Malformed server request")
			return
		}
		a.ID = f.ID
		a.Method = f.Method
		a.Payload = f.Params
		for _, v := range c.Approvals() {
			if string(v.ID) == string(a.ID) {
				return
			}
		}
		if c.Client != nil && c.RememberedMCPAccess(a) {
			client := c.Client
			go func() {
				if err := client.Reply(a.ID, mcpconfig.AccessAccepted()); err != nil {
					c.Dispatch(func() { c.SetStatus("Unable to respond to conversation approval: " + err.Error()) })
				}
			}()
			return
		}
		c.SetApprovals(append(append([]codex.Approval(nil), c.Approvals()...), a))
	default:
		client := c.Client
		go client.Reject(f.ID, "This client does not yet support "+f.Method)
		c.SetStatus("Unsupported server request: " + f.Method)
	}
}

func (c *Controller) Respond(a codex.Approval, result any) {
	if c.Remote != nil {
		c.removeRequest(a.ID)
		c.forward("respond", svcproto.Respond{ID: a.ID, Result: result})
		return
	}
	// A stale card may have been resolved by interruption while the UI was open.
	found := false
	for _, v := range c.Approvals() {
		if string(v.ID) == string(a.ID) {
			found = true
		}
	}
	if !found {
		return
	}
	if codex.IsMiyaApproval(a) {
		c.respondMiya(a, result)
		return
	}
	ApprovalOwner(a).Respond(c, a, result)
}

// askUser shows a card in caller's conversation and returns its id and the
// channel its answer arrives on.
func (c *Controller) AskUser(caller model.Thread, reason, detail string) (json.RawMessage, chan bool) {
	c.miyaSerial++
	id, _ := json.Marshal(fmt.Sprintf("miya:%d:%d", caller.ID, c.miyaSerial))
	ch := make(chan bool, 1)
	if c.miyaPending == nil {
		c.miyaPending = map[string]chan bool{}
	}
	c.miyaPending[string(id)] = ch
	a := codex.Approval{ID: id, Method: "miya/automation", ThreadID: caller.ServerID, Reason: reason, Command: detail}
	c.SetApprovals(append(append([]codex.Approval(nil), c.Approvals()...), a))
	return id, ch
}

func (c *Controller) respondMiya(a codex.Approval, result any) {
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
func (c *Controller) waitUser(id json.RawMessage, ch chan bool) bool {
	select {
	case ok := <-ch:
		return ok
	case <-time.After(svcproto.CardTimeout):
		c.Dispatch(func() {
			delete(c.miyaPending, string(id))
			c.removeRequest(id)
		})
		return false
	}
}

// approvalThread is the conversation an approval waits in, Codex's by its
// thread id and Claude Code's by the conversation in its request id.
func (c *Controller) approvalThread(a codex.Approval) (model.Thread, bool) {
	if claudeApproval(a) {
		var s string
		_ = json.Unmarshal(a.ID, &s)
		var id int
		if _, err := fmt.Sscanf(s, "claude:%d:", &id); err != nil {
			return model.Thread{}, false
		}
		t := c.ThreadWithID(id)
		return t, t.ID != 0
	}
	if a.ThreadID == "" {
		return model.Thread{}, false
	}
	for _, t := range c.Threads() {
		if t.ServerID == a.ThreadID {
			return t, true
		}
	}
	for path, threads := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		for _, t := range threads {
			if t.ServerID == a.ThreadID {
				return t, true
			}
		}
	}
	return model.Thread{}, false
}

// threadAwaitsUser reports whether a conversation waits for an approval or
// an answer.
func (c *Controller) ThreadAwaitsUser(t model.Thread) bool {
	if c.Approvals == nil {
		return false
	}
	for _, a := range c.Approvals() {
		if at, ok := c.approvalThread(a); ok && at.ID == t.ID {
			return true
		}
	}
	return false
}
