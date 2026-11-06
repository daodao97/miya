package model

// ProjectSnapshot is a workspace and its conversation metadata, shared by
// desktop search and remote clients. JSON tags preserve the phone wire format.
type ProjectSnapshot struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Current bool     `json:"current"`
	Threads []Thread `json:"threads"`
}

// ListThread is a conversation as the list shows it: no messages, and never
// the desktop's unsent draft.
func ListThread(t Thread) Thread {
	t.Messages = nil
	t.Draft = ""
	return t
}
