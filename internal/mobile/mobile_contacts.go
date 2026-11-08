package mobile

import (
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"miya/internal/widgets"
)

type contactCall struct {
	done func(appmodel.ContactResult, error)
	at   time.Time
}

func (m *App) callContact(r contacts.Request, done func(appmodel.ContactResult, error)) {
	if !m.Connected || m.Conn == nil {
		done(appmodel.ContactResult{}, errors.New("Disconnected. Your edits are kept on this device."))
		return
	}
	if m.contactCalls == nil {
		m.contactCalls = map[string]contactCall{}
	}
	m.contactCalls[r.ID] = contactCall{done: done, at: time.Now()}
	data, err := json.Marshal(r)
	if err != nil {
		delete(m.contactCalls, r.ID)
		done(appmodel.ContactResult{}, err)
		return
	}
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(remote.Request{Op: "contacts", Contacts: data, RequestID: r.ID}); err != nil {
			m.onMain(gen, func() { m.finishContact(r.ID, appmodel.ContactResult{}, err) })
		}
	}()
	time.AfterFunc(20*time.Second, func() {
		m.onMain(gen, func() {
			if call, ok := m.contactCalls[r.ID]; ok && time.Since(call.at) >= 20*time.Second {
				m.finishContact(r.ID, appmodel.ContactResult{}, errors.New("Request timed out. Retry to check the saved result."))
			}
		})
	})
}

func (m *App) finishContact(id string, out appmodel.ContactResult, err error) {
	call, ok := m.contactCalls[id]
	if !ok {
		return
	}
	delete(m.contactCalls, id)
	if err == nil && out.State.Version > 0 {
		if out.State.Revision >= m.State.Contacts.Revision {
			m.State.Contacts = out.State
		}
		if out.Thread.ID != 0 {
			m.adoptContactThread(out)
		}
	}
	call.done(out, err)
}
func (m *App) ReceiveContacts(data json.RawMessage, message string) {
	var out appmodel.ContactResult
	if json.Unmarshal(data, &out) != nil {
		return
	}
	var err error
	if message != "" {
		err = errors.New(message)
	}
	m.finishContact(out.RequestID, out, err)
}
func (m *App) adoptContactThread(out appmodel.ContactResult) {
	projects := &m.State.Projects
	if out.Thread.Contact != nil && out.Thread.Contact.Daily {
		projects = &m.State.ContactThreads
	}
	for i := range *projects {
		if (*projects)[i].Path != out.Workspace {
			continue
		}
		for _, t := range (*projects)[i].Threads {
			if t.ID == out.Thread.ID {
				return
			}
		}
		(*projects)[i].Threads = append((*projects)[i].Threads, out.Thread)
		return
	}
	*projects = append(*projects, model.ProjectSnapshot{Name: "Daily chat", Path: out.Workspace, Threads: []model.Thread{out.Thread}})
}
func (m *App) allPhoneProjects() []model.ProjectSnapshot {
	return append(append([]model.ProjectSnapshot(nil), m.State.Projects...), m.State.ContactThreads...)
}
func (m *App) contactView() widgets.ContactView {
	v := widgets.ContactView{Snapshot: m.State.Contacts, Projects: m.State.Projects, Connected: m.Connected, Call: m.callContact, Unread: m.completionUnread, Ink: m.Pal.ink, Muted: m.Pal.muted, Border: m.Pal.line, Surface: m.Pal.surface}
	v.Source = m.State.ReferenceSource
	for _, p := range m.allPhoneProjects() {
		v.Threads = append(v.Threads, p.Threads...)
	}
	for _, a := range m.State.Agents {
		e := widgets.ContactEngine{ID: a.Agent, Name: model.AgentName(a.Agent)}
		for _, option := range a.Models {
			e.Models = append(e.Models, agent.ModelOption{Value: option.Value, Label: option.Label, Efforts: option.Efforts, DefaultEffort: option.DefaultEffort})
		}
		v.Engines = append(v.Engines, e)
	}
	v.Open = func(out appmodel.ContactResult) { m.adoptContactThread(out); m.OpenThread(out.Thread) }
	v.PickAvatar = func(done func([]byte, error)) {
		win, gen := m.win, m.Generation
		go func() {
			paths, err := mygo.Dialog.Photos(mygo.PhotoDialogOptions{Parent: win})
			var data []byte
			if err == nil && len(paths) > 0 {
				data, err = thumbnail.File((&url.URL{Scheme: "file", Path: paths[0]}).String())
			}
			m.onMain(gen, func() { done(data, err) })
		}()
	}
	return v
}
func (m *App) contactsPage(c *ui.Context) {
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/contacts")
		ui.Text(c, tr("Contacts")).FontSize(m.Font(16)).FontWeight(600)
	})
	ui.Scroll(c.Key("contacts-scroll")).Grow(1).MinHeight(0).TouchScroll().Children(func() { ui.Column(c).FillWidth().Padding(16).Children(func() { m.Contacts.View(c, m.contactView()) }) })
}
func (m *App) contactBar(c *ui.Context, t model.Thread) {
	m.Contacts.Bar(c, t, m.contactView(), func() { m.Contacts.Detail = t.Contact.BotID; m.OpenDrawerPage("/contacts") })
}

func (m *App) interruptContacts() {
	calls := m.contactCalls
	m.contactCalls = nil
	for _, call := range calls {
		call.done(appmodel.ContactResult{}, errors.New("Disconnected. Your edits are kept on this device."))
	}
}
