package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

type mobileAutomationForm struct {
	request, project, agent string
	draft                   *automationView
	enabled                 bool
	pending, op             string
	requestedAt             time.Time
	err                     string
	// Keep both the key and payload after an uncertain send, so retrying
	// creates exactly the same automation, including across reconnects.
	createKey     string
	createPayload json.RawMessage
}

func (m *mobileApp) openAutomationCreate() {
	f := &m.autos.form
	if f.project == "" {
		*f = mobileAutomationForm{project: m.selectedProject().Path, agent: m.composerAgent(), enabled: true}
	}
	m.router().Push("/automation/new")
}

func (m *mobileApp) sendAutomationForm(op string) {
	f := &m.autos.form
	if f.pending != "" || !m.connected || m.conn == nil || !m.state.AutomationCreate {
		return
	}
	r := remote.Request{Op: op, RequestID: newAutomationID() + newAutomationID(), Text: f.request, Project: f.project, Agent: f.agent}
	switch op {
	case "automation-draft":
		if strings.TrimSpace(f.request) == "" || f.createKey != "" {
			return
		}
		f.draft = nil
	case "automation-create":
		if f.draft == nil {
			return
		}
		if f.createKey == "" {
			a := f.draft.Automation
			a.ID, a.Project, a.Agent, a.Request, a.Enabled = "", f.project, f.agent, strings.TrimSpace(f.request), f.enabled
			if err := a.normalize(); err != nil {
				f.err = err.Error()
				return
			}
			f.createKey = r.RequestID
			f.createPayload, _ = json.Marshal(a)
		}
		r.RequestID, r.Automation = f.createKey, f.createPayload
	default:
		return
	}
	f.pending, f.op, f.err, f.requestedAt = r.RequestID, op, "", time.Now()
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.conn == conn && f.pending == r.RequestID {
					f.pending, f.err = "", tr("Failed to send. Try again: ")+err.Error()
				}
			})
		}
	}()
}

func (m *mobileApp) receiveAutomationForm(op string, raw json.RawMessage, message string) {
	f := &m.autos.form
	var result phoneAutomationFormResult
	if json.Unmarshal(raw, &result) != nil || result.RequestID == "" || result.RequestID != f.pending || op != f.op {
		return
	}
	f.pending, f.err = "", message
	if message != "" {
		if op == "automation-create" {
			f.createKey, f.createPayload = "", nil
		}
		return
	}
	if result.View == nil || result.View.ID == "" {
		f.err = tr("Failed to load automation data. Try again.")
		return
	}
	if op == "automation-draft" {
		f.draft = result.View
		return
	}
	v := *result.View
	replaced := false
	for i := range m.autos.list {
		if m.autos.list[i].ID == v.ID {
			m.autos.list[i], replaced = v, true
		}
	}
	if !replaced {
		m.autos.list = append(m.autos.list, v)
	}
	m.autos.loaded, m.autos.err, m.autos.requestedAt = true, "", time.Time{}
	*f = mobileAutomationForm{}
	if m.router().Path() == "/automation/new" {
		m.autos.detail = v.ID
		m.router().Back()
		m.router().Push("/automation")
	}
}

func (m *mobileApp) interruptAutomationForm() {
	f := &m.autos.form
	if f.pending != "" {
		f.pending, f.err = "", tr("Disconnected. Reconnect to try again.")
	}
}

func (m *mobileApp) automationCreatePage(c *ui.Context) {
	f, p := &m.autos.form, m.pal
	if f.pending != "" {
		limit := 150 * time.Second
		if f.op == "automation-create" {
			limit = 30 * time.Second
		}
		if time.Since(f.requestedAt) > limit {
			f.pending, f.err = "", tr("Request timed out. Try again.")
		}
		c.After(time.Second)
	}
	locked := f.pending != "" || f.createKey != ""
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/automation/new")
		ui.Text(c, tr("New automation")).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	ui.Scroll(c.Key("automation-form")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			ui.Text(c, tr("Describe what to do and when. Your desktop will run it on schedule after saving.")).FontSize(m.font(14)).TextColor(p.muted).LineHeight(1.5)
			ui.Text(c, tr("Projects")).FontSize(m.font(13)).TextColor(p.muted)
			name := f.project
			for _, project := range m.state.Projects {
				if project.Path == f.project {
					name = project.Name
				}
			}
			pick := m.phoneButton(c.Key("project")).FillWidth().MinHeight(44).Padding(10).Gap(8).Label(tr("Automation project")).Disabled(locked)
			pick.Children(func() {
				ui.Text(c, name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				icon(c, "chevron-down", 14, p.muted)
			})
			pick.PopoverMenuButton(func(menu *ui.Menu) {
				for _, project := range m.state.Projects {
					if menu.Item(project.Name).Radio(project.Path == f.project).Chosen() {
						f.project, f.draft, f.err = project.Path, nil, ""
					}
				}
			})
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				for _, backend := range agentBackends {
					agent := backend.ID()
					if m.pill(c, "agent:"+agent, agentName(agent), f.agent == agent).Disabled(locked).Clicked() && f.agent != agent {
						f.agent, f.draft, f.err = agent, nil, ""
					}
				}
			})
			input := ui.TextAreaBase(c.Key("request"), &f.request).Label(tr("What to do")).Placeholder(tr("For example: Check new issues every weekday at 9 AM and summarize those that need attention")).Lines(3, 6).FillWidth().MinHeight(100).Padding(12).FontSize(m.font(16)).Background(p.surface).Radius(12).Disabled(locked)
			if input.Changed() {
				f.draft, f.err = nil, ""
			}
			label := tr("Generate settings")
			if f.pending != "" && f.op == "automation-draft" {
				label = tr("Generating…")
			}
			if m.pill(c, "draft", label, false).Disabled(locked || !m.connected || !m.state.AutomationCreate || strings.TrimSpace(f.request) == "").Clicked() {
				m.sendAutomationForm("automation-draft")
			}
			if f.draft != nil {
				ui.Text(c, tr("Generated settings")).FontSize(m.font(17)).FontWeight(600)
				if f.draft.TimeMissing {
					ui.Text(c, tr("No time specified. Using 9:00 AM daily; you can change it below.")).FontSize(m.font(13)).TextColor(p.warn)
				}
				m.automationCreateField(c, "name", tr("Name"), &f.draft.Name, false, locked)
				m.automationCreateField(c, "schedule", tr("Schedule"), &f.draft.Schedule, false, locked)
				ui.Text(c, tr("Format: minute hour day month weekday. For example, 0 9 * * 1-5 runs at 9 AM on weekdays.")).FontSize(m.font(12)).TextColor(p.muted).LineHeight(1.5)
				ui.Text(c, describeCron(f.draft.Schedule)+tr(" · Desktop local time")).FontSize(m.font(13)).TextColor(p.muted)
				m.automationCreateField(c, "instructions", tr("Task instructions"), &f.draft.Instructions, true, locked)
				m.automationCreateField(c, "check", tr("Check command (optional)"), &f.draft.Check, false, locked)
				ui.Text(c, tr("Maximum turns")).FontSize(m.font(13)).TextColor(p.muted)
				ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
					for _, n := range []int{1, 2, 3, 4, 5} {
						label := string(rune('0' + n))
						if m.pill(c, "round:"+label, label, f.draft.Rounds == n).Label(label + tr(" turns")).Disabled(locked || f.draft.Check == "").Clicked() {
							f.draft.Rounds = n
						}
					}
				})
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Switch(c.Key("enabled"), &f.enabled).Label(tr("Enable after saving")).Disabled(locked)
					ui.Text(c, tr("Enable after saving")).FontSize(m.font(14))
				})
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					carry := !f.draft.NoCarryOver
					if ui.Switch(c.Key("carry"), &carry).Label(tr("Include previous result")).Disabled(locked).Changed() {
						f.draft.NoCarryOver = !carry
					}
					ui.Text(c, tr("Include previous result")).FontSize(m.font(14))
				})
			}
		})
	})
	ui.Column(c).FillWidth().Padding(10, 20).Gap(8).Children(func() {
		if !m.connected {
			ui.Text(c, tr("Connect to your desktop to continue creating")).FontSize(m.font(13)).TextColor(p.muted)
		} else if !m.state.AutomationCreate {
			ui.Text(c, tr("Update the desktop app to create automations")).FontSize(m.font(13)).TextColor(p.muted)
		}
		if f.err != "" {
			ui.Text(c, f.err).FontSize(m.font(13)).TextColor(danger)
		}
		label := tr("Create")
		if f.createKey != "" {
			label = tr("Retry creation")
		}
		if f.pending != "" && f.op == "automation-create" {
			label = tr("Saving…")
		}
		if m.pill(c, "save", label, true).FillWidth().Disabled(f.draft == nil || f.pending != "" || !m.connected || !m.state.AutomationCreate).Clicked() {
			m.sendAutomationForm("automation-create")
		}
	})
}

func (m *mobileApp) automationCreateField(c *ui.Context, key, label string, value *string, multiline, locked bool) {
	ui.Column(c.Key(key)).FillWidth().Gap(6).Children(func() {
		ui.Text(c, label).FontSize(m.font(13)).TextColor(m.pal.muted)
		var input ui.Element
		if multiline {
			input = ui.TextAreaBase(c.Key("input"), value).Lines(3, 8)
		} else {
			input = ui.TextInput(c.Key("input"), value)
		}
		input.Label(label).FillWidth().MinHeight(44).Padding(12).FontSize(m.font(16)).Background(m.pal.surface).Radius(10).Disabled(locked)
		if key == "schedule" && input.Changed() {
			m.autos.form.draft.TimeMissing = false
		}
	})
}
