package client

import (
	"encoding/json"
	"errors"
	"time"

	"miya/internal/delegation"
	"miya/internal/remote"
	"miya/internal/textutil"
)

type CollaborationCall struct {
	Done  func(json.RawMessage, error)
	Timer *time.Timer
}

func (m *Client) CallCollaboration(request delegation.UserRequest, done func(json.RawMessage, error)) {
	if !m.CanSend() || !m.Connected {
		done(nil, errors.New(tr("Not connected to desktop")))
		return
	}
	if m.State.Collaboration == nil || m.State.Collaboration.Version < 1 {
		done(nil, errors.New(tr("Update the Miya background service to use Agent tasks")))
		return
	}
	if m.CollaborationCalls == nil {
		m.CollaborationCalls = map[string]*CollaborationCall{}
	}
	id := textutil.NewID()
	pending := &CollaborationCall{Done: done}
	m.CollaborationCalls[id] = pending
	raw, _ := json.Marshal(request)
	conn, gen := m.Conn, m.Generation
	dispatch := m.post
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
		if err := m.write(conn, remote.Request{Op: "collaboration", RequestID: id, Collaboration: raw}); err != nil {
			complete(err)
		}
	}()
}

func (m *Client) ReceiveCollaboration(raw json.RawMessage, message string) {
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

func (m *Client) InterruptCollaborationRequests() {
	callbacks := m.CollaborationCalls
	m.CollaborationCalls = nil
	for _, callback := range callbacks {
		callback.Timer.Stop()
		callback.Done(nil, errors.New(tr("Disconnected · Task results remain available; retry uses the original request")))
	}
}
