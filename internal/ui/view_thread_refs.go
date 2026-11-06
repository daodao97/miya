package desktopui

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
)

func (app *App) desktopReferenceTitle(ref model.ThreadRef) string {
	if app.ReferenceThread != nil {
		if t, ok := app.ReferenceThread(ref); ok {
			return appmodel.ThreadRefTitle(t)
		}
	}
	if app.C != nil && ref.Source == app.C.State().ReferenceSource {
		if title, ok := app.C.SavedReferenceTitle(ref); ok {
			return title
		}
		for _, project := range app.C.ThreadRefProjects() {
			for _, t := range project.Threads {
				if t.ServerID == ref.SessionID && model.ThreadAgent(t) == ref.Backend {
					return appmodel.ThreadRefTitle(t)
				}
			}
		}
	}
	return tr("Session unavailable")
}
