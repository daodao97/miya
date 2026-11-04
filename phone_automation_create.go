package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"miya/internal/remote"
)

type phoneAutomationFormResult struct {
	RequestID string          `json:"requestId"`
	View      *automationView `json:"view,omitempty"`
}

func phoneAutomationFormFrame(r remote.Request, view *automationView, err error) remote.Frame {
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
	pending, ok := onMain(ctx, c.dispatch, func() pendingDraft {
		work, err := c.prepareAutomationDraft(ctx, automationRequest{Request: r.Text, Project: r.Project, Agent: r.Agent})
		return pendingDraft{work, err}
	})
	if !ok {
		return phoneAutomationFormFrame(r, nil, ctx.Err())
	}
	if pending.err != nil {
		return phoneAutomationFormFrame(r, nil, pending.err)
	}
	result, err := pending.work()
	v, _ := result.(automationView)
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
	if i, exists := c.auto.store.find(id); exists {
		a := c.auto.store.Automations[i]
		_, running := c.auto.active[id]
		v := automationViewOf(a, c.auto.store.Runs[id], running, c.auto.now())
		return phoneAutomationFormFrame(r, &v, nil)
	}
	var a Automation
	if json.Unmarshal(r.Automation, &a) != nil {
		return phoneAutomationFormFrame(r, nil, errors.New("No automation to save"))
	}
	a.ID, a.Created, a.Due = "", time.Time{}, time.Time{}
	a, err = c.saveAutomationWithCreateID(a, id)
	if err != nil {
		return phoneAutomationFormFrame(r, nil, err)
	}
	v := automationViewOf(a, nil, false, c.auto.now())
	return phoneAutomationFormFrame(r, &v, nil)
}
