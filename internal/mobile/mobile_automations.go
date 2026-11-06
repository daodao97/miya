package mobile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

type mobileAutomations struct {
	Form             mobileAutomationForm
	List             []automation.View
	Detail           string
	Loaded           bool
	Loading          bool
	RequestedAt      time.Time
	Err              string
	InstructionsOpen bool
	openRuns         map[string]bool
	RunRequest       string
	RunID            string
	RunRequestedAt   time.Time
	runErr           string
}

func (m *App) runPhoneAutomation(id string) {
	a := &m.Autos
	if a.RunRequest != "" || !m.Connected || m.Conn == nil || !m.State.AutomationRun {
		return
	}
	for _, v := range a.List {
		if v.ID == id && v.Running {
			return
		}
	}
	a.RunRequestedAt = time.Now()
	a.RunRequest, a.RunID, a.runErr = fmt.Sprint(a.RunRequestedAt.UnixNano()), id, ""
	request := remote.Request{Op: "automation-run", AutomationID: id, RequestID: a.RunRequest}
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(request); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && a.RunRequest == request.RequestID {
					a.RunRequest, a.runErr = "", tr("Failed to send. Refresh to check run status: ")+err.Error()
				}
			})
		}
	}()
}

func (m *App) ReceiveAutomationRun(data json.RawMessage, message string) {
	var result phoneapi.AutomationRunResult
	if json.Unmarshal(data, &result) != nil || result.RequestID == "" || result.RequestID != m.Autos.RunRequest {
		return
	}
	a := &m.Autos
	a.RunRequest, a.runErr = "", message
	if result.List != nil {
		a.List, a.Loaded, a.Err = result.List, true, ""
	}
	a.RequestedAt = time.Time{}
}

func (m *App) interruptAutomationRequest() {
	m.InterruptCollaborationRequests()
	m.InterruptAutomationForm()
	if m.Autos.RunRequest != "" {
		m.Autos.RunRequest, m.Autos.runErr = "", tr("Disconnected. Refresh to check run status.")
	}
}

func (m *App) loadPhoneAutomations() {
	if m.Autos.Loading || !m.Connected || m.Conn == nil || !m.State.Automations {
		return
	}
	m.Autos.Loading, m.Autos.RequestedAt = true, time.Now()
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(remote.Request{Op: "automations"}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn {
					m.Autos.Loading, m.Autos.Err = false, tr("Failed to load: ")+err.Error()
				}
			})
		}
	}()
}

func (m *App) ReceiveAutomations(data json.RawMessage, message string) {
	m.Autos.Loading = false
	if message != "" {
		m.Autos.Err = message
		return
	}
	var list []automation.View
	if err := json.Unmarshal(data, &list); err != nil {
		m.Autos.Err = tr("Failed to load automation data. Try again.")
		return
	}
	m.Autos.List, m.Autos.Loaded, m.Autos.Err = list, true, ""
}

func (m *App) automationsPage(c *ui.Context, detail bool) {
	p, a := m.Pal, &m.Autos
	if a.RunRequest != "" && time.Since(a.RunRequestedAt) > 30*time.Second {
		a.RunRequest, a.runErr = "", tr("Request timed out. Refresh to check run status.")
		a.RequestedAt = time.Time{}
	}
	if m.Router().Path() != "/thread" && !m.Router().Transitioning() && m.Open != 0 {
		m.leave()
	}
	if a.Loading && time.Since(a.RequestedAt) > 10*time.Second {
		a.Loading, a.Err = false, tr("Read timed out. Try again.")
		a.RequestedAt = time.Now()
	}
	if time.Since(a.RequestedAt) >= 5*time.Second {
		m.loadPhoneAutomations()
	}
	if m.Connected && m.State.Automations {
		c.After(time.Second)
	}
	ui.Row(c).FillWidth().MinHeight(52).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		path := "/automations"
		if detail {
			path = "/automation"
		}
		m.navigationBack(c, path)
		title := "Automations"
		if detail {
			title = tr("Automation details")
		}
		ui.Text(c, title).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		if m.homeAction(c, "refresh", tr("Refresh automations"), "refresh").Disabled(!m.Connected || !m.State.Automations || a.Loading).Clicked() {
			m.loadPhoneAutomations()
		}
		if !detail && m.homeAction(c, "create", tr("New automation"), "plus").Disabled(!m.Connected || !m.State.AutomationCreate).Clicked() {
			m.OpenAutomationCreate()
		}
	})
	ui.Scroll(c.Key("automations-content")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			switch {
			case !m.Connected:
				message := tr("Connect to your desktop to view automations.")
				if a.Loaded {
					message = tr("Disconnected. Showing the last synced content.")
				}
				ui.Text(c, message).FontSize(m.Font(13)).TextColor(p.muted)
			case !m.State.Automations:
				m.automationPhoneEmpty(c, tr("Desktop update required"), tr("Update to view automations. If already updated, try again shortly."))
				return
			}
			if a.Err != "" {
				ui.Text(c, a.Err).FontSize(m.Font(14)).TextColor(widgets.Danger)
			}
			if !detail && m.Connected && !m.State.AutomationCreate {
				ui.Text(c, tr("Update the desktop app to create automations on your phone")).FontSize(m.Font(13)).TextColor(p.muted)
			}
			if !a.Loaded {
				if a.Loading {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, tr("Loading…")).TextColor(p.muted)
					})
				}
				return
			}
			if detail {
				for _, v := range a.List {
					if v.ID == a.Detail {
						m.automationPhoneDetail(c, v)
						return
					}
				}
				ui.Text(c, tr("This automation was deleted")).TextColor(p.muted)
				return
			}
			if len(a.List) == 0 {
				message := tr("Tap the top-right button to create a task and schedule.")
				if !m.State.AutomationCreate {
					message = tr("Create one on your desktop or in a conversation.")
				}
				m.automationPhoneEmpty(c, tr("No automations yet"), message)
				return
			}
			ui.Column(c).FillWidth().Children(func() {
				for i, v := range a.List {
					m.automationPhoneRow(c.Key(v.ID), v)
					if i < len(a.List)-1 {
						ui.Box(c.Key(i)).FillWidth().Padding(0, 0, 0, 48).Children(func() {
							ui.Box(c).FillWidth().Height(0.5).Background(p.line)
						})
					}
				}
			})
		})
	})
}

func (m *App) automationPhoneEmpty(c *ui.Context, title, text string) {
	p := m.Pal
	ui.Column(c).FillWidth().MinHeight(280).Gap(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		widgets.Icon(c, "clock", 30, p.muted)
		ui.Text(c, title).FontSize(m.Font(18)).FontWeight(600).TextAlign(ui.Center)
		ui.Text(c, text).MaxWidth(280).FontSize(m.Font(14)).TextColor(p.muted).TextAlign(ui.Center).LineHeight(1.5)
	})
}

func (m *App) automationPhoneRow(c *ui.Context, v automation.View) {
	p := m.Pal
	b := m.phoneButton(c).FillWidth().Padding(16, 0).Gap(12).Radius(0).Background(ui.Transparent).Label(v.Name)
	b.Children(func() {
		ui.Box(c).Size(36, 36).Shrink(0).Radius(10).Background(p.surface).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			widgets.Icon(c, "clock", 19, p.muted)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, v.Name).FontSize(m.Font(17)).FontWeight(600).MaxLines(2)
			ui.Text(c, m.automationPhoneProject(v)+" · "+appmodel.AgentName(v.Agent)).FontSize(m.Font(12)).TextColor(p.muted).MaxLines(2)
			ui.Text(c, automation.DescribeCron(v.Schedule)).FontSize(m.Font(13)).TextColor(p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				if v.Enabled {
					ui.Text(c, automationPhoneNext(v)).FontSize(m.Font(13)).TextColor(p.muted)
				}
				label, color := tr("Not run yet"), p.muted
				last, has := widgets.LastAutomationRun(v)
				switch {
				case v.Running:
					label, color = tr("Running"), p.ink
				case !v.Enabled:
					label = tr("Paused")
				case has:
					label, color = tr("Last run")+automation.ResultLabel(last.Result), m.automationPhoneRunColor(last.Result)
				}
				ui.Text(c, label).FontSize(m.Font(12)).TextColor(color)
			})
		})
		widgets.Icon(c, "chevron-right", 13, p.muted)
	})
	if b.Clicked() {
		m.Autos.Detail, m.Autos.InstructionsOpen = v.ID, false
		m.Router().Push("/automation")
	}
}

func automationPhoneNext(v automation.View) string {
	if !v.Enabled {
		return tr("Paused")
	}
	if v.Next.IsZero() {
		return tr("Next run not scheduled")
	}
	return tr("Next ") + v.Next.Format("Jan 2, 15:04")
}

func (m *App) automationPhoneProject(v automation.View) string {
	for _, p := range m.State.Projects {
		if p.Path == v.Project {
			return p.Name
		}
	}
	return filepath.Base(v.Project)
}

func (m *App) automationPhoneDetail(c *ui.Context, v automation.View) {
	p := m.Pal
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, v.Name).FontSize(m.Font(26)).FontWeight(600)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			widgets.AgentIcon(c, v.Agent, 16)
			ui.Text(c, m.automationPhoneProject(v)+" · "+appmodel.AgentName(v.Agent)).FontSize(m.Font(13)).TextColor(p.muted)
		})
	})
	ui.Column(c).FillWidth().Padding(12, 0, 20, 0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Next run")).Grow(1).FontSize(m.Font(13)).TextColor(p.muted)
			status := tr("Enabled")
			if !v.Enabled {
				status = tr("Paused")
			}
			if v.Running {
				status = tr("Running")
				ui.Spinner(c).FontSize(m.Font(12))
			}
			ui.Text(c, status).FontSize(m.Font(13)).TextColor(p.muted)
		})
		if v.Enabled && !v.Next.IsZero() {
			ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, v.Next.Format("15:04")).FontSize(m.Font(32)).FontWeight(600)
				ui.Text(c, fmt.Sprintf(tr("%d/%d %s"), v.Next.Month(), v.Next.Day(), tr(automation.WeekdayNames[v.Next.Weekday()]))).FontSize(m.Font(14)).TextColor(p.muted)
			})
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, "clock", 14, p.muted)
			ui.Text(c, automation.DescribeCron(v.Schedule)).FontSize(m.Font(14)).TextColor(p.muted)
		})
		label := tr("Run now")
		if v.Running {
			label = tr("Running")
		} else if m.Autos.RunRequest != "" && m.Autos.RunID == v.ID {
			label = tr("Starting…")
		}
		if m.pill(c, "run", label, true).Disabled(!m.Connected || !m.State.AutomationRun || v.Running || m.Autos.RunRequest != "").Clicked() {
			m.runPhoneAutomation(v.ID)
		}
		if m.Connected && !m.State.AutomationRun {
			ui.Text(c, tr("Update the desktop app to run now")).FontSize(m.Font(13)).TextColor(p.muted)
		}
		if m.Autos.RunID == v.ID && m.Autos.runErr != "" {
			ui.Text(c, m.Autos.runErr).FontSize(m.Font(13)).TextColor(widgets.Danger)
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Background(p.line)
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		long := len([]rune(v.Instructions)) > 100 || strings.Count(v.Instructions, "\n") > 2
		if long {
			label, rotation := tr("Expand instructions"), float32(0)
			if m.Autos.InstructionsOpen {
				label, rotation = tr("Collapse instructions"), 180
			}
			b := m.phoneButton(c.Key("instructions")).FillWidth().Padding(0).Background(ui.Transparent).Label(label)
			b.Children(func() {
				ui.Text(c, tr("Task instructions")).Grow(1).FontSize(m.Font(16)).FontWeight(600)
				widgets.Icon(c, "chevron-down", 14, p.muted).Rotate(rotation)
			})
			if b.Clicked() {
				m.Autos.InstructionsOpen = !m.Autos.InstructionsOpen
			}
		} else {
			ui.Text(c, tr("Task instructions")).FontSize(m.Font(16)).FontWeight(600).Margin(8, 0, 0, 0)
		}
		body := ui.Text(c, msgfmt.NativeText(v.Instructions)).FontSize(m.Font(15)).LineHeight(1.6).Selectable()
		if long && !m.Autos.InstructionsOpen {
			body.MaxLines(3)
		}
		if v.Check != "" {
			ui.Column(c).FillWidth().Padding(12).Gap(6).Radius(8).Background(p.surface).Children(func() {
				ui.Text(c, fmt.Sprintf(tr("Completion criteria · Up to %d turns"), v.Rounds)).FontSize(m.Font(12)).TextColor(p.muted)
				ui.Text(c, v.Check).FontSize(m.Font(13)).Font(widgets.MonoFont).Selectable()
			})
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Margin(8, 0, 0, 0).Background(p.line)
	ui.Row(c).FillWidth().Gap(8).Margin(8, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr("Run history")).Grow(1).FontSize(m.Font(16)).FontWeight(600)
		if len(v.Runs) > 0 {
			ui.Text(c, fmt.Sprintf(tr("%d runs"), len(v.Runs))).FontSize(m.Font(12)).TextColor(p.muted)
		}
	})
	if len(v.Runs) == 0 {
		ui.Text(c, tr("No runs yet")).FontSize(m.Font(14)).TextColor(p.muted)
	}
	ui.Column(c).FillWidth().Children(func() {
		for i := len(v.Runs) - 1; i >= 0; i-- {
			m.automationPhoneRun(c.Key(i), v, v.Runs[i], v.Running && i == len(v.Runs)-1)
			if i > 0 {
				ui.Box(c.Key(i)).FillWidth().Height(0.5).Background(p.line)
			}
		}
	})
}

func (m *App) automationPhoneRunColor(result string) ui.Color {
	switch result {
	case automation.Passed:
		return m.Pal.ok
	case automation.Failed, automation.Interrupted:
		return m.Pal.warn
	default:
		return m.Pal.muted
	}
}

func (m *App) automationPhoneRun(c *ui.Context, v automation.View, r automation.Run, running bool) {
	p := m.Pal
	key := v.ID + ":" + r.Started.Format(time.RFC3339Nano)
	expanded := m.Autos.openRuns[key]
	summary := r.Summary
	if r.Report != nil {
		summary = textutil.FirstNonEmpty(summary, r.Report.Summary)
	}
	hasDetails := false
	if report := r.Report; report != nil {
		hasDetails = report.Summary != "" && strings.TrimSpace(report.Summary) != strings.TrimSpace(summary) ||
			len(report.Findings)+len(report.Metrics)+len(report.Artifacts) > 0 || report.NeedsUser
	}
	expandable := hasDetails || len([]rune(summary)) > 60 || strings.Contains(summary, "\n")
	ui.Column(c).FillWidth().Padding(12, 0).Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			meta := r.Started.Format("Jan 2, 15:04")
			if r.Rounds > 1 {
				meta += fmt.Sprintf(tr(" · Turn %d/%d"), max(r.Round, 1), r.Rounds)
			}
			ui.Text(c, meta).Grow(1).MinWidth(0).FontSize(m.Font(13)).TextColor(p.muted)
			label := automation.ResultLabel(r.Result)
			if running {
				label = tr("Running")
				ui.Spinner(c).FontSize(m.Font(12))
			} else {
				ui.Box(c).Size(5, 5).Radius(2.5).Background(m.automationPhoneRunColor(r.Result))
			}
			ui.Text(c, label).FontSize(m.Font(13)).TextColor(p.muted)
		})
		if summary != "" {
			t := ui.Text(c, msgfmt.NativeText(summary)).FontSize(m.Font(15)).LineHeight(1.5).Selectable()
			if expandable && !expanded {
				t.MaxLines(2)
			}
		}
		if expanded && r.Report != nil {
			report := r.Report
			if report.Summary != "" && strings.TrimSpace(report.Summary) != strings.TrimSpace(summary) {
				ui.Text(c, msgfmt.NativeText(report.Summary)).FontSize(m.Font(14)).LineHeight(1.6).Selectable()
			}
			for _, finding := range report.Findings {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, "•").FontSize(m.Font(14)).TextColor(p.muted)
					ui.Text(c, msgfmt.NativeText(finding)).Grow(1).MinWidth(0).FontSize(m.Font(14)).LineHeight(1.6).Selectable()
				})
			}
			for _, metric := range report.Metrics {
				ui.Text(c, msgfmt.NativeText(metric.Text())).FontSize(m.Font(14)).Selectable()
			}
			for _, artifact := range report.Artifacts {
				ui.Text(c, msgfmt.NativeText(artifact)).FontSize(m.Font(14)).TextColor(p.muted).Selectable()
			}
			if report.NeedsUser {
				ui.Text(c, tr("Needs your attention")).FontSize(m.Font(14)).TextColor(p.warn)
			}
		}
		ui.Row(c).FillWidth().Wrap().Gap(24).Children(func() {
			if expandable {
				label, rotation := tr("View result"), float32(0)
				if expanded {
					label, rotation = tr("Collapse result"), 180
				}
				b := m.phoneButton(c.Key("result")).Padding(0).Gap(6).Background(ui.Transparent).Label(label)
				b.Children(func() {
					ui.Text(c, label).FontSize(m.Font(14))
					widgets.Icon(c, "chevron-down", 12, p.muted).Rotate(rotation)
				})
				if b.Clicked() {
					if m.Autos.openRuns == nil {
						m.Autos.openRuns = map[string]bool{}
					}
					m.Autos.openRuns[key] = !expanded
				}
			}
			if t, ok := m.AutomationRunThread(v, r); ok {
				b := m.phoneButton(c.Key("conversation")).Padding(0).Gap(6).Background(ui.Transparent).Label(tr("Open run conversation"))
				b.Children(func() {
					widgets.Icon(c, "message-circle", 14, p.muted)
					ui.Text(c, tr("Open conversation")).FontSize(m.Font(14))
				})
				if b.Clicked() {
					m.OpenThread(t)
				}
			} else if r.Thread != 0 || r.ServerID != "" {
				ui.Text(c, tr("Conversation unavailable")).AlignSelf(ui.Center).FontSize(m.Font(12)).TextColor(p.muted)
			}
		})
	})
}

func (m *App) AutomationRunThread(v automation.View, r automation.Run) (model.Thread, bool) {
	for _, p := range m.State.Projects {
		if p.Path != v.Project {
			continue
		}
		for _, t := range p.Threads {
			if model.ThreadAgent(t) != textutil.FirstNonEmpty(v.Agent, m.defaultAgent()) {
				continue
			}
			if r.ServerID != "" && t.ServerID == r.ServerID || r.ServerID == "" && r.Thread != 0 && t.ID == r.Thread {
				return t, true
			}
		}
	}
	return model.Thread{}, false
}
