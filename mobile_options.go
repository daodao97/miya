package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/remote"
)

func (m *mobileApp) agentOptions(agent string) phoneAgentOptions {
	for _, a := range m.state.Agents {
		if a.Agent == agent {
			return a
		}
	}
	return phoneAgentOptions{Agent: agent, Current: remote.SendOptions{Model: m.state.Model, Effort: m.state.Effort, Policy: "on-request"}}
}
func (m *mobileApp) sendOptions(agent string) remote.SendOptions {
	if o, ok := m.options[agent]; ok {
		return o
	}
	o := m.agentOptions(agent).Current
	if m.open != 0 {
		t := m.view.c.current()
		if threadAgent(t) == agent && t.PermissionPolicy != "" {
			o.Policy = t.PermissionPolicy
		}
	}
	return o
}
func (m *mobileApp) saveOptions() {
	if m.dataDir == "" {
		return
	}
	data, err := json.Marshal(m.options)
	if err != nil {
		return
	}
	if os.MkdirAll(m.dataDir, 0700) == nil {
		_ = os.WriteFile(filepath.Join(m.dataDir, "message-options.json"), data, 0600)
	}
}
func (m *mobileApp) setOptions(agent string, o remote.SendOptions) {
	if m.options == nil {
		m.options = map[string]remote.SendOptions{}
	}
	m.options[agent] = o
	m.saveOptions()
}
func (m *mobileApp) openOptions(agent string) { m.optionsAgent = agent; m.router().Push("/options") }

func (m *mobileApp) optionsView(c *ui.Context) {
	p := m.pal
	agent := m.optionsAgent
	a := m.agentOptions(agent)
	o := m.sendOptions(agent)
	ui.Row(c).FillWidth().Height(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		if m.circle(c, "back", "返回", func() { icon(c, "arrow-left", 20, p.ink) }).Clicked() {
			m.router().Back()
		}
		ui.Text(c, "对话设置").Grow(1).TextAlign(ui.Center).FontSize(16).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
			ui.Text(c, agentName(agent)).FontSize(20).FontWeight(600)
			ui.Text(c, "用于从手机发送的新消息，不修改电脑默认设置。").FontSize(13).TextColor(p.muted)
			if m.pill(c, "defaults", "使用电脑默认", false).Clicked() {
				delete(m.options, agent)
				m.saveOptions()
			}
			ui.Text(c, "模型").FontSize(13).TextColor(p.muted)
			ui.Column(c).FillWidth().Background(p.surface).Radius(16).Padding(6).Children(func() {
				for _, model := range a.Models {
					row := quiet(c.Key("model:" + model.Value)).FillWidth().MinHeight(44).Padding(10).Gap(8).Label("模型 " + model.Label)
					row.Children(func() {
						ui.Text(c, model.Label).Grow(1).MinWidth(0)
						if model.Value == o.Model {
							icon(c, "check", 18, p.ink)
						}
					})
					if row.Clicked() {
						o.Model, o.Effort = model.Value, model.DefaultEffort
						m.setOptions(agent, o)
					}
				}
				if len(a.Models) == 0 {
					ui.Text(c, "连接电脑后可选择模型").Padding(10).TextColor(p.muted)
				}
			})
			ui.Text(c, "推理强度").FontSize(13).TextColor(p.muted)
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				choices := []string{""}
				for _, model := range a.Models {
					if model.Value == o.Model {
						for _, e := range model.Efforts {
							if e != "" {
								choices = append(choices, e)
							}
						}
					}
				}
				for _, e := range choices {
					label := strings.ToUpper(e)
					if e == "" {
						label = "模型默认"
					}
					if m.pill(c, "effort:"+e, label, e == o.Effort).Label("强度 " + label).Clicked() {
						o.Effort = e
						m.setOptions(agent, o)
					}
				}
			})
			ui.Text(c, "权限模式").FontSize(13).TextColor(p.muted)
			for _, policy := range []struct{ value, label, detail string }{{"on-request", "需要时询问", "敏感操作由你确认"}, {"on-failure", "协助审批", "由自动审批审核处理，需要时再询问你"}, {"never", "完全访问", "允许 Agent 不经询问执行操作"}} {
				b := quiet(c.Key("policy:" + policy.value)).FillWidth().MinHeight(56).Padding(12).Gap(12).Background(p.surface).Radius(14).Label(policy.label)
				b.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, policy.label).FontWeight(500)
						ui.Text(c, policy.detail).FontSize(12).TextColor(p.muted)
					})
					if policy.value == o.Policy {
						icon(c, "check", 18, p.ink)
					}
				})
				if b.Clicked() {
					o.Policy = policy.value
					m.setOptions(agent, o)
				}
			}
		})
	})
}
