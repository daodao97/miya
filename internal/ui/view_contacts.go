package desktopui

import (
	"errors"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"net/url"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/thumbnail"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

func (app *App) contactCall(r contacts.Request, done func(appmodel.ContactResult, error)) {
	if cl := app.C.State().Remote; cl != nil {
		go func() {
			var out appmodel.ContactResult
			err := cl.Call("contacts", r, &out)
			app.C.Dispatch(func() {
				if app.C.State().Remote != cl {
					done(out, errors.New("Connection changed. Retry to check the saved result."))
					return
				}
				if err == nil && r.Op != "avatar" {
					app.C.AdoptContactResult(out)
				}
				done(out, err)
			})
		}()
		return
	}
	out, err := app.C.ContactOp(r)
	done(out, err)
}

func (app *App) contactView() widgets.ContactView {
	v := widgets.ContactView{Snapshot: app.C.ContactSnapshot(), Threads: app.C.AllThreads(), Unread: app.C.DesktopThreadUnread, Connected: true, Call: app.contactCall, Ink: widgets.Ink, Muted: widgets.Muted, Border: widgets.Line, Surface: widgets.Panel}
	v.Source = app.C.State().ReferenceSource
	if cl := app.C.State().Remote; cl != nil {
		select {
		case <-cl.Done():
			v.Connected = false
		default:
		}
	}
	for _, a := range app.C.Agents() {
		v.Engines = append(v.Engines, widgets.ContactEngine{ID: a.ID, Name: a.Name, Models: app.C.AgentModels(a.ID)})
	}
	for _, project := range app.C.State().Projects() {
		v.Projects = append(v.Projects, model.ProjectSnapshot{Name: project.Name, Path: project.Path})
	}
	v.Open = func(out appmodel.ContactResult) { app.C.AdoptContactResult(out); app.SelectThread(out.Thread) }
	v.PickAvatar = func(done func([]byte, error)) {
		parent := uiruntime.CurrentWindow()
		go func() {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: tr("Choose photo")})
			var data []byte
			if err == nil && len(paths) > 0 {
				data, err = thumbnail.File((&url.URL{Scheme: "file", Path: paths[0]}).String())
			}
			app.C.Dispatch(func() { done(data, err) })
		}()
	}
	return v
}

func (app *App) ShowContacts() {
	app.Contacts.Reopen()
	app.Section = "Contacts"
	app.Contacts.Detail = ""
	app.Contacts.Err = ""
}
func (app *App) contactsView(c *ui.Context) { app.Contacts.View(c, app.contactView()) }
func (app *App) contactBar(c *ui.Context) {
	t := app.C.Current()
	app.Contacts.Bar(c, t, app.contactView(), func() { app.Section = "Contacts"; app.Contacts.Detail = t.Contact.BotID })
}

func (app *App) contactSidebar(c *ui.Context) {
	v := app.contactView()
	if len(v.Snapshot.Profiles) == 0 {
		return
	}
	ui.Column(c).PaddingX(12).Margin(12, 0, 6, 0).Gap(2).Children(func() {
		for _, profile := range v.Snapshot.OrderedProfiles() {
			if profile.Archived {
				continue
			}
			row := widgets.Quiet(c.Key("bot:"+profile.ID)).FillWidth().MinWidth(0).MinHeight(40).Padding(4, 8).Gap(9).Label(tr("Contact ") + profile.Name).Disabled(app.Contacts.Busy || !v.Connected)
			row.Children(func() {
				app.Contacts.Avatar(c, profile, 26, v)
				ui.Text(c, profile.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(13)
				busy, unread := false, false
				for _, thread := range v.Threads {
					if thread.Contact != nil && thread.Contact.BotID == profile.ID {
						busy = busy || thread.Busy
						unread = unread || app.C.DesktopThreadUnread(thread)
					}
				}
				if busy {
					ui.Spinner(c).Size(12, 12)
				} else if unread {
					ui.Box(c).Size(6, 6).Radius(3).Background(widgets.Added)
				}
			})
			if row.Clicked() {
				app.Contacts.Submit(contacts.Request{Op: "open", BotID: profile.ID, Context: "recent"}, v)
			}
		}
	})
}
