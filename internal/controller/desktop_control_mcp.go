package controller

import (
	"encoding/json"
	"errors"
	"strings"

	"miya/internal/async"
	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/mcpproto"
)

func (c *Controller) McpDesktopControl(r mcpproto.Request) (any, error) {
	caller, ok := c.McpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	reason := strings.TrimSpace(r.Task)
	if reason == "" || len(reason) > 2000 {
		return nil, errors.New("Describe the action required from the user in 1–2000 bytes")
	}
	for _, a := range c.Approvals() {
		if a.Method == "miya/desktopControl" && a.ThreadID == caller.ServerID {
			return nil, errors.New("This conversation already has a pending takeover request")
		}
	}
	if len(r.WindowApp) > 256 || len(r.WindowTitle) > 1000 {
		return nil, errors.New("Window name is too long")
	}
	hint := control.WindowHint{App: strings.TrimSpace(r.WindowApp), Title: strings.TrimSpace(r.WindowTitle)}
	if windows, err := desktop.Windows(); err == nil {
		if target, err := control.ChooseWindow(windows, hint); err == nil {
			hint.Window = &target
		}
	}
	id, ch := c.AskUser(caller, "Action required in the authorization window", reason+"\nOn your phone, tap the window preview to enter a password or authorize access, then return to this card to confirm. You can also act directly on your Mac.")
	list := append([]codex.Approval(nil), c.Approvals()...)
	list[len(list)-1].Method = "miya/desktopControl"
	list[len(list)-1].Payload, _ = json.Marshal(hint)
	c.SetApprovals(list)
	return async.Reply(func() (any, error) {
		if !c.waitUser(id, ch) {
			return "The user canceled takeover or the request timed out. Stop any steps that depend on it.", nil
		}
		return "The user confirmed that the manual action is complete. Check the target page again before continuing.", nil
	}), nil
}
