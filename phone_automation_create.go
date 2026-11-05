package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/remote"
)

type phoneAutomationFormResult struct {
	RequestID string           `json:"requestId"`
	View      *automation.View `json:"view,omitempty"`
}

func phoneAutomationFormFrame(r remote.Request, view *automation.View, err error) remote.Frame {
	data, _ := json.Marshal(phoneAutomationFormResult{RequestID: r.RequestID, View: view})
	f := remote.Frame{Type: r.Op, Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	return f
}

func (c *chatController) phoneAutomationDraft(ctx context.Context, r remote.Request) remote.Frame {
	type pendingDraft struct {
		work deferred
		err  error
	}
	pending, ok := async.OnMain(ctx, c.dispatch, func() pendingDraft {
		work, err := c.prepareAutomationDraft(ctx, automation.Request{Request: r.Text, Project: r.Project, Agent: r.Agent})
		return pendingDraft{work, err}
	})
	if !ok {
		return phoneAutomationFormFrame(r, nil, ctx.Err())
	}
	if pending.err != nil {
		return phoneAutomationFormFrame(r, nil, pending.err)
	}
	result, err := pending.work()
	v, _ := result.(automation.View)
	return phoneAutomationFormFrame(r, &v, err)
}

// Creation has its own operation: a phone can retry the same request after a
// lost reply, but cannot accidentally replace an existing desktop automation.
func (c *chatController) phoneAutomationCreate(r remote.Request) remote.Frame {
	if c.auto == nil {
		return phoneAutomationFormFrame(r, nil, errors.New("Automations require the Miya background service"))
	}
	key, err := hex.DecodeString(r.RequestID)
	if err != nil || len(key) != 8 {
		return phoneAutomationFormFrame(r, nil, errors.New("Invalid creation request. Start again."))
	}
	id := "phone-" + hex.EncodeToString(key)
	if i, exists := c.auto.Store.Find(id); exists {
		a := c.auto.Store.Automations[i]
		_, running := c.auto.Active[id]
		v := automation.ViewOf(a, c.auto.Store.Runs[id], running, c.auto.Now())
		return phoneAutomationFormFrame(r, &v, nil)
	}
	var a automation.Automation
	if json.Unmarshal(r.Automation, &a) != nil {
		return phoneAutomationFormFrame(r, nil, errors.New("No automation to save"))
	}
	a.ID, a.Created, a.Due = "", time.Time{}, time.Time{}
	a, err = c.auto.SaveWithID(a, id)
	if err != nil {
		return phoneAutomationFormFrame(r, nil, err)
	}
	v := automation.ViewOf(a, nil, false, c.auto.Now())
	return phoneAutomationFormFrame(r, &v, nil)
}
