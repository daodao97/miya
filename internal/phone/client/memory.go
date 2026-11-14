package client

import (
	"encoding/json"
	"time"

	"miya/internal/memory/manager"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type Memory struct {
	Project, Scope string
	List           manager.List
	Loading        bool
	RequestedAt    time.Time
	Revision       uint64
	Error          string
}

func (m *Client) LoadMemory() {
	state := &m.Memories
	if state.Loading || !m.Connected || !m.CanSend() || !m.State.Memory {
		return
	}
	state.Loading = true
	state.RequestedAt = time.Now()
	conn, gen := m.Conn, m.Generation
	project, scope := state.Project, state.Scope
	requestedAt := state.RequestedAt
	go func() {
		if err := m.write(conn, remote.Request{Op: "memory", Project: project, MemoryScope: scope}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && state.Project == project && state.Scope == scope && state.RequestedAt == requestedAt {
					state.Loading = false
					state.Error = err.Error()
				}
			})
		}
	}()
}

func (m *Client) ReceiveMemory(raw json.RawMessage, message string) {
	var result phoneproto.MemoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		m.Memories.Loading = false
		m.Memories.Error = tr("Failed to load memory")
		return
	}
	state := &m.Memories
	if result.Project != state.Project || result.Scope != state.Scope {
		return
	}
	state.Loading = false
	if result.Revision < m.State.MemoryRevision {
		state.RequestedAt = time.Time{}
		return
	}
	if message != "" {
		state.Error = message
		return
	}
	state.List = result.List
	state.Revision = result.Revision
	state.Error = ""
}
