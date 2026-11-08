package controller

import (
	"errors"
	"path/filepath"
	"strings"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	threadlist "miya/internal/chat/history"
	"miya/internal/chat/model"
	"miya/internal/contacts"
)

func (c *Controller) contactStore() *contacts.Store {
	if c.contacts == nil {
		c.contacts = contacts.New(filepath.Join(appdir.Dir(), "contacts"))
	}
	return c.contacts
}
func (c *Controller) ContactSnapshot() contacts.Snapshot {
	if c.Remote != nil || c.Mirror != nil {
		return c.contactSnapshot
	}
	return c.contactStore().Snapshot().ForClient()
}
func (c *Controller) ContactOp(r contacts.Request) (appmodel.ContactResult, error) {
	if c.Remote != nil {
		var out appmodel.ContactResult
		if c.contactSnapshot.Version < contacts.Version {
			return out, errors.New("Update the Miya background service to use contacts")
		}
		err := c.Remote.Call("contacts", r, &out)
		return out, err
	}
	store := c.contactStore()
	out := appmodel.ContactResult{RequestID: r.ID}
	if r.Op == "avatar" {
		var err error
		out.Avatar, err = store.Avatar(r.Profile.Avatar)
		return out, err
	}
	if r.Op == "save" {
		if _, ok := agentmeta.Lookup(r.Profile.Agent); !ok {
			return out, errors.New("Unknown execution engine")
		}
	}
	if r.Op == "open" && r.Context == "project" {
		found := false
		for _, p := range c.Projects() {
			if p.Path == r.Project {
				found = true
				break
			}
		}
		if !found {
			return out, errors.New("Select an existing project")
		}
	}
	snapshot, session, err := store.Apply(r)
	if err != nil {
		return out, err
	}
	out.State = snapshot.ForClient()
	out.ProfileID = store.ProfileForRequest(r.ID)
	if (r.Op == "save" || r.Op == "open") && c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
	if session != nil {
		out.Thread, out.Workspace = model.ListThread(c.ensureContactThread(*session)), session.Workspace
	}
	return out, nil
}
func (c *Controller) ensureContactThread(s contacts.Session) model.Thread {
	for _, t := range c.AllThreads() {
		if t.Contact != nil && t.Contact.ID == s.ID || s.NativeID != "" && model.ThreadAgent(t) == s.Agent && t.ServerID == s.NativeID && c.ThreadWorkspace(t.ID) == s.Workspace {
			c.Update(t.ID, func(t *model.Thread) {
				t.Contact = &s
				if (t.ServerID == "" || !s.NativeReady && s.Agent == agentmeta.Claude) && !t.Busy {
					t.Loaded, t.HistoryLoaded = true, true
				}
			})
			t = c.ThreadWithID(t.ID)
			return t
		}
	}
	t := model.Thread{ID: c.NextID(), Contact: &s, Agent: s.Agent, ServerID: s.NativeID, Title: s.Title, Project: filepath.Base(s.Workspace), LaunchModel: s.Model, LaunchEffort: s.Effort, LastActivityAt: s.LastActivity, HistoryLoaded: s.NativeID == "" || !s.NativeReady && s.Agent == agentmeta.Claude, Loaded: s.NativeID == "" || !s.NativeReady && s.Agent == agentmeta.Claude, Resumed: s.NativeReady && s.Agent == agentmeta.Claude}
	if t.Title == "" {
		t.Title = s.Name
	}
	if c.ProjectCache == nil {
		c.ProjectCache = map[string][]model.Thread{}
	}
	c.AddThread(s.Workspace, t)
	if s.Workspace == c.Cwd {
		c.ProjectCache[c.Cwd] = append([]model.Thread(nil), c.Threads()...)
	}
	threadlist.Persist(c.ProjectCache)
	return t
}
func (c *Controller) restoreContactThreads() {
	for _, s := range c.contactStore().Snapshot().Sessions {
		if !s.Hidden {
			c.ensureContactThread(s)
		}
	}
}
func (c *Controller) syncContact(t model.Thread, hidden bool) {
	if t.Contact == nil || c.Remote != nil || c.Mirror != nil {
		return
	}
	preview := t.Contact.Preview
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
			v := []rune(model.CleanUserText(m.Content))
			if len(v) > 120 {
				v = v[:120]
			}
			preview = string(v)
			break
		}
	}
	if err := c.contactStore().Sync(t.Contact.ID, t.ServerID, t.Resumed, t.Title, preview, t.LastActivityAt, hidden); err != nil {
		c.SetStatus("Unable to save contact conversation: " + err.Error())
	}
}

// AdoptContactResult updates a window from an acknowledged operation before
// the next service publication, without starting a second writer.
func (c *Controller) AdoptContactResult(r appmodel.ContactResult) {
	if r.State.Revision >= c.contactSnapshot.Revision {
		c.contactSnapshot = r.State
	}
	if r.Thread.ID == 0 {
		return
	}
	items := c.ProjectCache[r.Workspace]
	if r.Workspace == c.Cwd {
		items = c.Threads()
	}
	found := false
	for _, t := range items {
		if t.ID == r.Thread.ID {
			found = true
			break
		}
	}
	if !found {
		items = append([]model.Thread{r.Thread}, items...)
	}
	c.ProjectCache[r.Workspace] = items
	if r.Workspace == c.Cwd {
		c.SetThreads(items)
	}
}
func (c *Controller) isContactWorkspace(path string) bool {
	if c.Remote != nil || c.Mirror != nil {
		for _, s := range c.contactSnapshot.Sessions {
			if s.Daily && s.Workspace == path {
				return true
			}
		}
		return false
	}
	return c.contactStore().IsDaily(path)
}

func (c *Controller) setContactChoice(id int, modelID, effort string) {
	if c.Remote != nil {
		c.forward("contactChoice", struct {
			Thread        int
			Model, Effort string
		}{id, modelID, effort})
	}
	if t := c.ThreadWithID(id); t.Contact != nil && c.Remote == nil && c.Mirror == nil {
		if err := c.contactStore().SetChoices(t.Contact.ID, modelID, effort); err != nil {
			c.SetStatus(err.Error())
			return
		}
	}
	c.Update(id, func(t *model.Thread) {
		if t.Contact != nil {
			t.LaunchModel, t.LaunchEffort = modelID, effort
		}
	})
}

func (c *Controller) AgentModels(id string) []agent.ModelOption {
	return c.BackendFor(id).ModelOptions()
}
