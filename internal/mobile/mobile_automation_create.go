package mobile

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	automation "miya/internal/automation"
	model "miya/internal/model"
	remote "miya/internal/remote"
	widgets "miya/internal/widgets"
	strings "strings"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

type mobileAutomationForm struct {
	Request, Project, Agent string
	Draft                   *automation.View
	Enabled                 bool
	Pending, Op             string
	requestedAt             time.Time
	err                     string
	// Keep both the key and payload after an uncertain send, so retrying
	// creates exactly the same automation, including across reconnects.
	CreateKey     string
	CreatePayload json.RawMessage
}

func (m *App) OpenAutomationCreate() {
	f := &m.Autos.Form
	if f.Project == "" {
		*f = mobileAutomationForm{Project: m.selectedProject().Path, Agent: m.composerAgent(), Enabled: true}
	}
	m.Router().Push("/automation/new")
}

func (m *App) sendAutomationForm(op string) {
	f := &m.Autos.Form
	if f.Pending != "" || !m.Connected || m.Conn == nil || !m.State.AutomationCreate {
		return
	}
	r := remote.Request{Op: op, RequestID: automation.NewID() + automation.NewID(), Text: f.Request, Project: f.Project, Agent: f.Agent}
	switch op {
	case "automation-draft":
		if strings.TrimSpace(f.Request) == "" || f.CreateKey != "" {
			return
		}
		f.Draft = nil
	case "automation-create":
		if f.Draft == nil {
			return
		}
		if f.CreateKey == "" {
			a := f.Draft.Automation
			a.ID, a.Project, a.Agent, a.Request, a.Enabled = "", f.Project, f.Agent, strings.TrimSpace(f.Request), f.Enabled
			if err := a.Normalize(); err != nil {
				f.err = err.Error()
				return
			}
			f.CreateKey = r.RequestID
			f.CreatePayload, _ = json.Marshal(a)
		}
		r.RequestID, r.Automation = f.CreateKey, f.CreatePayload
	default:
		return
	}
	f.Pending, f.Op, f.err, f.requestedAt = r.RequestID, op, "", time.Now()
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && f.Pending == r.RequestID {
					f.Pending, f.err = "", tr("Failed to send. Try again: ")+err.Error()
				}
			})
		}
	}()
}

func (m *App) ReceiveAutomationForm(op string, raw json.RawMessage, message string) {
	f := &m.Autos.Form
	var result appmodel.PhoneAutomationFormResult
	if json.Unmarshal(raw, &result) != nil || result.RequestID == "" || result.RequestID != f.Pending || op != f.Op {
		return
	}
	f.Pending, f.err = "", message
	if message != "" {
		if op == "automation-create" {
			f.CreateKey, f.CreatePayload = "", nil
		}
		return
	}
	if result.View == nil || result.View.ID == "" {
		f.err = tr("Failed to load automation data. Try again.")
		return
	}
	if op == "automation-draft" {
		f.Draft = result.View
		return
	}
	v := *result.View
	replaced := false
	for i := range m.Autos.List {
		if m.Autos.List[i].ID == v.ID {
			m.Autos.List[i], replaced = v, true
		}
	}
	if !replaced {
		m.Autos.List = append(m.Autos.List, v)
	}
	m.Autos.Loaded, m.Autos.Err, m.Autos.RequestedAt = true, "", time.Time{}
	*f = mobileAutomationForm{}
	if m.Router().Path() == "/automation/new" {
		m.Autos.Detail = v.ID
		m.Router().Back()
		m.Router().Push("/automation")
	}
}

func (m *App) InterruptAutomationForm() {
	f := &m.Autos.Form
	if f.Pending != "" {
		f.Pending, f.err = "", tr("Disconnected. Reconnect to try again.")
	}
}

func (m *App) automationCreatePage(c *ui.Context) {
	f, p := &m.Autos.Form, m.Pal
	if f.Pending != "" {
		limit := 150 * time.Second
		if f.Op == "automation-create" {
			limit = 30 * time.Second
		}
		if time.Since(f.requestedAt) > limit {
			f.Pending, f.err = "", tr("Request timed out. Try again.")
		}
		c.After(time.Second)
	}
	locked := f.Pending != "" || f.CreateKey != ""
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/automation/new")
		ui.Text(c, tr("New automation")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	ui.Scroll(c.Key("automation-form")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			ui.Text(c, tr("Describe what to do and when. Your desktop will run it on schedule after saving.")).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.5)
			ui.Text(c, tr("Projects")).FontSize(m.Font(13)).TextColor(p.muted)
			name := f.Project
			for _, project := range m.State.Projects {
				if project.Path == f.Project {
					name = project.Name
				}
			}
			pick := m.phoneButton(c.Key("project")).FillWidth().MinHeight(44).Padding(10).Gap(8).Label(tr("Automation project")).Disabled(locked)
			pick.Children(func() {
				ui.Text(c, name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				widgets.Icon(c, "chevron-down", 14, p.muted)
			})
			pick.PopoverMenuButton(func(menu *ui.Menu) {
				for _, project := range m.State.Projects {
					if menu.Item(project.Name).Radio(project.Path == f.Project).Chosen() {
						f.Project, f.Draft, f.err = project.Path, nil, ""
					}
				}
			})
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				for _, agent := range model.Backends {
					if m.pill(c, "agent:"+agent, appmodel.AgentName(agent), f.Agent == agent).Disabled(locked).Clicked() && f.Agent != agent {
						f.Agent, f.Draft, f.err = agent, nil, ""
					}
				}
			})
			input := ui.TextAreaBase(c.Key("request"), &f.Request).Label(tr("What to do")).Placeholder(tr("For example: Check new issues every weekday at 9 AM and summarize those that need attention")).Lines(3, 6).FillWidth().MinHeight(100).Padding(12).FontSize(m.Font(16)).Background(p.surface).Radius(12).Disabled(locked)
			if input.Changed() {
				f.Draft, f.err = nil, ""
			}
			label := tr("Generate settings")
			if f.Pending != "" && f.Op == "automation-draft" {
				label = tr("Generating…")
			}
			if m.pill(c, "draft", label, false).Disabled(locked || !m.Connected || !m.State.AutomationCreate || strings.TrimSpace(f.Request) == "").Clicked() {
				m.sendAutomationForm("automation-draft")
			}
			if f.Draft != nil {
				ui.Text(c, tr("Generated settings")).FontSize(m.Font(17)).FontWeight(600)
				if f.Draft.TimeMissing {
					ui.Text(c, tr("No time specified. Using 9:00 AM daily; you can change it below.")).FontSize(m.Font(13)).TextColor(p.warn)
				}
				m.automationCreateField(c, "name", tr("Name"), &f.Draft.Name, false, locked)
				m.automationCreateField(c, "schedule", tr("Schedule"), &f.Draft.Schedule, false, locked)
				ui.Text(c, tr("Format: minute hour day month weekday. For example, 0 9 * * 1-5 runs at 9 AM on weekdays.")).FontSize(m.Font(12)).TextColor(p.muted).LineHeight(1.5)
				ui.Text(c, automation.DescribeCron(f.Draft.Schedule)+tr(" · Desktop local time")).FontSize(m.Font(13)).TextColor(p.muted)
				m.automationCreateField(c, "instructions", tr("Task instructions"), &f.Draft.Instructions, true, locked)
				m.automationCreateField(c, "check", tr("Check command (optional)"), &f.Draft.Check, false, locked)
				ui.Text(c, tr("Maximum turns")).FontSize(m.Font(13)).TextColor(p.muted)
				ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
					for _, n := range []int{1, 2, 3, 4, 5} {
						label := string(rune('0' + n))
						if m.pill(c, "round:"+label, label, f.Draft.Rounds == n).Label(label + tr(" turns")).Disabled(locked || f.Draft.Check == "").Clicked() {
							f.Draft.Rounds = n
						}
					}
				})
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Switch(c.Key("enabled"), &f.Enabled).Label(tr("Enable after saving")).Disabled(locked)
					ui.Text(c, tr("Enable after saving")).FontSize(m.Font(14))
				})
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					carry := !f.Draft.NoCarryOver
					if ui.Switch(c.Key("carry"), &carry).Label(tr("Include previous result")).Disabled(locked).Changed() {
						f.Draft.NoCarryOver = !carry
					}
					ui.Text(c, tr("Include previous result")).FontSize(m.Font(14))
				})
			}
		})
	})
	ui.Column(c).FillWidth().Padding(10, 20).Gap(8).Children(func() {
		if !m.Connected {
			ui.Text(c, tr("Connect to your desktop to continue creating")).FontSize(m.Font(13)).TextColor(p.muted)
		} else if !m.State.AutomationCreate {
			ui.Text(c, tr("Update the desktop app to create automations")).FontSize(m.Font(13)).TextColor(p.muted)
		}
		if f.err != "" {
			ui.Text(c, f.err).FontSize(m.Font(13)).TextColor(widgets.Danger)
		}
		label := tr("Create")
		if f.CreateKey != "" {
			label = tr("Retry creation")
		}
		if f.Pending != "" && f.Op == "automation-create" {
			label = tr("Saving…")
		}
		if m.pill(c, "save", label, true).FillWidth().Disabled(f.Draft == nil || f.Pending != "" || !m.Connected || !m.State.AutomationCreate).Clicked() {
			m.sendAutomationForm("automation-create")
		}
	})
}

func (m *App) automationCreateField(c *ui.Context, key, label string, value *string, multiline, locked bool) {
	ui.Column(c.Key(key)).FillWidth().Gap(6).Children(func() {
		ui.Text(c, label).FontSize(m.Font(13)).TextColor(m.Pal.muted)
		var input ui.Element
		if multiline {
			input = ui.TextAreaBase(c.Key("input"), value).Lines(3, 8)
		} else {
			input = ui.TextInput(c.Key("input"), value)
		}
		input.Label(label).FillWidth().MinHeight(44).Padding(12).FontSize(m.Font(16)).Background(m.Pal.surface).Radius(10).Disabled(locked)
		if key == "schedule" && input.Changed() {
			m.Autos.Form.Draft.TimeMissing = false
		}
	})
}
