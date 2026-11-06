package mobile

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

type MobileCollaborationCall struct {
	Done  func(json.RawMessage, error)
	Timer *time.Timer
}

func (m *App) collaborationCall(request appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
	if m.Conn == nil || !m.Connected {
		done(nil, errors.New(tr("Not connected to desktop")))
		return
	}
	if m.State.Collaboration == nil || m.State.Collaboration.Version < 1 {
		done(nil, errors.New(tr("Update the Miya background service to use Agent tasks")))
		return
	}
	if m.CollaborationCalls == nil {
		m.CollaborationCalls = map[string]*MobileCollaborationCall{}
	}
	id := textutil.NewID()
	pending := &MobileCollaborationCall{Done: done}
	m.CollaborationCalls[id] = pending
	raw, _ := json.Marshal(request)
	conn, gen := m.Conn, m.Generation
	dispatch := uiruntime.RunOnMain
	complete := func(err error) {
		dispatch(func() {
			if m.Generation != gen {
				return
			}
			if callback := m.CollaborationCalls[id]; callback != nil {
				delete(m.CollaborationCalls, id)
				callback.Timer.Stop()
				callback.Done(nil, err)
			}
		})
	}
	pending.Timer = time.AfterFunc(20*time.Second, func() { complete(errors.New(tr("Task request timed out; retry uses the same request ID"))) })
	go func() {
		if err := conn.Send(remote.Request{Op: "collaboration", RequestID: id, Collaboration: raw}); err != nil {
			complete(err)
		}
	}()
}

func (m *App) ReceiveCollaboration(raw json.RawMessage, message string) {
	var response struct {
		RequestID string          `json:"request_id"`
		Result    json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return
	}
	callback := m.CollaborationCalls[response.RequestID]
	if callback == nil {
		return
	}
	delete(m.CollaborationCalls, response.RequestID)
	callback.Timer.Stop()
	if message != "" {
		callback.Done(nil, errors.New(message))
	} else {
		callback.Done(response.Result, nil)
	}
}

func (m *App) InterruptCollaborationRequests() {
	callbacks := m.CollaborationCalls
	m.CollaborationCalls = nil
	for _, callback := range callbacks {
		callback.Timer.Stop()
		callback.Done(nil, errors.New(tr("Disconnected · Task results remain available; retry uses the original request")))
	}
}

func (m *App) CollaborationPane() (*widgets.CollaborationPanel, widgets.CollaborationPanelView, bool) {
	capability := m.State.Collaboration
	if capability == nil || capability.Version < 1 {
		return nil, widgets.CollaborationPanelView{}, false
	}
	t := m.Viewer.Current()
	from := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}.URL()
	p := m.Pal
	// Running and completed turns share the same status row and task toggle.
	v := widgets.CollaborationPanelView{Palette: widgets.TaskPalette{Surface: p.surface, Border: p.line, Ink: p.ink, Muted: p.muted}, Capability: capability, From: from, ReadOnly: t.ReadOnly, Connected: m.Connected, Call: m.collaborationCall, OpenThread: m.openCollaborationThread, Trailing: true, ToggleElsewhere: m.View.TurnStatusVisible(t)}
	return m.Collaborations.Pane(from), v, true
}

func (m *App) collaborationTasks(c *ui.Context) {
	if pane, v, ok := m.CollaborationPane(); ok {
		pane.Draw(c, v)
	}
}

// collaborationToggle is the turn status row's accessory on the phone.
func (m *App) collaborationToggle(c *ui.Context) {
	if pane, v, ok := m.CollaborationPane(); ok && v.ToggleElsewhere && !pane.Hidden() {
		v.CompactToggle = c.Root().Bounds().W < 360 || m.textScale > 1.3
		ui.Row(c).Shrink(0).Margin(0, -8, 0, 0).Children(func() { pane.DrawToggle(c, v) })
	}
}

func (m *App) openCollaborationThread(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok || ref.Source != m.State.ReferenceSource {
		m.SendErr = tr("Conversation unavailable")
		return
	}
	if thread, _, found := m.referencedThread(ref); found {
		m.OpenThread(thread)
		return
	}
	m.SendErr = tr("Conversation unavailable")
}

// Resolve the complete backend identity across projects. Numeric sidebar IDs
// may be reused after a reconnect and must not choose the destination.
func (m *App) referencedThread(ref model.ThreadRef) (model.Thread, phoneproto.Project, bool) {
	if !ref.Valid() || ref.Source != m.State.ReferenceSource {
		return model.Thread{}, phoneproto.Project{}, false
	}
	for _, project := range m.State.Projects {
		for _, thread := range project.Threads {
			if model.ThreadAgent(thread) == ref.Backend && thread.ServerID == ref.SessionID {
				return thread, project, true
			}
		}
	}
	return model.Thread{}, phoneproto.Project{}, false
}
