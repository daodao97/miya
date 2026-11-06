package controller

import (
	context "context"
	hex "encoding/hex"
	json "encoding/json"
	errors "errors"
	appmodel "miya/internal/appmodel"
	async "miya/internal/async"
	automation "miya/internal/automation"
	remote "miya/internal/remote"
	time "time"
)

func phoneAutomationFormFrame(r remote.Request, view *automation.View, err error) remote.Frame {
	data, _ := json.Marshal(appmodel.PhoneAutomationFormResult{RequestID: r.RequestID, View: view})
	f := remote.Frame{Type: r.Op, Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	return f
}

func (c *Controller) phoneAutomationDraft(ctx context.Context, r remote.Request) remote.Frame {
	type pendingDraft struct {
		work appmodel.Deferred
		err  error
	}
	pending, ok := async.OnMain(ctx, c.Dispatch, func() pendingDraft {
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
func (c *Controller) PhoneAutomationCreate(r remote.Request) remote.Frame {
	if c.Auto == nil {
		return phoneAutomationFormFrame(r, nil, errors.New("Automations require the Miya background service"))
	}
	key, err := hex.DecodeString(r.RequestID)
	if err != nil || len(key) != 8 {
		return phoneAutomationFormFrame(r, nil, errors.New("Invalid creation request. Start again."))
	}
	id := "phone-" + hex.EncodeToString(key)
	if i, exists := c.Auto.Store.Find(id); exists {
		a := c.Auto.Store.Automations[i]
		_, running := c.Auto.Active[id]
		v := automation.ViewOf(a, c.Auto.Store.Runs[id], running, c.Auto.Now())
		return phoneAutomationFormFrame(r, &v, nil)
	}
	var a automation.Automation
	if json.Unmarshal(r.Automation, &a) != nil {
		return phoneAutomationFormFrame(r, nil, errors.New("No automation to save"))
	}
	a.ID, a.Created, a.Due = "", time.Time{}, time.Time{}
	a, err = c.Auto.SaveWithID(a, id)
	if err != nil {
		return phoneAutomationFormFrame(r, nil, err)
	}
	v := automation.ViewOf(a, nil, false, c.Auto.Now())
	return phoneAutomationFormFrame(r, &v, nil)
}
