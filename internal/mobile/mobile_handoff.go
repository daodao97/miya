package mobile

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/remote"
)

func (m *App) handoffToNewThread() {
	if m.Sending {
		m.SendErr = tr("A message is being sent. Wait before handing off.")
		return
	}
	source := m.Viewer.Current()
	ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(source), SessionID: source.ServerID}
	if source.ID == 0 || !ref.Valid() {
		m.SendErr = tr("Available once the session is created")
		return
	}
	project := ""
	for _, p := range m.State.Projects {
		for _, t := range p.Threads {
			if t.ServerID == ref.SessionID && model.ThreadAgent(t) == ref.Backend {
				project = p.Path
				break
			}
		}
		if project != "" {
			break
		}
	}
	if project == "" {
		m.SendErr = tr("Source project not found. Reopen the conversation.")
		return
	}
	// Carry an already-written task and its photos into the new draft. Detach
	// the photos before resetting the composer so their cache files survive.
	draft, images := m.Draft, m.Images
	m.Images = nil
	m.StartConversation(project, ref.Backend)
	m.Draft = model.ReferenceText(ref, appmodel.ThreadRefTitle(source)) + " " + draft
	m.Images = images
	if m.Conn != nil {
		m.sendPhoneAction(remote.Request{Op: "prefetch", Reference: ref.URL()})
	}
}
