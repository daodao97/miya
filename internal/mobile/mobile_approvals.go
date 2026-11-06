package mobile

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/interaction"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (m *App) FieldsFor(a interaction.Request) map[string]string {
	if m.ApprovalFields == nil {
		m.ApprovalFields = map[string]map[string]string{}
	}
	id := string(a.ID)
	if m.ApprovalFields[id] == nil {
		m.ApprovalFields[id] = map[string]string{}
	}
	return m.ApprovalFields[id]
}

func (m *App) submitApproval(a interaction.Request, reply interaction.Response, remember bool) {
	id := string(a.ID)
	if m.ApprovalPending[id] {
		return
	}
	if m.Conn == nil {
		m.FormErrors[id] = tr("Not connected to desktop")
		return
	}
	raw, err := json.Marshal(phoneapi.LegacyResponse(a, reply))
	if err != nil {
		m.FormErrors[id] = err.Error()
		return
	}
	if m.ApprovalPending == nil {
		m.ApprovalPending = map[string]bool{}
	}
	m.ApprovalPending[id] = true
	delete(m.FormErrors, id)
	conn, gen := m.Conn, m.Generation
	r := remote.Request{Response: &reply, ApprovalVersion: phoneapi.ApprovalFingerprint(a), Op: "respond", Approval: a.ID, Result: raw, Allow: reply.Action == interaction.Allow, Remember: remember}
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				delete(m.ApprovalPending, id)
				m.FormErrors[id] = tr("Failed to submit: ") + err.Error()
			})
		}
	}()
}

func (m *App) ApprovalAnswered(id json.RawMessage, message string) {
	key := string(id)
	delete(m.ApprovalPending, key)
	if message != "" {
		m.FormErrors[key] = message
		return
	}
	delete(m.ApprovalFields, key)
	delete(m.DesktopPreviews, key)
	delete(m.FormErrors, key)
	out := make([]interaction.Request, 0, len(m.State.Approvals))
	for _, a := range m.State.Approvals {
		if !phone.SameJSON(a.ID, id) {
			out = append(out, a)
		}
	}
	m.State.Approvals = out
	m.Viewer.setApprovals(out)
	m.CloseResolvedDesktopControl(out)
	if m.Router().Path() == "/approval" && phone.SameJSON(m.approvalID, id) {
		m.Router().Back()
	}
}

func (m *App) approvalCard(c *ui.Context, a interaction.Request) {
	p := m.Pal
	id := string(a.ID)
	ui.Column(c).Margin(0, 12, 8, 12).Padding(14).Gap(10).Background(p.surface).Border(1, p.warn).Radius(18).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, "shield-exclamation", 16, p.warn)
			label := widgets.RequestAgent(a) + tr(" requests confirmation")
			if a.Kind() == interaction.DesktopControl && m.State.DesktopWindows {
				label = tr("Needs your attention")
			}
			ui.Text(c, label).FontSize(m.Font(14)).FontWeight(600)
		})
		m.desktopControlAction(c, a)
		switch a.Kind() {
		case interaction.DesktopControl:
			if m.State.DesktopWindows {
				break
			}
			ui.Text(c, m.Viewer.approvalDetail(a)).FontSize(m.Font(13)).LineHeight(1.45)
			ui.Row(c).Gap(8).Children(func() {
				if m.pill(c, "done", tr("Done, continue"), true).Disabled(m.ApprovalPending[id]).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Allow}, false)
				}
				if m.pill(c, "cancel", tr("Cancel takeover"), false).Disabled(m.ApprovalPending[id]).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Decline}, false)
				}
			})
		case interaction.Form:
			if inlineMCPConfirmation(a) {
				ui.Scroll(c).FillWidth().MaxHeight(120).Children(func() {
					ui.Text(c, a.Message).FontSize(m.Font(15)).LineHeight(1.4).Selectable()
				})
				m.elicitationActions(c, a)
				break
			}
			fallthrough
		case interaction.Questions:
			label := tr("Enter your answer")
			summary := a.Message
			if len(a.Questions) > 0 {
				summary = a.Questions[0].Question
			}
			if summary != "" {
				ui.Text(c, summary).FontSize(m.Font(15)).LineHeight(1.4).MaxLines(3)
			}
			if a.Kind() == interaction.Form {
				label = tr("Review and confirm")
			}
			if m.pill(c, "open", label, true).Clicked() {
				m.approvalID = a.ID
				m.Router().Push("/approval")
			}
		default:
			ui.Scroll(c).MaxHeight(140).Children(func() { ui.Text(c, m.Viewer.approvalDetail(a)).FontSize(m.Font(13)).LineHeight(1.45).Selectable() })
			ui.Row(c).Gap(8).Children(func() {
				if m.ApprovalButton(c, "allow", tr("Allow once"), true, m.ApprovalPending[id]).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Allow}, false)
				}
				if m.ApprovalButton(c, "deny", tr("Deny"), false, m.ApprovalPending[id]).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Decline}, false)
				}
			})
		}
		if message := m.FormErrors[id]; message != "" {
			ui.Text(c, message).FontSize(m.Font(13)).TextColor(widgets.Danger)
		}
	})
}

// Confirmations with no input fields can be answered on the conversation card.
// URL requests and forms retain their full page and existing validation.
func inlineMCPConfirmation(a interaction.Request) bool {
	if a.Kind() != interaction.Form || a.Mode != "" && a.Mode != "form" {
		return false
	}
	var schema phoneapi.FormSchema
	if json.Unmarshal(a.RequestedSchema, &schema) != nil || schema.Type != "object" || len(schema.Properties) != 0 {
		return false
	}
	_, err := phoneapi.ValidateMobileForm(a, nil)
	return err == nil
}

func (m *App) approvalPage(c *ui.Context) {
	p := m.Pal
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/approval")
		ui.Text(c, tr("Confirm request")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	var approval *interaction.Request
	for _, a := range m.State.Approvals {
		if phone.SameJSON(a.ID, m.approvalID) {
			copy := a
			approval = &copy
			break
		}
	}
	if approval == nil {
		ui.Text(c, tr("Request already handled or expired")).Margin(ui.Auto).TextColor(p.muted)
		return
	}
	a := *approval
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(20, 16).Gap(16).Children(func() {
			ui.Text(c, widgets.RequestAgent(a)).FontSize(m.Font(20)).FontWeight(600)
			m.desktopControlAction(c, a)
			if a.Kind() == interaction.Questions {
				m.questionFields(c, a)
			} else {
				m.elicitationFields(c, a)
			}
		})
	})
	ui.Column(c).FillWidth().Padding(12, 16).Gap(8).Background(p.Bg).Children(func() {
		if message := m.FormErrors[string(a.ID)]; message != "" {
			ui.Text(c, message).TextColor(widgets.Danger).FontSize(m.Font(13)).MaxLines(2)
		}
		if m.ApprovalPending[string(a.ID)] {
			ui.Text(c, tr("Submitting…")).TextColor(p.muted).FontSize(m.Font(13))
		}
		if a.Kind() == interaction.Questions {
			m.questionActions(c, a)
		} else {
			m.elicitationActions(c, a)
		}
	})
}

func (m *App) questionFields(c *ui.Context, a interaction.Request) {
	p := m.Pal
	fields := m.FieldsFor(a)
	for _, q := range a.Questions {
		ui.Column(c.Key(q.ID)).FillWidth().Gap(8).Children(func() {
			if q.Header != "" {
				ui.Text(c, q.Header).FontWeight(600)
			}
			ui.Text(c, q.Question).FontSize(m.Font(15)).LineHeight(1.45)
			for i, option := range q.Options {
				b := m.phoneButton(c.Key(i)).FillWidth().MinHeight(44).Padding(12).Background(p.surface).Radius(12).Label(option.Label)
				b.Children(func() {
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						ui.Text(c, option.Label).FontWeight(500)
						if option.Description != "" {
							ui.Text(c, option.Description).FontSize(m.Font(13)).TextColor(p.muted)
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
			input := ui.TextInput(c.Key("answer"), &value).FillWidth().MinHeight(44).FontSize(m.Font(16)).Placeholder(tr("You can also enter your own answer")).Label(textutil.FirstNonEmpty(q.Header, q.Question))
			if q.IsSecret {
				input.Password()
			}
			if input.Changed() {
				fields[q.ID] = value
			}
		})
	}
}

func (m *App) questionActions(c *ui.Context, a interaction.Request) {
	fields := m.FieldsFor(a)
	complete := true
	for _, q := range a.Questions {
		if strings.TrimSpace(fields[q.ID]) == "" {
			complete = false
		}
	}
	pending := m.ApprovalPending[string(a.ID)]
	ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
		if m.pill(c, "submit", tr("Submit answer"), true).Disabled(!complete || pending).Clicked() {
			answers := map[string]string{}
			for _, q := range a.Questions {
				answers[q.ID] = fields[q.ID]
			}
			m.submitApproval(a, interaction.Response{Action: interaction.Submit, Answers: answers}, false)
		}
		if m.pill(c, "skip", tr("Skip question"), false).Disabled(pending).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Skip}, false)
		}
	})
}

func (m *App) elicitationFields(c *ui.Context, a interaction.Request) {
	p := m.Pal
	fields := m.FieldsFor(a)
	ui.Text(c, a.Message).FontSize(m.Font(15)).LineHeight(1.45).Selectable()
	if a.Mode == "url" {
		ui.Text(c, tr("Complete the action in your browser, then return here to confirm.")).FontSize(m.Font(13)).TextColor(p.muted)
		ui.Link(c, a.URL, a.URL).FontSize(m.Font(14))
	} else {
		var schema phoneapi.FormSchema
		_ = json.Unmarshal(a.RequestedSchema, &schema)
		keys := make([]string, 0, len(schema.Properties))
		for key := range schema.Properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			field := schema.Properties[key]
			ui.Column(c.Key(key)).FillWidth().Gap(8).Children(func() {
				label := textutil.FirstNonEmpty(field.Title, key)
				if slices.Contains(schema.Required, key) {
					label += tr(" · Required")
				}
				ui.Text(c, label).FontWeight(500)
				if field.Description != "" {
					ui.Text(c, field.Description).FontSize(m.Font(13)).TextColor(p.muted)
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
						for _, option := range []struct{ value, label string }{{"true", tr("Yes")}, {"false", tr("No")}} {
							if m.pill(c, option.value, option.label, fields[key] == option.value).Clicked() {
								fields[key] = option.value
							}
						}
					})
				default:
					value := fields[key]
					if ui.TextInput(c.Key("value"), &value).FillWidth().MinHeight(44).FontSize(m.Font(16)).Label(textutil.FirstNonEmpty(field.Title, key)).Changed() {
						fields[key] = value
					}
				}
			})
		}
	}
}

func (m *App) elicitationActions(c *ui.Context, a interaction.Request) {
	id := string(a.ID)
	fields := m.FieldsFor(a)
	pending := m.ApprovalPending[id]
	label := tr("Confirm")
	if mcpconfig.RememberableMCPAccess(a) {
		label = tr("Allow once")
	}
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		if m.ApprovalButton(c, "confirm", label, true, pending).Grow(1).Clicked() {
			result := interaction.Response{Action: interaction.Allow}
			if a.Mode != "url" {
				content, err := phoneapi.ValidateMobileForm(a, fields)
				if err != nil {
					m.FormErrors[id] = err.Error()
					return
				}
				result.Content = content
			}
			m.submitApproval(a, result, false)
		}
		if m.ApprovalButton(c, "decline", tr("Deny"), false, pending).Grow(1).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Decline}, false)
		}
	})
	ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
		if mcpconfig.RememberableMCPAccess(a) && m.ApprovalButton(c, "remember", tr("Always allow in this conversation"), false, pending).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Allow}, true)
		}
		if m.ApprovalButton(c, "cancel", tr("Cancel request"), false, pending).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Cancel}, false)
		}
	})
}
