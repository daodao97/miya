package main

import (
	"encoding/json"
	"errors"

	"github.com/egoist/mygo/ui"
)

func (app *chatApp) collaborationCall(request collaborationRequest, done func(json.RawMessage, error)) {
	c := app.c
	if c.remote != nil {
		client, source := c.remote, c.referenceSource
		go func() {
			var raw json.RawMessage
			err := client.call("collaboration", request, &raw)
			c.dispatch(func() {
				if c.referenceSource != source {
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
	value, err := c.collaborationUserOp(request)
	if work, ok := value.(deferred); ok && err == nil {
		go func() { value, err := work(); c.dispatch(func() { finish(value, err) }) }()
		return
	}
	finish(value, err)
}

func (app *chatApp) collaborationTasks(c *ui.Context, t Thread) {
	capability := app.c.collaborationCapabilities()
	if capability == nil || capability.Version < 1 {
		return
	}
	from := app.c.threadRef(t).URL()
	pane := app.collaborations.pane(from)
	pane.draw(c, collaborationPanelView{palette: taskPalette{panel, line, ink, muted}, capability: capability, from: from, workspace: app.c.threadWorkspace(t.ID), agent: threadAgent(t), readOnly: t.ReadOnly, connected: true, call: app.collaborationCall, openThread: app.openCollaborationThread})
}

func (app *chatApp) openCollaborationThread(raw string) {
	ref, ok := parseThreadURL(raw)
	if !ok || ref.Source != app.c.referenceSource {
		app.notice = tr("Conversation unavailable")
		return
	}
	for _, thread := range app.c.allThreads() {
		if app.c.threadRef(thread) != ref {
			continue
		}
		project := app.c.threadWorkspace(thread.ID)
		if project != app.c.cwd {
			if err := app.c.changeWorkspace(project); err != nil {
				app.notice = err.Error()
				return
			}
		}
		app.section = ""
		app.c.selectThread(thread)
		return
	}
	app.notice = tr("Conversation unavailable")
}
