package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

type mobileAutomations struct {
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
					a.runRequest, a.runErr = "", "发送失败，请先刷新确认运行状态："+err.Error()
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
	if m.autos.runRequest != "" {
		m.autos.runRequest, m.autos.runErr = "", "连接已断开，请刷新确认运行状态"
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
					m.autos.loading, m.autos.err = false, "读取失败："+err.Error()
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
		m.autos.err = "自动化数据读取失败，请重试"
		return
	}
	m.autos.list, m.autos.loaded, m.autos.err = list, true, ""
}

func (m *mobileApp) automationsPage(c *ui.Context, detail bool) {
	p, a := m.pal, &m.autos
	if a.runRequest != "" && time.Since(a.runRequestedAt) > 30*time.Second {
		a.runRequest, a.runErr = "", "请求超时，请先刷新确认运行状态"
		a.requestedAt = time.Time{}
	}
	if m.router().Path() != "/thread" && !m.router().Transitioning() && m.open != 0 {
		m.leave()
	}
	if a.loading && time.Since(a.requestedAt) > 10*time.Second {
		a.loading, a.err = false, "读取超时，请重试"
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
		title := "自动化"
		if detail {
			title = "自动化详情"
		}
		ui.Text(c, title).Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		if m.homeAction(c, "refresh", "刷新自动化", "refresh").Disabled(!m.connected || !m.state.Automations || a.loading).Clicked() {
			m.loadPhoneAutomations()
		}
	})
	ui.Scroll(c.Key("automations-content")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 20, 24, 20).Gap(14).Children(func() {
			switch {
			case !m.connected:
				message := "连接桌面端后查看自动化。"
				if a.loaded {
					message = "连接已断开，以下为上次同步的内容。"
				}
				ui.Text(c, message).FontSize(m.font(13)).TextColor(p.muted)
			case !m.state.Automations:
				m.automationPhoneEmpty(c, "需要更新桌面端", "更新后即可查看自动化。若已更新，请稍后重试。")
				return
			}
			if a.err != "" {
				ui.Text(c, a.err).FontSize(m.font(14)).TextColor(danger)
			}
			if !a.loaded {
				if a.loading {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, "正在加载…").TextColor(p.muted)
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
				ui.Text(c, "此自动化已删除").TextColor(p.muted)
				return
			}
			if len(a.list) == 0 {
				m.automationPhoneEmpty(c, "还没有自动化", "可以在桌面端或对话中创建。")
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
			ui.Text(c, v.ScheduleText).FontSize(m.font(13)).TextColor(p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				if v.Enabled {
					ui.Text(c, automationPhoneNext(v)).FontSize(m.font(13)).TextColor(p.muted)
				}
				label, color := "未运行", p.muted
				last, has := lastAutomationRun(v)
				switch {
				case v.Running:
					label, color = "运行中", p.ink
				case !v.Enabled:
					label = "已暂停"
				case has:
					label, color = "上次"+runResultLabel(last.Result), m.automationPhoneRunColor(last.Result)
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
		return "已暂停"
	}
	if v.Next.IsZero() {
		return "下次时间未定"
	}
	return "下次 " + v.Next.Format("1月2日 15:04")
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
			ui.Text(c, "下次运行").Grow(1).FontSize(m.font(13)).TextColor(p.muted)
			status := "已启用"
			if !v.Enabled {
				status = "已暂停"
			}
			if v.Running {
				status = "运行中"
				ui.Spinner(c).FontSize(m.font(12))
			}
			ui.Text(c, status).FontSize(m.font(13)).TextColor(p.muted)
		})
		if v.Enabled && !v.Next.IsZero() {
			ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, v.Next.Format("15:04")).FontSize(m.font(32)).FontWeight(600)
				ui.Text(c, fmt.Sprintf("%d月%d日 周%s", v.Next.Month(), v.Next.Day(), weekdayNames[v.Next.Weekday()])).FontSize(m.font(14)).TextColor(p.muted)
			})
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			icon(c, "clock", 14, p.muted)
			ui.Text(c, v.ScheduleText).FontSize(m.font(14)).TextColor(p.muted)
		})
		label := "立即运行"
		if v.Running {
			label = "运行中"
		} else if m.autos.runRequest != "" && m.autos.runID == v.ID {
			label = "正在启动…"
		}
		if m.pill(c, "run", label, true).Disabled(!m.connected || !m.state.AutomationRun || v.Running || m.autos.runRequest != "").Clicked() {
			m.runPhoneAutomation(v.ID)
		}
		if m.connected && !m.state.AutomationRun {
			ui.Text(c, "更新桌面端后即可立即运行").FontSize(m.font(13)).TextColor(p.muted)
		}
		if m.autos.runID == v.ID && m.autos.runErr != "" {
			ui.Text(c, m.autos.runErr).FontSize(m.font(13)).TextColor(danger)
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Background(p.line)
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		long := len([]rune(v.Instructions)) > 100 || strings.Count(v.Instructions, "\n") > 2
		if long {
			label, rotation := "展开任务内容", float32(0)
			if m.autos.instructionsOpen {
				label, rotation = "收起任务内容", 180
			}
			b := m.phoneButton(c.Key("instructions")).FillWidth().Padding(0).Background(ui.Transparent).Label(label)
			b.Children(func() {
				ui.Text(c, "任务内容").Grow(1).FontSize(m.font(16)).FontWeight(600)
				icon(c, "chevron-down", 14, p.muted).Rotate(rotation)
			})
			if b.Clicked() {
				m.autos.instructionsOpen = !m.autos.instructionsOpen
			}
		} else {
			ui.Text(c, "任务内容").FontSize(m.font(16)).FontWeight(600).Margin(8, 0, 0, 0)
		}
		body := ui.Text(c, nativeText(v.Instructions)).FontSize(m.font(15)).LineHeight(1.6).Selectable()
		if long && !m.autos.instructionsOpen {
			body.MaxLines(3)
		}
		if v.Check != "" {
			ui.Column(c).FillWidth().Padding(12).Gap(6).Radius(8).Background(p.surface).Children(func() {
				ui.Text(c, fmt.Sprintf("完成标准 · 最多 %d 轮", v.Rounds)).FontSize(m.font(12)).TextColor(p.muted)
				ui.Text(c, v.Check).FontSize(m.font(13)).Font(monoFont).Selectable()
			})
		}
	})
	ui.Box(c).FillWidth().Height(0.5).Margin(8, 0, 0, 0).Background(p.line)
	ui.Row(c).FillWidth().Gap(8).Margin(8, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "运行历史").Grow(1).FontSize(m.font(16)).FontWeight(600)
		if len(v.Runs) > 0 {
			ui.Text(c, fmt.Sprintf("%d 次", len(v.Runs))).FontSize(m.font(12)).TextColor(p.muted)
		}
	})
	if len(v.Runs) == 0 {
		ui.Text(c, "还没有运行过").FontSize(m.font(14)).TextColor(p.muted)
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
	case runPassed:
		return m.pal.ok
	case runFailed, runInterrupted:
		return m.pal.warn
	default:
		return m.pal.muted
	}
}

func (m *mobileApp) automationPhoneRun(c *ui.Context, v automationView, r AutomationRun, running bool) {
	p := m.pal
	key := v.ID + ":" + r.Started.Format(time.RFC3339Nano)
	expanded := m.autos.openRuns[key]
	summary := r.Summary
	if r.Report != nil {
		summary = firstNonEmpty(summary, r.Report.Summary)
	}
	hasDetails := false
	if report := r.Report; report != nil {
		hasDetails = report.Summary != "" && strings.TrimSpace(report.Summary) != strings.TrimSpace(summary) ||
			len(report.Findings)+len(report.Metrics)+len(report.Artifacts) > 0 || report.NeedsUser
	}
	expandable := hasDetails || len([]rune(summary)) > 60 || strings.Contains(summary, "\n")
	ui.Column(c).FillWidth().Padding(12, 0).Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			meta := r.Started.Format("1月2日 15:04")
			if r.Rounds > 1 {
				meta += fmt.Sprintf(" · 第 %d/%d 轮", max(r.Round, 1), r.Rounds)
			}
			ui.Text(c, meta).Grow(1).MinWidth(0).FontSize(m.font(13)).TextColor(p.muted)
			label := runResultLabel(r.Result)
			if running {
				label = "运行中"
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
				ui.Text(c, nativeText(metric.text())).FontSize(m.font(14)).Selectable()
			}
			for _, artifact := range report.Artifacts {
				ui.Text(c, nativeText(artifact)).FontSize(m.font(14)).TextColor(p.muted).Selectable()
			}
			if report.NeedsUser {
				ui.Text(c, "需要你处理").FontSize(m.font(14)).TextColor(p.warn)
			}
		}
		ui.Row(c).FillWidth().Wrap().Gap(24).Children(func() {
			if expandable {
				label, rotation := "查看结果", float32(0)
				if expanded {
					label, rotation = "收起结果", 180
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
				b := m.phoneButton(c.Key("conversation")).Padding(0).Gap(6).Background(ui.Transparent).Label("打开运行对话")
				b.Children(func() {
					icon(c, "message-circle", 14, p.muted)
					ui.Text(c, "打开对话").FontSize(m.font(14))
				})
				if b.Clicked() {
					m.openThread(t)
				}
			} else if r.Thread != 0 || r.ServerID != "" {
				ui.Text(c, "对话不可用").AlignSelf(ui.Center).FontSize(m.font(12)).TextColor(p.muted)
			}
		})
	})
}

func (m *mobileApp) automationRunThread(v automationView, r AutomationRun) (Thread, bool) {
	for _, p := range m.state.Projects {
		if p.Path != v.Project {
			continue
		}
		for _, t := range p.Threads {
			if threadAgent(t) != firstNonEmpty(v.Agent, agentCodex) {
				continue
			}
			if r.ServerID != "" && t.ServerID == r.ServerID || r.ServerID == "" && r.Thread != 0 && t.ID == r.Thread {
				return t, true
			}
		}
	}
	return Thread{}, false
}
