package protocol

import (
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/textutil"
)

// AutomationRunThread prefers durable session identity over reusable sidebar IDs.
func AutomationRunThread(projects []model.ProjectSnapshot, defaultAgent string, v automation.View, r automation.Run) (model.Thread, bool) {
	workspace := automation.Workspace(v.Project, r)
	for _, p := range projects {
		if p.Path != workspace {
			continue
		}
		for _, t := range p.Threads {
			if model.ThreadAgent(t) != textutil.FirstNonEmpty(v.Agent, defaultAgent) {
				continue
			}
			if r.ServerID != "" && t.ServerID == r.ServerID || r.ServerID == "" && r.Thread != 0 && t.ID == r.Thread {
				return t, true
			}
		}
	}
	return model.Thread{}, false
}
