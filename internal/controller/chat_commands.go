package controller

import (
	svcproto "miya/internal/service/protocol"
)

// RunThreadCommand routes an explicit command through the selected workspace
// adapter and selects the conversation returned by it.
func (c *Controller) RunThreadCommand(command string) string {
	id, message := c.ThreadCommand(c.Current().ID, c.CurrentAgent(), command, c.CurrentPermissionPolicy())
	if id != 0 {
		c.SetActive(id)
		c.SetDraft("")
	}
	return message
}

// threadCommand runs a command in a conversation, 0 for a new one of agent;
// it returns the conversation running it, or why it cannot run.
func (c *Controller) ThreadCommand(threadID int, agent, command, policy string) (int, string) {
	if c.Remote != nil {
		var r svcproto.CommandResult
		if err := c.Remote.Call("command", svcproto.Command{Thread: threadID, Agent: agent, Command: command, Policy: policy}, &r); err != nil {
			return 0, err.Error()
		}
		c.fastMode = r.FastMode
		return r.Thread, r.Message
	}
	t := c.ThreadWithID(threadID)
	backend := c.BackendOf(t)
	if t.ID == 0 {
		backend = c.BackendFor(agent)
	}
	return backend.RunCommand(threadID, command, policy)
}
