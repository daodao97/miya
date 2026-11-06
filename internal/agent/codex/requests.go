package codex

import (
	"encoding/json"

	codex "miya/internal/agent/codex/native"
	mcpaccess "miya/internal/mcp/access"
)

func (b Adapter) HandleRequest(f codex.Frame) {
	c := b.host
	switch f.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		var a codex.Approval
		if json.Unmarshal(f.Params, &a) != nil {
			go c.Snapshot().Client.Reject(f.ID, "Malformed server request")
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
		if c.Snapshot().Client != nil && c.RememberedMCPAccess(a) {
			client := c.Snapshot().Client
			go func() {
				if err := client.Reply(a.ID, mcpaccess.Accepted()); err != nil {
					c.Dispatch(func() { c.SetStatus("Unable to respond to conversation approval: " + err.Error()) })
				}
			}()
			return
		}
		c.SetApprovals(append(append([]codex.Approval(nil), c.Approvals()...), a))
	default:
		client := c.Snapshot().Client
		go client.Reject(f.ID, "This client does not yet support "+f.Method)
		c.SetStatus("Unsupported server request: " + f.Method)
	}
}
