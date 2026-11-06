package desktopui

import (
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
)

// Conversation choices carry their identity separately from their label.
func (app *App) ThreadChoices(query string) []appmodel.ComposerChoice {
	var out []appmodel.ComposerChoice
	for _, match := range appmodel.MatchingThreadRefs(app.C.ThreadRefProjects(), app.C.State().Active(), query) {
		t := match.Thread
		ref := app.C.ThreadRef(t)
		if !ref.Valid() {
			continue
		}
		out = append(out, appmodel.ComposerChoice{Name: appmodel.ThreadRefTitle(t), Detail: tr("Conversation · ") + model.AgentName(model.ThreadAgent(t)) + " · " + match.ProjectName, Ref: &ref})
	}
	return out
}

func (app *App) HandoffToNewThread() {
	c := app.C
	t := c.Current()
	ref := c.ThreadRef(t)
	if !ref.Valid() {
		app.Notice = tr("Available once the session is created")
		return
	}
	c.NewThread()
	c.RememberDraft(model.ReferenceText(ref, appmodel.ThreadRefTitle(t)) + " ")
	c.PrefetchReference(ref)
	app.Notice = tr("Referenced conversation \"") + appmodel.ThreadRefTitle(t) + tr("\": choose an agent, describe the task, and send")
}
