package mobile

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/remote"
	"miya/internal/textutil"
)

// loadPhoneBriefing asks the desktop for the daily briefing's setting, at
// most every five seconds; setPhoneBriefing changes it. The reply lands in
// m.Autos.Briefing.
func (m *App) loadPhoneBriefing() {
	a := &m.Autos
	if !m.Connected || m.Conn == nil || !m.State.AutomationBriefing || time.Since(a.BriefingRequestedAt) < 5*time.Second {
		return
	}
	a.BriefingRequestedAt = time.Now()
	conn := m.Conn
	go func() { _ = conn.Send(remote.Request{Op: "automation-briefing"}) }()
}

func (m *App) setPhoneBriefing(at string) {
	if !m.Connected || m.Conn == nil || !m.State.AutomationBriefing {
		return
	}
	body, _ := json.Marshal(automation.Request{Action: "set", Data: at})
	conn := m.Conn
	go func() { _ = conn.Send(remote.Request{Op: "automation-briefing", Automation: body}) }()
}

// ReceiveBriefing takes the desktop's reply about the daily briefing.
func (m *App) ReceiveBriefing(data json.RawMessage, message string) {
	if message != "" {
		m.Autos.Err = message
		return
	}
	var v automation.BriefingView
	if json.Unmarshal(data, &v) == nil {
		m.Autos.Briefing, m.Autos.BriefingTime = &v, v.Time
	}
}

// automationPhoneBriefing turns the daily briefing on or off and sets its
// time, under the list of automations.
func (m *App) automationPhoneBriefing(c *ui.Context) {
	p, a := m.Pal, &m.Autos
	m.loadPhoneBriefing()
	if a.Briefing == nil {
		return
	}
	ui.Column(c).FillWidth().Padding(14, 16).Gap(10).Radius(18).Background(p.surface).Children(func() {
		on := a.Briefing.Time != ""
		if ui.Switch(c.Key("on"), &on).Label(tr("Daily briefing")).Disabled(!m.Connected).Changed() {
			at := ""
			if on {
				at = textutil.FirstNonEmpty(a.BriefingTime, "09:00")
			}
			m.setPhoneBriefing(at)
		}
		if on {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("time"), &a.BriefingTime).Label(tr("Briefing time")).Placeholder("09:00").Grow(1).FontSize(m.Font(15))
				if m.pill(c, "save", tr("Save"), true).Disabled(!m.Connected || a.BriefingTime == a.Briefing.Time).Clicked() {
					m.setPhoneBriefing(a.BriefingTime)
				}
			})
		}
		ui.Text(c, tr("One notice a day with the last 24 hours of runs and what needs you; nothing on a day with nothing to say")).FontSize(m.Font(13)).TextColor(p.muted).Wrap()
	})
}
