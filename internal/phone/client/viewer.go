package client

import "miya/internal/chat/model"

// Viewer holds only the selected snapshot. It never starts an agent and
// depends on neither view framework. Changed is called on its owner's goroutine.
type Viewer struct {
	threads          []model.Thread
	active, revision int
	Changed          func()
}

func NewViewer() *Viewer { return &Viewer{} }
func (v *Viewer) notify() {
	if v.Changed != nil {
		v.Changed()
	}
}
func (v *Viewer) SetThreads(t []model.Thread) { v.threads = t; v.notify() }
func (v *Viewer) SetActive(id int)            { v.active = id; v.notify() }
func (v *Viewer) Revision() int               { return v.revision }
func (v *Viewer) setRevision(n int)           { v.revision = n; v.notify() }
func (v *Viewer) Current() model.Thread {
	for _, t := range v.threads {
		if t.ID == v.active {
			return t
		}
	}
	return model.Thread{}
}
