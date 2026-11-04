package main

import "strings"

type threadTag struct {
	Ref   ThreadRef
	Title string
}

func (app *chatApp) referenceTitle(ref ThreadRef) string {
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
				if t.ServerID == ref.SessionID && threadAgent(t) == ref.Backend {
					return threadRefTitle(t)
				}
			}
		}
	}
	return tr("Session unavailable")
}

// Tokens retain their identities; history never reconstructs them from titles.
func (app *chatApp) threadReferenceContent(text string) (string, []threadTag) {
	own, _ := splitReferenceContext(text)
	seen := map[ThreadRef]bool{}
	var tags []threadTag
	for _, m := range threadMentions(own) {
		if seen[m.Ref] {
			continue
		}
		seen[m.Ref] = true
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = app.referenceTitle(m.Ref)
		}
		tags = append(tags, threadTag{Ref: m.Ref, Title: title})
	}
	return own, tags
}
