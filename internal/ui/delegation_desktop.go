package desktopui

import (
	json "encoding/json"
	errors "errors"
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
	widgets "miya/internal/widgets"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) CollaborationCall(request appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
	c := app.C
	if c.State().Remote != nil {
		client, source := c.State().Remote, c.State().ReferenceSource
		go func() {
			var raw json.RawMessage
			err := client.Call("collaboration", request, &raw)
			c.Dispatch(func() {
				if c.State().ReferenceSource != source {
					done(nil, errors.New(tr("The connected workspace changed; retry from the original workspace")))
					return
				}
				done(raw, err)
			})
		}()
		return
	}
	finish := func(value any, err error) {
		var raw json.RawMessage
		if err == nil {
			raw, err = json.Marshal(value)
		}
		done(raw, err)
	}
	value, err := c.CollaborationUserOp(request)
	if work, ok := value.(appmodel.Deferred); ok && err == nil {
		go func() { value, err := work(); c.Dispatch(func() { finish(value, err) }) }()
		return
	}
	finish(value, err)
}

func (app *App) CollaborationPane(t appmodel.Thread) (*widgets.CollaborationPanel, widgets.CollaborationPanelView, bool) {
	capability := app.C.CollaborationCapabilities()
	if capability == nil || capability.Version < 1 {
		return nil, widgets.CollaborationPanelView{}, false
	}
	from := app.C.ThreadRef(t).URL()
	// Running and completed turns share the same status row and task toggle.
	v := widgets.CollaborationPanelView{Palette: widgets.TaskPalette{widgets.Panel, widgets.Line, widgets.Ink, widgets.Muted}, Capability: capability, From: from, ReadOnly: t.ReadOnly, Connected: true, Call: app.CollaborationCall, OpenThread: app.openCollaborationThread, Trailing: true, ToggleElsewhere: app.TurnStatusVisible(t)}
	return app.Collaborations.Pane(from), v, true
}

func (app *App) collaborationTasks(c *ui.Context, t appmodel.Thread) {
	if pane, v, ok := app.CollaborationPane(t); ok {
		pane.Draw(c, v)
	}
}

// collaborationToggle is the turn status row's accessory on the desktop.
func (app *App) collaborationToggle(c *ui.Context, t appmodel.Thread) {
	if pane, v, ok := app.CollaborationPane(t); ok && v.ToggleElsewhere && !pane.Hidden() {
		ui.Row(c).Shrink(0).Margin(0, -8, 0, 0).Children(func() { pane.DrawToggle(c, v) })
	}
}

func (app *App) openCollaborationThread(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok || ref.Source != app.C.State().ReferenceSource {
		app.Notice = tr("Conversation unavailable")
		return
	}
	for _, thread := range app.C.AllThreads() {
		if app.C.ThreadRef(thread) != ref {
			continue
		}
		project := app.C.ThreadWorkspace(thread.ID)
		if project != app.C.State().Cwd {
			if err := app.C.ChangeWorkspace(project); err != nil {
				app.Notice = err.Error()
				return
			}
		}
		app.Collaborations.ClosePopovers()
		app.Section = ""
		app.C.SelectThread(thread)
		return
	}
	app.Notice = tr("Conversation unavailable")
}
