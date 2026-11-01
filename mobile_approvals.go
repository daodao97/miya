package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/remote"
)

func (m *mobileApp) fieldsFor(a codex.Approval) map[string]string {
	if m.approvalFields == nil {
		m.approvalFields = map[string]map[string]string{}
	}
	id := string(a.ID)
	if m.approvalFields[id] == nil {
		m.approvalFields[id] = map[string]string{}
	}
	return m.approvalFields[id]
}
func (m *mobileApp) submitApproval(a codex.Approval, result any, allow, remember bool) {
	id := string(a.ID)
	if m.approvalPending[id] {
		return
	}
	if m.conn == nil {
		m.view.formErrors[id] = "未连接桌面端"
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		m.view.formErrors[id] = err.Error()
		return
	}
	if m.approvalPending == nil {
		m.approvalPending = map[string]bool{}
	}
	m.approvalPending[id] = true
	delete(m.view.formErrors, id)
	conn, gen := m.conn, m.generation
	r := remote.Request{ApprovalVersion: approvalFingerprint(a), Op: "respond", Approval: a.ID, Result: raw, Allow: allow, Remember: remember}
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { delete(m.approvalPending, id); m.view.formErrors[id] = "提交失败：" + err.Error() })
		}
	}()
}
func (m *mobileApp) approvalAnswered(id json.RawMessage, message string) {
	key := string(id)
	delete(m.approvalPending, key)
	if message != "" {
		m.view.formErrors[key] = message
		return
	}
	delete(m.approvalFields, key)
	delete(m.view.formErrors, key)
	out := make([]codex.Approval, 0, len(m.state.Approvals))
	for _, a := range m.state.Approvals {
		if !sameJSON(a.ID, id) {
			out = append(out, a)
		}
	}
	m.state.Approvals = out
	m.view.c.setApprovals(out)
	if m.router().Path() == "/approval" && sameJSON(m.approvalID, id) {
		m.router().Back()
	}
}
func (m *mobileApp) approvalCard(c *ui.Context, a codex.Approval) {
	p := m.pal
	id := string(a.ID)
	ui.Column(c).Margin(0, 12, 8, 12).Padding(14).Gap(10).Background(p.surface).Border(1, p.warn).Radius(18).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-exclamation", 16, p.warn)
			ui.Text(c, requestAgent(a)+" 请求确认").FontSize(m.font(14)).FontWeight(600)
		})
		switch a.Method {
		case "item/tool/requestUserInput", "mcpServer/elicitation/request":
			label := "填写回答"
			if a.Method == "mcpServer/elicitation/request" {
				label = "处理表单"
			}
			if m.pill(c, "open", label, true).Clicked() {
				m.approvalID = a.ID
				m.router().Push("/approval")
			}
		default:
			ui.Scroll(c).MaxHeight(140).Children(func() { ui.Text(c, m.view.c.approvalDetail(a)).FontSize(m.font(13)).LineHeight(1.45).Selectable() })
			ui.Row(c).Gap(8).Children(func() {
				if m.pill(c, "allow", "允许一次", true).Disabled(m.approvalPending[id]).Clicked() {
					m.submitApproval(a, nil, true, false)
				}
				if m.pill(c, "deny", "拒绝", false).Disabled(m.approvalPending[id]).Clicked() {
					m.submitApproval(a, nil, false, false)
				}
			})
		}
		if message := m.view.formErrors[id]; message != "" {
			ui.Text(c, message).FontSize(m.font(13)).TextColor(danger)
		}
	})
}
func (m *mobileApp) approvalPage(c *ui.Context) {
	p := m.pal
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		if m.circle(c, "back", "返回", func() { icon(c, "arrow-left", 20, p.ink) }).Clicked() {
			m.router().Back()
		}
		ui.Text(c, "确认请求").Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	var approval *codex.Approval
	for _, a := range m.state.Approvals {
		if sameJSON(a.ID, m.approvalID) {
			copy := a
			approval = &copy
			break
		}
	}
	if approval == nil {
		ui.Text(c, "请求已处理或已过期").Margin(ui.Auto).TextColor(p.muted)
		return
	}
	a := *approval
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(20, 16).Gap(16).Children(func() {
			ui.Text(c, requestAgent(a)).FontSize(m.font(20)).FontWeight(600)
			if a.Method == "item/tool/requestUserInput" {
				m.questionFields(c, a)
			} else {
				m.elicitationFields(c, a)
			}
			if message := m.view.formErrors[string(a.ID)]; message != "" {
				ui.Text(c, message).TextColor(danger).FontSize(m.font(13))
			}
			if m.approvalPending[string(a.ID)] {
				ui.Text(c, "正在提交…").TextColor(p.muted).FontSize(m.font(13))
			}
		})
	})
}
func (m *mobileApp) questionFields(c *ui.Context, a codex.Approval) {
	p := m.pal
	fields := m.fieldsFor(a)
	complete := true
	for _, q := range a.Questions {
		ui.Column(c.Key(q.ID)).FillWidth().Gap(8).Children(func() {
			if q.Header != "" {
				ui.Text(c, q.Header).FontWeight(600)
			}
			ui.Text(c, q.Question).FontSize(m.font(15)).LineHeight(1.45)
			for i, option := range q.Options {
				b := m.phoneButton(c.Key(i)).FillWidth().MinHeight(44).Padding(12).Background(p.surface).Radius(12).Label(option.Label)
				b.Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						ui.Text(c, option.Label).FontWeight(500)
						if option.Description != "" {
							ui.Text(c, option.Description).FontSize(m.font(13)).TextColor(p.muted)
						}
					})
				})
				if fields[q.ID] == option.Label {
					b.Border(1, p.ink)
				}
				if b.Clicked() {
					fields[q.ID] = option.Label
				}
			}
			value := fields[q.ID]
			input := ui.TextInput(c.Key("answer"), &value).FillWidth().MinHeight(44).FontSize(m.font(16)).Placeholder("也可以输入自己的回答").Label(firstNonEmpty(q.Header, q.Question))
			if q.IsSecret {
				input.Password()
			}
			if input.Changed() {
				fields[q.ID] = value
			}
		})
		if strings.TrimSpace(fields[q.ID]) == "" {
			complete = false
		}
	}
	pending := m.approvalPending[string(a.ID)]
	ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
		if m.pill(c, "submit", "提交回答", true).Disabled(!complete || pending).Clicked() {
			answers := map[string]any{}
			for _, q := range a.Questions {
				answers[q.ID] = map[string]any{"answers": []string{fields[q.ID]}}
			}
			m.submitApproval(a, map[string]any{"answers": answers}, false, false)
		}
		if m.pill(c, "skip", "跳过问题", false).Disabled(pending).Clicked() {
			m.submitApproval(a, map[string]any{"answers": map[string]any{}}, false, false)
		}
	})
}
func (m *mobileApp) elicitationFields(c *ui.Context, a codex.Approval) {
	p := m.pal
	id := string(a.ID)
	fields := m.fieldsFor(a)
	ui.Text(c, a.Message).FontSize(m.font(15)).LineHeight(1.45).Selectable()
	if a.Mode == "url" {
		ui.Text(c, "先在浏览器完成操作，再回到这里确认。").FontSize(m.font(13)).TextColor(p.muted)
		ui.Link(c, a.URL, a.URL).FontSize(m.font(14))
	} else {
		var schema phoneFormSchema
		_ = json.Unmarshal(a.RequestedSchema, &schema)
		keys := make([]string, 0, len(schema.Properties))
		for key := range schema.Properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			field := schema.Properties[key]
			ui.Column(c.Key(key)).FillWidth().Gap(8).Children(func() {
				label := firstNonEmpty(field.Title, key)
				if slices.Contains(schema.Required, key) {
					label += " · 必填"
				}
				ui.Text(c, label).FontWeight(500)
				if field.Description != "" {
					ui.Text(c, field.Description).FontSize(m.font(13)).TextColor(p.muted)
				}
				switch {
				case len(field.Enum) > 0:
					for i, raw := range field.Enum {
						var value any
						_ = json.Unmarshal(raw, &value)
						if m.pill(c, fmt.Sprint(i), fmt.Sprint(value), fields[key] == string(raw)).Clicked() {
							fields[key] = string(raw)
						}
					}
				case field.Type == "boolean":
					ui.Row(c).Gap(10).Children(func() {
						for _, option := range []struct{ value, label string }{{"true", "是"}, {"false", "否"}} {
							if m.pill(c, option.value, option.label, fields[key] == option.value).Clicked() {
								fields[key] = option.value
							}
						}
					})
				default:
					value := fields[key]
					if ui.TextInput(c.Key("value"), &value).FillWidth().MinHeight(44).FontSize(m.font(16)).Label(firstNonEmpty(field.Title, key)).Changed() {
						fields[key] = value
					}
				}
			})
		}
	}
	pending := m.approvalPending[id]
	ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
		if rememberableMCPAccess(a) && m.pill(c, "remember", "此对话始终允许", false).Disabled(pending).Clicked() {
			m.submitApproval(a, nil, false, true)
		}
		if m.pill(c, "confirm", "确认", true).Disabled(pending).Clicked() {
			result := map[string]any{"action": "accept"}
			if a.Mode != "url" {
				content, err := validateMobileForm(a, fields)
				if err != nil {
					m.view.formErrors[id] = err.Error()
					return
				}
				result["content"] = content
			}
			m.submitApproval(a, result, false, false)
		}
		if m.pill(c, "decline", "拒绝", false).Disabled(pending).Clicked() {
			m.submitApproval(a, map[string]any{"action": "decline"}, false, false)
		}
		if m.pill(c, "cancel", "取消请求", false).Disabled(pending).Clicked() {
			m.submitApproval(a, map[string]any{"action": "cancel"}, false, false)
		}
	})
}
