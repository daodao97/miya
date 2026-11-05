package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/model"
	"miya/internal/remote"
	"miya/internal/claude"
)

type mobileCollaborationCall struct {
	done  func(json.RawMessage, error)
	timer *time.Timer
}

func (m *mobileApp) collaborationCall(request collaborationRequest, done func(json.RawMessage, error)) {
	if m.conn == nil || !m.connected {
		done(nil, errors.New(tr("Not connected to desktop")))
		return
	}
	if m.state.Collaboration == nil || m.state.Collaboration.Version < 1 {
		done(nil, errors.New(tr("Update the Miya background service to use Agent tasks")))
		return
	}
	if m.collaborationCalls == nil {
		m.collaborationCalls = map[string]*mobileCollaborationCall{}
	}
	id := claude.NewSessionID()
	pending := &mobileCollaborationCall{done: done}
	m.collaborationCalls[id] = pending
	raw, _ := json.Marshal(request)
	conn, gen := m.conn, m.generation
	dispatch := runOnMain
	complete := func(err error) {
		dispatch(func() {
			if m.generation != gen {
				return
			}
			if callback := m.collaborationCalls[id]; callback != nil {
				delete(m.collaborationCalls, id)
				callback.timer.Stop()
				callback.done(nil, err)
			}
		})
	}
	pending.timer = time.AfterFunc(20*time.Second, func() { complete(errors.New(tr("Task request timed out; retry uses the same request ID"))) })
	go func() {
		if err := conn.Send(remote.Request{Op: "collaboration", RequestID: id, Collaboration: raw}); err != nil {
			complete(err)
		}
	}()
}

func (m *mobileApp) receiveCollaboration(raw json.RawMessage, message string) {
	var response struct {
		RequestID string          `json:"request_id"`
		Result    json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	callback := m.collaborationCalls[response.RequestID]
	if callback == nil {
		return
	}
	delete(m.collaborationCalls, response.RequestID)
	callback.timer.Stop()
	if message != "" {
		callback.done(nil, errors.New(message))
	} else {
		callback.done(response.Result, nil)
	}
}

func (m *mobileApp) interruptCollaborationRequests() {
	callbacks := m.collaborationCalls
	m.collaborationCalls = nil
	for _, callback := range callbacks {
		callback.timer.Stop()
		callback.done(nil, errors.New(tr("Disconnected · Task results remain available; retry uses the original request")))
	}
}

func (m *mobileApp) collaborationPane() (*collaborationPanel, collaborationPanelView, bool) {
	capability := m.state.Collaboration
	if capability == nil || capability.Version < 1 {
		return nil, collaborationPanelView{}, false
	}
	t := m.view.c.current()
	from := ThreadRef{Source: m.state.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}.URL()
	p := m.pal
	// Running and completed turns share the same status row and task toggle.
	v := collaborationPanelView{palette: taskPalette{p.surface, p.line, p.ink, p.muted}, capability: capability, from: from, readOnly: t.ReadOnly, connected: m.connected, call: m.collaborationCall, openThread: m.openCollaborationThread, trailing: true, toggleElsewhere: m.view.turnStatusVisible(t)}
	return m.collaborations.pane(from), v, true
}

func (m *mobileApp) collaborationTasks(c *ui.Context) {
	if pane, v, ok := m.collaborationPane(); ok {
		pane.draw(c, v)
	}
}

// collaborationToggle is the turn status row's accessory on the phone.
func (m *mobileApp) collaborationToggle(c *ui.Context) {
	if pane, v, ok := m.collaborationPane(); ok && v.toggleElsewhere && !pane.hidden() {
		v.compactToggle = c.Root().Bounds().W < 360 || m.textScale > 1.3
		ui.Row(c).Shrink(0).Margin(0, -8, 0, 0).Children(func() { pane.drawToggle(c, v) })
	}
}

func (m *mobileApp) openCollaborationThread(raw string) {
	ref, ok := parseThreadURL(raw)
	if !ok || ref.Source != m.state.ReferenceSource {
		m.sendErr = tr("Conversation unavailable")
		return
	}
	if thread, _, found := m.referencedThread(ref); found {
		m.openThread(thread)
		return
	}
	m.sendErr = tr("Conversation unavailable")
}

// Resolve the complete backend identity across projects. Numeric sidebar IDs
// may be reused after a reconnect and must not choose the destination.
func (m *mobileApp) referencedThread(ref ThreadRef) (Thread, phoneProject, bool) {
	if !ref.Valid() || ref.Source != m.state.ReferenceSource {
		return Thread{}, phoneProject{}, false
	}
	for _, project := range m.state.Projects {
		for _, thread := range project.Threads {
			if model.ThreadAgent(thread) == ref.Backend && thread.ServerID == ref.SessionID {
				return thread, project, true
			}
		}
	}
	return Thread{}, phoneProject{}, false
}
