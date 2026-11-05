package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/automation"
	"miya/internal/model"
	"miya/internal/remote"
	"miya/internal/textutil"
)

type mobileAutomations struct {
	form             mobileAutomationForm
	list             []automationView
	detail           string
	loaded           bool
	loading          bool
	requestedAt      time.Time
	err              string
	instructionsOpen bool
	openRuns         map[string]bool
	runRequest       string
	runID            string
	runRequestedAt   time.Time
	runErr           string
}

type phoneAutomationRunResult struct {
	RequestID string           `json:"requestId"`
	List      []automationView `json:"list"`
}

func (m *mobileApp) runPhoneAutomation(id string) {
	a := &m.autos
	if a.runRequest != "" || !m.connected || m.conn == nil || !m.state.AutomationRun {
		return
	}
	for _, v := range a.list {
		if v.ID == id && v.Running {
			return
		}
	}
	a.runRequestedAt = time.Now()
	a.runRequest, a.runID, a.runErr = fmt.Sprint(a.runRequestedAt.UnixNano()), id, ""
	request := remote.Request{Op: "automation-run", AutomationID: id, RequestID: a.runRequest}
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(request); err != nil {
			m.onMain(gen, func() {
				if m.conn == conn && a.runRequest == request.RequestID {
					a.runRequest, a.runErr = "", tr("Failed to send. Refresh to check run status: ")+err.Error()
				}
			})
		}
	}()
}

func (m *mobileApp) receiveAutomationRun(data json.RawMessage, message string) {
	var result phoneAutomationRunResult
	if json.Unmarshal(data, &result) != nil || result.RequestID == "" || result.RequestID != m.autos.runRequest {
		return
	}
	a := &m.autos
	a.runRequest, a.runErr = "", message
	if result.List != nil {
		a.list, a.loaded, a.err = result.List, true, ""
	}
	a.requestedAt = time.Time{}
}

func (m *mobileApp) interruptAutomationRequest() {
	m.interruptCollaborationRequests()
	m.interruptAutomationForm()
	if m.autos.runRequest != "" {
		m.autos.runRequest, m.autos.runErr = "", tr("Disconnected. Refresh to check run status.")
	}
}

func (m *mobileApp) loadPhoneAutomations() {
	if m.autos.loading || !m.connected || m.conn == nil || !m.state.Automations {
		return
	}
	m.autos.loading, m.autos.requestedAt = true, time.Now()
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(remote.Request{Op: "automations"}); err != nil {
			m.onMain(gen, func() {
				if m.conn == conn {
					m.autos.loading, m.autos.err = false, tr("Failed to load: ")+err.Error()
				}
			})
		}
	}()
}

func (m *mobileApp) receiveAutomations(data json.RawMessage, message string) {
	m.autos.loading = false
	if message != "" {
		m.autos.err = message
		return
	}
	var list []automationView
	if err := json.Unmarshal(data, &list); err != nil {
		m.autos.err = tr("Failed to load automation data. Try again.")
		return
	}
	m.autos.list, m.autos.loaded, m.autos.err = list, true, ""
}

func (m *mobileApp) automationsPage(c *ui.Context, detail bool) {
	p, a := m.pal, &m.autos
	if a.runRequest != "" && time.Since(a.runRequestedAt) > 30*time.Second {
		a.runRequest, a.runErr = "", tr("Request timed out. Refresh to check run status.")
		a.requestedAt = time.Time{}
	}
	if m.router().Path() != "/thread" && !m.router().Transitioning() && m.open != 0 {
		m.leave()
	}
	if a.loading && time.Since(a.requestedAt) > 10*time.Second {
		a.loading, a.err = false, tr("Read timed out. Try again.")
		a.requestedAt = time.Now()
	}
	if time.Since(a.requestedAt) >= 5*time.Second {
		m.loadPhoneAutomations()
	}
	if m.connected && m.state.Automations {
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
		ui.Text(c, title).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		if m.homeAction(c, "refresh", tr("Refresh automations"), "refresh").Disabled(!m.connected || !m.state.Automations || a.loading).Clicked() {
			m.loadPhoneAutomations()
		}
		if !detail && m.homeAction(c, "create", tr("New automation"), "plus").Disabled(!m.connected || !m.state.AutomationCreate).Clicked() {
			m.openAutomationCreate()
		}
	})
	ui.Scroll(c.Key("automations-content")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			switch {
			case !m.connected:
				message := tr("Connect to your desktop to view automations.")
				if a.loaded {
					message = tr("Disconnected. Showing the last synced content.")
				}
				ui.Text(c, message).FontSize(m.font(13)).TextColor(p.muted)
			case !m.state.Automations:
				m.automationPhoneEmpty(c, tr("Desktop update required"), tr("Update to view automations. If already updated, try again shortly."))
				return
			}
			if a.err != "" {
				ui.Text(c, a.err).FontSize(m.font(14)).TextColor(danger)
			}
			if !detail && m.connected && !m.state.AutomationCreate {
				ui.Text(c, tr("Update the desktop app to create automations on your phone")).FontSize(m.font(13)).TextColor(p.muted)
			}
			if !a.loaded {
				if a.loading {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, tr("Loading…")).TextColor(p.muted)
					})
				}
				return
			}
			if detail {
				for _, v := range a.list {
					if v.ID == a.detail {
						m.automationPhoneDetail(c, v)
						return
					}
				}
				ui.Text(c, tr("This automation was deleted")).TextColor(p.muted)
				return
			}
			if len(a.list) == 0 {
				message := tr("Tap the top-right button to create a task and schedule.")
				if !m.state.AutomationCreate {
					message = tr("Create one on your desktop or in a conversation.")
				}
				m.automationPhoneEmpty(c, tr("No automations yet"), message)
				return
			}
			ui.Column(c).FillWidth().Children(func() {
				for i, v := range a.list {
					m.automationPhoneRow(c.Key(v.ID), v)
					if i < len(a.list)-1 {
						ui.Box(c.Key(i)).FillWidth().Padding(0, 0, 0, 48).Children(func() {
							ui.Box(c).FillWidth().Height(0.5).Background(p.line)
						})
					}
				}
			})
		})
	})
}

func (m *mobileApp) automationPhoneEmpty(c *ui.Context, title, text string) {
	p := m.pal
	ui.Column(c).FillWidth().MinHeight(280).Gap(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		icon(c, "clock", 30, p.muted)
		ui.Text(c, title).FontSize(m.font(18)).FontWeight(600).TextAlign(ui.Center)
		ui.Text(c, text).MaxWidth(280).FontSize(m.font(14)).TextColor(p.muted).TextAlign(ui.Center).LineHeight(1.5)
	})
}

func (m *mobileApp) automationPhoneRow(c *ui.Context, v automationView) {
	p := m.pal
	b := m.phoneButton(c).FillWidth().Padding(16, 0).Gap(12).Radius(0).Background(ui.Transparent).Label(v.Name)
	b.Children(func() {
		ui.Box(c).Size(36, 36).Shrink(0).Radius(10).Background(p.surface).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			icon(c, "clock", 19, p.muted)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, v.Name).FontSize(m.font(17)).FontWeight(600).MaxLines(2)
			ui.Text(c, m.automationPhoneProject(v)+" · "+agentName(v.Agent)).FontSize(m.font(12)).TextColor(p.muted).MaxLines(2)
			ui.Text(c, automation.DescribeCron(v.Schedule)).FontSize(m.font(13)).TextColor(p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				if v.Enabled {
					ui.Text(c, automationPhoneNext(v)).FontSize(m.font(13)).TextColor(p.muted)
				}
				label, color := tr("Not run yet"), p.muted
				last, has := lastAutomationRun(v)
				switch {
				case v.Running:
					label, color = tr("Running"), p.ink
				case !v.Enabled:
					label = tr("Paused")
				case has:
					label, color = tr("Last run")+automation.ResultLabel(last.Result), m.automationPhoneRunColor(last.Result)
				}
				ui.Text(c, label).FontSize(m.font(12)).TextColor(color)
			})
		})
		icon(c, "chevron-right", 13, p.muted)
	})
	if b.Clicked() {
		m.autos.detail, m.autos.instructionsOpen = v.ID, false
		m.router().Push("/automation")
	}
}

func automationPhoneNext(v automationView) string {
	if !v.Enabled {
		return tr("Paused")
	}
	if v.Next.IsZero() {
		return tr("Next run not scheduled")
	}
	return tr("Next ") + v.Next.Format("Jan 2, 15:04")
}

func (m *mobileApp) automationPhoneProject(v automationView) string {
	for _, p := range m.state.Projects {
		if p.Path == v.Project {
			return p.Name
		}
	}
	return filepath.Base(v.Project)
}

func (m *mobileApp) automationPhoneDetail(c *ui.Context, v automationView) {
	p := m.pal
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, v.Name).FontSize(m.font(26)).FontWeight(600)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			agentIcon(c, v.Agent, 16)
			ui.Text(c, m.automationPhoneProject(v)+" · "+agentName(v.Agent)).FontSize(m.font(13)).TextColor(p.muted)
		})
	})
	ui.Column(c).FillWidth().Padding(12, 0, 20, 0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Next run")).Grow(1).FontSize(m.font(13)).TextColor(p.muted)
			status := tr("Enabled")
			if !v.Enabled {
				status = tr("Paused")
			}
			if v.Running {
				status = tr("Running")
				ui.Spinner(c).FontSize(m.font(12))
			}
			ui.Text(c, status).FontSize(m.font(13)).TextColor(p.muted)
		})
		if v.Enabled && !v.Next.IsZero() {
			ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, v.Next.Format("15:04")).FontSize(m.font(32)).FontWeight(600)
				ui.Text(c, fmt.Sprintf(tr("%d/%d %s"), v.Next.Month(), v.Next.Day(), tr(weekdayNames[v.Next.Weekday()]))).FontSize(m.font(14)).TextColor(p.muted)
			})
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			icon(c, "clock", 14, p.muted)
			ui.Text(c, automation.DescribeCron(v.Schedule)).FontSize(m.font(14)).TextColor(p.muted)
		})
		label := tr("Run now")
		if v.Running {
			label = tr("Running")
		} else if m.autos.runRequest != "" && m.autos.runID == v.ID {
			label = tr("Starting…")
		}
		if m.pill(c, "run", label, true).Disabled(!m.connected || !m.state.AutomationRun || v.Running || m.autos.runRequest != "").Clicked() {
			m.runPhoneAutomation(v.ID)
		}
		if m.connected && !m.state.AutomationRun {
			ui.Text(c, tr("Update the desktop app to run now")).FontSize(m.font(13)).TextColor(p.muted)
		}
		if m.autos.runID == v.ID && m.autos.runErr != "" {
			ui.Text(c, m.autos.runErr).FontSize(m.font(13)).TextColor(danger)
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Background(p.line)
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		long := len([]rune(v.Instructions)) > 100 || strings.Count(v.Instructions, "\n") > 2
		if long {
			label, rotation := tr("Expand instructions"), float32(0)
			if m.autos.instructionsOpen {
				label, rotation = tr("Collapse instructions"), 180
			}
			b := m.phoneButton(c.Key("instructions")).FillWidth().Padding(0).Background(ui.Transparent).Label(label)
			b.Children(func() {
				ui.Text(c, tr("Task instructions")).Grow(1).FontSize(m.font(16)).FontWeight(600)
				icon(c, "chevron-down", 14, p.muted).Rotate(rotation)
			})
			if b.Clicked() {
				m.autos.instructionsOpen = !m.autos.instructionsOpen
			}
		} else {
			ui.Text(c, tr("Task instructions")).FontSize(m.font(16)).FontWeight(600).Margin(8, 0, 0, 0)
		}
		body := ui.Text(c, nativeText(v.Instructions)).FontSize(m.font(15)).LineHeight(1.6).Selectable()
		if long && !m.autos.instructionsOpen {
			body.MaxLines(3)
		}
		if v.Check != "" {
			ui.Column(c).FillWidth().Padding(12).Gap(6).Radius(8).Background(p.surface).Children(func() {
				ui.Text(c, fmt.Sprintf(tr("Completion criteria · Up to %d turns"), v.Rounds)).FontSize(m.font(12)).TextColor(p.muted)
				ui.Text(c, v.Check).FontSize(m.font(13)).Font(monoFont).Selectable()
			})
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Margin(8, 0, 0, 0).Background(p.line)
	ui.Row(c).FillWidth().Gap(8).Margin(8, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr("Run history")).Grow(1).FontSize(m.font(16)).FontWeight(600)
		if len(v.Runs) > 0 {
			ui.Text(c, fmt.Sprintf(tr("%d runs"), len(v.Runs))).FontSize(m.font(12)).TextColor(p.muted)
		}
	})
	if len(v.Runs) == 0 {
		ui.Text(c, tr("No runs yet")).FontSize(m.font(14)).TextColor(p.muted)
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

func (m *mobileApp) automationPhoneRunColor(result string) ui.Color {
	switch result {
	case automation.Passed:
		return m.pal.ok
	case automation.Failed, automation.Interrupted:
		return m.pal.warn
	default:
		return m.pal.muted
	}
}

func (m *mobileApp) automationPhoneRun(c *ui.Context, v automationView, r automation.Run, running bool) {
	p := m.pal
	key := v.ID + ":" + r.Started.Format(time.RFC3339Nano)
	expanded := m.autos.openRuns[key]
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
			ui.Text(c, meta).Grow(1).MinWidth(0).FontSize(m.font(13)).TextColor(p.muted)
			label := automation.ResultLabel(r.Result)
			if running {
				label = tr("Running")
				ui.Spinner(c).FontSize(m.font(12))
			} else {
				ui.Box(c).Size(5, 5).Radius(2.5).Background(m.automationPhoneRunColor(r.Result))
			}
			ui.Text(c, label).FontSize(m.font(13)).TextColor(p.muted)
		})
		if summary != "" {
			t := ui.Text(c, nativeText(summary)).FontSize(m.font(15)).LineHeight(1.5).Selectable()
			if expandable && !expanded {
				t.MaxLines(2)
			}
		}
		if expanded && r.Report != nil {
			report := r.Report
			if report.Summary != "" && strings.TrimSpace(report.Summary) != strings.TrimSpace(summary) {
				ui.Text(c, nativeText(report.Summary)).FontSize(m.font(14)).LineHeight(1.6).Selectable()
			}
			for _, finding := range report.Findings {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, "•").FontSize(m.font(14)).TextColor(p.muted)
					ui.Text(c, nativeText(finding)).Grow(1).MinWidth(0).FontSize(m.font(14)).LineHeight(1.6).Selectable()
				})
			}
			for _, metric := range report.Metrics {
				ui.Text(c, nativeText(metric.Text())).FontSize(m.font(14)).Selectable()
			}
			for _, artifact := range report.Artifacts {
				ui.Text(c, nativeText(artifact)).FontSize(m.font(14)).TextColor(p.muted).Selectable()
			}
			if report.NeedsUser {
				ui.Text(c, tr("Needs your attention")).FontSize(m.font(14)).TextColor(p.warn)
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
					ui.Text(c, label).FontSize(m.font(14))
					icon(c, "chevron-down", 12, p.muted).Rotate(rotation)
				})
				if b.Clicked() {
					if m.autos.openRuns == nil {
						m.autos.openRuns = map[string]bool{}
					}
					m.autos.openRuns[key] = !expanded
				}
			}
			if t, ok := m.automationRunThread(v, r); ok {
				b := m.phoneButton(c.Key("conversation")).Padding(0).Gap(6).Background(ui.Transparent).Label(tr("Open run conversation"))
				b.Children(func() {
					icon(c, "message-circle", 14, p.muted)
					ui.Text(c, tr("Open conversation")).FontSize(m.font(14))
				})
				if b.Clicked() {
					m.openThread(t)
				}
			} else if r.Thread != 0 || r.ServerID != "" {
				ui.Text(c, tr("Conversation unavailable")).AlignSelf(ui.Center).FontSize(m.font(12)).TextColor(p.muted)
			}
		})
	})
}

func (m *mobileApp) automationRunThread(v automationView, r automation.Run) (Thread, bool) {
	for _, p := range m.state.Projects {
		if p.Path != v.Project {
			continue
		}
		for _, t := range p.Threads {
			if model.ThreadAgent(t) != textutil.FirstNonEmpty(v.Agent, agentCodex) {
				continue
			}
			if r.ServerID != "" && t.ServerID == r.ServerID || r.ServerID == "" && r.Thread != 0 && t.ID == r.Thread {
				return t, true
			}
		}
	}
	return Thread{}, false
}
