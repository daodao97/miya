package widgets

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/textutil"
)

type ContactEngine struct {
	ID, Name string
	Models   []agent.ModelOption
}

type ContactView struct {
	Source                      string
	Snapshot                    contacts.Snapshot
	Engines                     []ContactEngine
	Projects                    []model.ProjectSnapshot
	Threads                     []model.Thread
	Unread                      func(model.Thread) bool
	Connected                   bool
	Call                        func(contacts.Request, func(appmodel.ContactResult, error))
	Open                        func(appmodel.ContactResult)
	PickAvatar                  func(func([]byte, error))
	Ink, Muted, Border, Surface ui.Color
}

// ContactUI owns device-local navigation and editing state. Its mutations
// always go through the connected background service.
type ContactUI struct {
	source                  string
	State                   contacts.Snapshot
	Detail, Query           string
	Archived, Editing, Busy bool
	Draft                   contacts.Profile
	AvatarData              []byte
	Err                     string
	pending                 *contacts.Request
	avatars                 map[string]ui.ImageSource
	avatarLoading           map[string]bool
}

func (p *ContactUI) Use(s contacts.Snapshot) {
	if s.Version > 0 && s.Revision >= p.State.Revision {
		p.State = s
	}
}

func (p *ContactUI) useSource(v ContactView) {
	if p.source != v.Source {
		*p = ContactUI{source: v.Source}
	}
	p.Use(v.Snapshot)
}

func (p *ContactUI) Edit(v contacts.Profile) {
	p.Draft, p.Editing, p.AvatarData, p.Err, p.pending = v, true, nil, "", nil
}

func (p *ContactUI) Reopen() { p.avatarLoading = map[string]bool{} }

func sameContactRequest(a, b contacts.Request) bool {
	a.ID, b.ID = "", ""
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (p *ContactUI) Submit(r contacts.Request, v ContactView) {
	p.useSource(v)
	if v.Snapshot.Version < contacts.Version {
		p.Err = tr("Update the Miya background service to use contacts")
		return
	}
	if p.Busy || !v.Connected || v.Call == nil {
		return
	}
	if p.pending != nil && sameContactRequest(*p.pending, r) {
		r.ID = p.pending.ID
	} else {
		r.ID = textutil.NewID()
	}
	p.pending, p.Busy, p.Err = &r, true, ""
	v.Call(r, func(out appmodel.ContactResult, err error) {
		if p.source != v.Source || p.pending == nil || p.pending.ID != r.ID {
			return
		}
		p.Busy = false
		if err != nil {
			p.Err = err.Error()
			return
		}
		p.Use(out.State)
		p.pending, p.Err = nil, ""
		if r.Op == "save" {
			p.Editing, p.AvatarData = false, nil
			p.Detail = r.Profile.ID
			if p.Detail == "" {
				p.Detail = out.ProfileID
			}
		}
		if r.Op == "open" && v.Open != nil {
			v.Open(out)
		}
	})
}

func contactButton(c *ui.Context, key, label string, disabled bool, f func()) {
	if Quiet(c.Key(key)).MinHeight(40).Padding(8, 12).Label(label).Disabled(disabled).Children(func() { ui.Text(c, label) }).Clicked() {
		f()
	}
}

func (p *ContactUI) Avatar(c *ui.Context, profile contacts.Profile, size float32, v ContactView) {
	p.useSource(v)
	if p.avatars == nil {
		p.avatars, p.avatarLoading = map[string]ui.ImageSource{}, map[string]bool{}
	}
	if p.avatarLoading == nil {
		p.avatarLoading = map[string]bool{}
	}
	if strings.HasPrefix(profile.Avatar, "image:") && p.avatars[profile.Avatar] == nil && !p.avatarLoading[profile.Avatar] && v.Connected && v.Call != nil {
		ref := profile.Avatar
		p.avatarLoading[ref] = true
		v.Call(contacts.Request{ID: textutil.NewID(), Op: "avatar", Profile: contacts.Profile{Avatar: ref}}, func(r appmodel.ContactResult, err error) {
			if p.source != v.Source || p.avatars == nil {
				return
			}
			if err == nil {
				p.avatars[ref], _ = ui.DecodeImage(r.Avatar)
			}
			// Failed loads are retried when the page is opened again.
		})
	}
	colors := []ui.Color{ui.Hex("#497866"), ui.Hex("#94654f"), ui.Hex("#58778b"), ui.Hex("#8c6b83")}
	h := uint32(0)
	for _, r := range profile.ID + profile.Name {
		h = h*31 + uint32(r)
	}
	ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.32).Clip().Background(colors[h%uint32(len(colors))]).Children(func() {
		if img := p.avatars[profile.Avatar]; img != nil {
			ui.Image(c, img).Size(size, size)
			return
		}
		glyph := map[string]string{"leaf": "message-circle", "book": "file-text", "code": "code"}[profile.Avatar]
		ui.Box(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if glyph != "" {
				Icon(c, glyph, size*0.48, ui.Hex("#ffffff"))
				return
			}
			name := []rune(profile.Name)
			initial := "?"
			if len(name) > 0 {
				initial = string(name[0])
			}
			ui.Text(c, initial).FontSize(size * 0.43).FontWeight(600).TextColor(ui.Hex("#ffffff"))
		})
	})
}

func (p *ContactUI) View(c *ui.Context, v ContactView) {
	p.useSource(v)
	ui.Column(c).FillWidth().Gap(16).TextColor(v.Ink).Children(func() {
		if v.Snapshot.Version < contacts.Version {
			ui.Text(c, tr("Update the Miya background service to use contacts")).TextColor(v.Muted)
			return
		}
		if p.State.Error != "" {
			ui.Text(c, p.State.Error).TextColor(Danger)
			return
		}
		if p.Err != "" {
			ui.Text(c, p.Err).TextColor(Danger)
		}
		if !v.Connected {
			ui.Text(c, tr("Disconnected. Your edits are kept on this device.")).TextColor(v.Muted)
		}
		if p.Editing {
			p.form(c, v)
			return
		}
		if profile, ok := p.State.Find(p.Detail); ok {
			p.detail(c, profile, v)
			return
		}
		p.list(c, v)
	})
}

func (p *ContactUI) list(c *ui.Context, v ContactView) {
	locked := p.Busy || !v.Connected
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, tr("Contacts")).FontSize(25).FontWeight(600).Grow(1)
		contactButton(c, "new-contact", tr("New contact"), locked, func() {
			x := contacts.Profile{}
			if len(v.Engines) > 0 {
				x.Agent = v.Engines[0].ID
			}
			p.Edit(x)
		})
	})
	ui.TextInput(c.Key("contact-search"), &p.Query).FillWidth().Label(tr("Search contacts")).Placeholder(tr("Search by name or description"))
	ui.Row(c).Gap(4).Children(func() {
		contactButton(c, "active", tr("All contacts"), p.Busy, func() { p.Archived = false })
		contactButton(c, "archived", tr("Archived contacts"), p.Busy, func() { p.Archived = true })
	})
	rows := p.State.OrderedProfiles()
	activity := map[string]int64{}
	latest := map[string]contacts.Session{}
	for _, s := range p.State.Sessions {
		if !s.Hidden && s.LastActivity >= activity[s.BotID] {
			activity[s.BotID], latest[s.BotID] = s.LastActivity, s
		}
	}
	count := 0
	for _, profile := range rows {
		if profile.Archived != p.Archived || !strings.Contains(strings.ToLower(profile.Name+" "+profile.Description), strings.ToLower(strings.TrimSpace(p.Query))) {
			continue
		}
		count++
		ui.Row(c.Key(profile.ID)).FillWidth().AlignItems(ui.Center).Gap(6).BorderWidth(0, 0, 1, 0).BorderColor(v.Border).Children(func() {
			b := Quiet(c.Key("chat")).Grow(1).MinWidth(0).MinHeight(80).Padding(10, 0).Gap(12).Label(tr("Chat with ") + profile.Name).Disabled(locked)
			b.Children(func() {
				p.Avatar(c, profile, 44, v)
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, profile.Name).FontWeight(600).FontSize(16).SingleLine().Ellipsis("…")
					preview := latest[profile.ID].Preview
					if preview == "" {
						preview = profile.Description
					}
					ui.Text(c, preview).FontSize(13).TextColor(v.Muted).SingleLine().Ellipsis("…")
				})
				busy, unread := false, 0
				for _, t := range v.Threads {
					if t.Contact == nil || t.Contact.BotID != profile.ID {
						continue
					}
					busy = busy || t.Busy
					if v.Unread != nil && v.Unread(t) {
						unread++
					}
				}
				ui.Column(c).AlignItems(ui.End).Gap(6).Children(func() {
					if at := activity[profile.ID]; at > 0 {
						ui.Text(c, time.UnixMilli(at).Format("Jan 2")).FontSize(11).TextColor(v.Muted)
					}
					if busy {
						ui.Spinner(c).Size(14, 14).Label(tr("Working…"))
					} else if unread > 0 {
						ui.Text(c, fmt.Sprint(unread)).FontSize(11).TextColor(v.Ink).Label(tr("Unread messages"))
					}
				})
			})
			if b.Clicked() {
				p.Submit(contacts.Request{Op: "open", BotID: profile.ID, Context: "recent"}, v)
			}
			if Quiet(c.Key("details")).Size(40, 44).Padding(8).Label(tr("Contact details") + ": " + profile.Name).Disabled(p.Busy).Children(func() { Icon(c, "dots", 18, v.Muted) }).Clicked() {
				p.Detail = profile.ID
			}
		})
	}
	if count == 0 {
		ui.Text(c, tr("No contacts here yet")).TextColor(v.Muted).Padding(16, 0)
		if !p.Archived && p.Query == "" {
			ui.Text(c, tr("Start with a role, then make it your own.")).TextColor(v.Muted)
			for i, template := range contacts.Templates() {
				template.Name, template.Description, template.Persona = tr(template.Name), tr(template.Description), tr(template.Persona)
				contactButton(c, fmt.Sprint("template-", i), template.Name+" · "+template.Description, locked, func() { p.Edit(template) })
			}
		}
	}
}

func (p *ContactUI) form(c *ui.Context, v ContactView) {
	locked := p.Busy || !v.Connected
	title := tr("New contact")
	if p.Draft.ID != "" {
		title = tr("Edit contact")
	}
	ui.Text(c, title).FontSize(25).FontWeight(600)
	p.Avatar(c, p.Draft, 64, v)
	ui.Row(c).Wrap().Gap(4).Children(func() {
		for _, a := range []struct{ id, label string }{{"", "Initial"}, {"leaf", "Companion"}, {"book", "Researcher"}, {"code", "Engineer"}} {
			contactButton(c, "avatar-"+a.id, tr(a.label), locked, func() { p.Draft.Avatar = a.id; p.AvatarData = nil })
		}
		if v.PickAvatar != nil {
			contactButton(c, "avatar-photo", tr("Choose photo"), locked, func() {
				v.PickAvatar(func(data []byte, err error) {
					if err != nil {
						p.Err = err.Error()
						return
					}
					if len(data) == 0 {
						return
					}
					p.AvatarData = data
					img, err := ui.DecodeImage(data)
					if err != nil {
						p.Err = err.Error()
						return
					}
					p.Draft.Avatar = "preview"
					p.avatars["preview"] = img
				})
			})
		}
	})
	contactField(c, "name", tr("Name"), &p.Draft.Name, p.Busy)
	contactField(c, "description", tr("Description"), &p.Draft.Description, p.Busy)
	ui.Text(c, tr("Persona")).FontWeight(600)
	ui.TextArea(c.Key("persona"), &p.Draft.Persona).FillWidth().Lines(5, 12).Label(tr("Persona")).Disabled(p.Busy)
	ui.Text(c, tr("Describe their voice, strengths, and working habits.")).FontSize(12).TextColor(v.Muted)
	ui.Text(c, tr("Execution engine")).FontWeight(600)
	ui.Row(c).Wrap().Gap(4).Children(func() {
		for _, engine := range v.Engines {
			contactButton(c, engine.ID, engine.Name, locked, func() {
				if p.Draft.Agent != engine.ID {
					p.Draft.Agent, p.Draft.Model, p.Draft.Effort = engine.ID, "", ""
				}
			})
		}
	})
	ui.Text(c, model.AgentName(p.Draft.Agent)).TextColor(v.Muted).FontSize(12)
	contactField(c, "model", tr("Default model"), &p.Draft.Model, p.Busy)
	for _, engine := range v.Engines {
		if engine.ID != p.Draft.Agent {
			continue
		}
		if len(engine.Models) > 0 {
			b := Quiet(c.Key("model-choices")).MinHeight(40).Label(tr("Choose model")).Children(func() { ui.Text(c, tr("Choose model")) })
			b.PopoverMenuButton(func(menu *ui.Menu) {
				if menu.Item(tr("Default")).Chosen() {
					p.Draft.Model, p.Draft.Effort = "", ""
				}
				for _, m := range engine.Models {
					label := m.Label
					if label == "" {
						label = m.Value
					}
					if menu.Item(label).Chosen() {
						p.Draft.Model, p.Draft.Effort = m.Value, m.DefaultEffort
					}
				}
			})
		}
	}
	contactField(c, "effort", tr("Reasoning effort"), &p.Draft.Effort, p.Busy)
	ui.Text(c, tr("Persona and execution defaults apply to new conversations. Existing conversations keep their settings.")).FontSize(12).TextColor(v.Muted)
	ui.Row(c).Wrap().Gap(8).Children(func() {
		contactButton(c, "save-contact", tr("Save contact"), locked, func() {
			if err := contacts.Validate(p.Draft); err != nil {
				p.Err = err.Error()
				return
			}
			p.Submit(contacts.Request{Op: "save", Profile: p.Draft, AvatarData: p.AvatarData}, v)
		})
		contactButton(c, "cancel-contact", tr("Cancel"), p.Busy, func() { p.Editing, p.Err, p.pending = false, "", nil })
		if p.Draft.ID != "" {
			contactButton(c, "reload-contact", tr("Reload saved contact"), p.Busy, func() {
				if fresh, ok := p.State.Find(p.Draft.ID); ok {
					p.Edit(fresh)
				}
			})
		}
	})
}

func contactField(c *ui.Context, key, label string, value *string, disabled bool) {
	ui.Text(c, label).FontWeight(600)
	ui.TextInput(c.Key(key), value).FillWidth().Label(label).Disabled(disabled)
}

func (p *ContactUI) detail(c *ui.Context, profile contacts.Profile, v ContactView) {
	locked := p.Busy || !v.Connected
	contactButton(c, "contacts-back", tr("All contacts"), p.Busy, func() { p.Detail = "" })
	p.Avatar(c, profile, 64, v)
	ui.Text(c, profile.Name).FontSize(25).FontWeight(600)
	ui.Text(c, profile.Description).TextColor(v.Muted)
	ui.Row(c).Wrap().Gap(6).Children(func() {
		contactButton(c, "continue", tr("Continue conversation"), locked, func() { p.Submit(contacts.Request{Op: "open", BotID: profile.ID, Context: "recent"}, v) })
		contactButton(c, "edit-contact", tr("Edit contact"), locked, func() { p.Edit(profile) })
		label := tr("Pin contact")
		if profile.Pinned {
			label = tr("Unpin contact")
		}
		contactButton(c, "pin-contact", label, locked, func() { profile.Pinned = !profile.Pinned; p.Submit(contacts.Request{Op: "save", Profile: profile}, v) })
		label = tr("Archive contact")
		if profile.Archived {
			label = tr("Restore contact")
		}
		contactButton(c, "archive-contact", label, locked, func() {
			profile.Archived = !profile.Archived
			p.Submit(contacts.Request{Op: "save", Profile: profile}, v)
		})
	})
	ui.Text(c, tr("Persona")).FontWeight(600)
	ui.Text(c, profile.Persona).TextColor(v.Muted)
	ui.Text(c, tr("Uses your configured engine account. User memories may be shared across contacts; project memories stay in their project.")).FontSize(12).TextColor(v.Muted)
	ui.Text(c, tr("Start a conversation")).FontWeight(600)
	contactButton(c, "new-daily", tr("New daily chat"), locked || profile.Archived, func() { p.Submit(contacts.Request{Op: "open", BotID: profile.ID, Context: "daily", New: true}, v) })
	for _, project := range v.Projects {
		contactButton(c, "project-"+project.Path, tr("Work in ")+project.Name, locked || profile.Archived, func() {
			p.Submit(contacts.Request{Op: "open", BotID: profile.ID, Context: "project", Project: project.Path}, v)
		})
	}
	ui.Text(c, tr("Conversations")).FontWeight(600)
	sessions := append([]contacts.Session(nil), p.State.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].LastActivity > sessions[j].LastActivity })
	for _, session := range sessions {
		if session.BotID != profile.ID || session.Hidden {
			continue
		}
		label := session.Title
		if label == "" {
			label = profile.Name
		}
		context := tr("Daily chat")
		if !session.Daily {
			context = filepath.Base(session.Workspace)
		}
		if session.PersonaVersion != profile.PersonaVersion {
			context += " · " + tr("Using an earlier persona")
		}
		contactButton(c, session.ID, label+" · "+context, locked, func() {
			p.Submit(contacts.Request{Op: "open", BotID: profile.ID, SessionID: session.ID, Context: "recent"}, v)
		})
	}
}

func (p *ContactUI) Bar(c *ui.Context, t model.Thread, v ContactView, details func()) {
	if t.Contact == nil {
		return
	}
	p.useSource(v)
	profile, ok := p.State.Find(t.Contact.BotID)
	if !ok {
		profile = contacts.Profile{Name: t.Contact.Name}
	}
	ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
		contactButton(c, "bot-details", profile.Name, p.Busy, details)
		context := tr("Daily chat")
		if !t.Contact.Daily {
			context = filepath.Base(t.Contact.Workspace)
		}
		button := Quiet(c.Key("contact-context")).MinHeight(40).Label(tr("Conversation context")).Disabled(p.Busy || !v.Connected).Children(func() { ui.Text(c, context); Icon(c, "chevron-down", 12, v.Muted) })
		button.PopoverMenuButton(func(menu *ui.Menu) {
			if menu.Item(tr("Daily chat")).Chosen() {
				p.Submit(contacts.Request{Op: "open", BotID: t.Contact.BotID, Context: "daily"}, v)
			}
			if menu.Item(tr("New daily chat")).Chosen() {
				p.Submit(contacts.Request{Op: "open", BotID: t.Contact.BotID, Context: "daily", New: true}, v)
			}
			for _, project := range v.Projects {
				if menu.Item(project.Name).Chosen() {
					p.Submit(contacts.Request{Op: "open", BotID: t.Contact.BotID, Context: "project", Project: project.Path}, v)
				}
			}
		})
		if ok && profile.PersonaVersion != t.Contact.PersonaVersion {
			ui.Text(c, tr("Using an earlier persona")).FontSize(11).TextColor(v.Muted)
		}
	})
}
