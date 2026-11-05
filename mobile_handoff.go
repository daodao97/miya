package main

import "miya/internal/remote"

func (m *mobileApp) handoffToNewThread() {
	if m.sending {
		m.sendErr = tr("A message is being sent. Wait before handing off.")
		return
	}
	source := m.view.c.current()
	ref := ThreadRef{Source: m.state.ReferenceSource, Backend: threadAgent(source), SessionID: source.ServerID}
	if source.ID == 0 || !ref.Valid() {
		m.sendErr = tr("Available once the session is created")
		return
	}
	project := ""
	for _, p := range m.state.Projects {
		for _, t := range p.Threads {
			if t.ServerID == ref.SessionID && threadAgent(t) == ref.Backend {
				project = p.Path
				break
			}
		}
		if project != "" {
			break
		}
	}
	if project == "" {
		m.sendErr = tr("Source project not found. Reopen the conversation.")
		return
	}
	// Carry an already-written task and its photos into the new draft. Detach
	// the photos before resetting the composer so their cache files survive.
	draft, images := m.draft, m.images
	m.images = nil
	m.startConversation(project, ref.Backend)
	m.draft = referenceText(ref, threadRefTitle(source)) + " " + draft
	m.images = images
	if m.conn != nil {
		m.sendPhoneAction(remote.Request{Op: "prefetch", Reference: ref.URL()})
	}
}
