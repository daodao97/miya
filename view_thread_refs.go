package main

import (
	"miya/internal/model"
)

type threadTag struct {
	Ref   ThreadRef
	Title string
}

func (app *chatApp) desktopReferenceTitle(ref ThreadRef) string {
	if app.referenceThread != nil {
		if t, ok := app.referenceThread(ref); ok {
			return threadRefTitle(t)
		}
	}
	if app.c != nil && ref.Source == app.c.referenceSource {
		if saved, ok := app.c.forkedReferences[ref]; ok {
			return saved.Title
		}
		for _, project := range app.c.threadRefProjects() {
			for _, t := range project.Threads {
				if t.ServerID == ref.SessionID && model.ThreadAgent(t) == ref.Backend {
					return threadRefTitle(t)
				}
			}
		}
	}
	return tr("Session unavailable")
}

// Tokens retain their identities; history never reconstructs them from titles.
