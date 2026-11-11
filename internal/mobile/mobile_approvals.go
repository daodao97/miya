package mobile

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/interaction"
	mcpaccess "miya/internal/mcp/access"
	phoneproto "miya/internal/phone/protocol"
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
	raw, err := json.Marshal(phoneproto.LegacyResponse(a, reply))
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
	r := remote.Request{Response: &reply, ApprovalVersion: phoneproto.ApprovalFingerprint(a), Op: "respond", Approval: a.ID, Result: raw, Allow: reply.Action == interaction.Allow, Remember: remember}
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
		if !textutil.SameJSON(a.ID, id) {
			out = append(out, a)
		}
	}
	m.State.Approvals = out
	m.Viewer.setApprovals(out)
	m.CloseResolvedDesktopControl(out)
	if m.Router().Path() == "/approval" && textutil.SameJSON(m.approvalID, id) {
		m.Router().Back()
	}
}

// requestLook is how a request introduces itself: an icon tinted by what
// it asks for, a title saying it, and who asks.
type requestLook struct {
	icon            string
	tint            ui.Color
	title, subtitle string
	view            appmodel.ApprovalView
}

func (m *App) lookOf(a interaction.Request) requestLook {
	p := m.Pal
	v := appmodel.DescribeApproval(m.Viewer.threads(), a)
	agent := widgets.RequestAgent(a)
	l := requestLook{icon: v.Icon, tint: p.warn, title: v.Title, subtitle: agent, view: v}
	if v.Thread != "" {
		l.subtitle += " · " + v.Thread
	}
	switch a.Kind() {
	case interaction.Questions:
		// The title already names the agent.
		l.icon, l.tint, l.title, l.subtitle = "message-circle", p.link, fmt.Sprintf(tr("%s has a question"), agent), v.Thread
		if len(a.Questions) > 1 {
			l.title = fmt.Sprintf(tr("%s has some questions"), agent)
		}
	case interaction.Form:
		l.icon, l.tint, l.title = "shield-check", p.link, tr("Confirm request")
		if a.Mode == "url" {
			l.icon, l.title = "arrow-right", tr("Continue in your browser")
		}
		if a.ServerName != "" {
			l.subtitle = a.ServerName
		}
	}
	return l
}

func (m *App) requestHeader(c *ui.Context, l requestLook, behind ui.Color) {
	p := m.Pal
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Size(34, 34).Shrink(0).Radius(10).Background(l.tint.Mix(behind, 0.84)).
			Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, l.icon, 19, l.tint)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, l.title).FillWidth().Wrap().MaxLines(2).FontSize(m.Font(16)).FontWeight(600)
			if l.subtitle != "" {
				ui.Text(c, l.subtitle).FontSize(m.Font(13)).TextColor(p.muted).SingleLine().Ellipsis("…")
			}
		})
	})
}

// approvalBody shows the agent's reason as prose, then exactly what would
// run or change in monospace, on a well a step lighter or darker than the
// surface it sits on.
func (m *App) approvalBody(c *ui.Context, v appmodel.ApprovalView, well ui.Color, compact bool) {
	p := m.Pal
	if v.Summary != "" {
		t := ui.Text(c, v.Summary).FillWidth().Wrap().FontSize(m.Font(15)).LineHeight(1.4).Selectable()
		if compact {
			t.MaxLines(4)
		}
	}
	code, changes := float32(140), float32(180)
	if !compact {
		code, changes = 320, 420
	}
	if v.Code != "" {
		m.codeBlock(c.Key("code"), v.Code, code, well)
	}
	if v.Changes != "" {
		m.codeBlock(c.Key("changes"), v.Changes, changes, well)
	}
	for i, f := range v.Facts {
		ui.Column(c.Key(fmt.Sprintf("fact-%d", i))).FillWidth().Gap(2).Children(func() {
			ui.Text(c, f.Label).FontSize(m.Font(12)).TextColor(p.muted)
			ui.Text(c, f.Value).FillWidth().Wrap().FontSize(m.Font(14)).Selectable()
		})
	}
}

func (m *App) codeBlock(c *ui.Context, text string, maxHeight float32, well ui.Color) {
	ui.Scroll(c).FillWidth().MaxHeight(maxHeight).Background(well).Radius(12).Children(func() {
		ui.Column(c).FillWidth().Padding(10, 12).Children(func() {
			ui.Text(c, text).FillWidth().Wrap().Font(widgets.MonoFont).FontSize(m.Font(13)).LineHeight(1.45).Selectable()
		})
	})
}

func (m *App) approvalCard(c *ui.Context, a interaction.Request) {
	p := m.Pal
	id := string(a.ID)
	l := m.lookOf(a)
	ui.Column(c).Margin(0, 12, 8, 12).Padding(16).Gap(12).Background(p.surface).Radius(20).Children(func() {
		m.requestHeader(c, l, p.surface)
		m.desktopControlAction(c, a)
		switch a.Kind() {
		case interaction.DesktopControl:
			if !m.State.DesktopWindows {
				m.approvalBody(c, l.view, p.Bg, true)
			}
			m.takeoverRequestActions(c, a)
		case interaction.Form:
			if inlineMCPConfirmation(a) {
				ui.Scroll(c).FillWidth().MaxHeight(120).Children(func() {
					ui.Text(c, a.Message).FillWidth().Wrap().FontSize(m.Font(15)).LineHeight(1.4).Selectable()
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
				ui.Text(c, summary).FillWidth().Wrap().FontSize(m.Font(15)).LineHeight(1.4).MaxLines(3)
			}
			if a.Kind() == interaction.Form {
				label = tr("Review and confirm")
			}
			if m.ApprovalButton(c, "open", label, true, false).FillWidth().Clicked() {
				m.approvalID = a.ID
				m.Router().Push("/approval")
			}
		default:
			m.approvalBody(c, l.view, p.Bg, true)
			pending := m.ApprovalPending[id]
			// The default action sits on the right, as in iOS alerts.
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				if m.ApprovalButton(c, "deny", tr("Deny"), false, pending).Grow(1).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Decline}, false)
				}
				if m.ApprovalButton(c, "allow", tr("Allow once"), true, pending).Grow(1).Clicked() {
					m.submitApproval(a, interaction.Response{Action: interaction.Allow}, false)
				}
			})
		}
		m.requestError(c, id)
	})
}

func (m *App) requestError(c *ui.Context, id string) {
	message := m.FormErrors[id]
	if message == "" {
		return
	}
	ui.Row(c.Key("error")).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
		widgets.Icon(c, "alert-circle", 16, widgets.Danger)
		ui.Text(c, message).Grow(1).MinWidth(0).Wrap().MaxLines(3).FontSize(m.Font(13)).TextColor(widgets.Danger)
	})
}

// Native window buttons act on the Mac. Completing or cancelling Miya's
// waiting request is separate and must remain possible even when the preview
// has no native actions, is still loading, or has become stale.
func (m *App) takeoverRequestActions(c *ui.Context, a interaction.Request) {
	pending := m.ApprovalPending[string(a.ID)]
	ui.Row(c.Key("takeover-request-actions")).FillWidth().Wrap().Gap(10).Children(func() {
		if m.ApprovalButton(c, "cancel", tr("Cancel takeover"), false, pending).Grow(1).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Cancel}, false)
		}
		if m.ApprovalButton(c, "done", tr("Done, continue"), true, pending).Grow(1).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Allow}, false)
		}
	})
}

// Confirmations with no input fields can be answered on the conversation card.
// URL requests and forms retain their full page and existing validation.
func inlineMCPConfirmation(a interaction.Request) bool {
	if a.Kind() != interaction.Form || a.Mode != "" && a.Mode != "form" {
		return false
	}
	var schema phoneproto.FormSchema
	if json.Unmarshal(a.RequestedSchema, &schema) != nil || schema.Type != "object" || len(schema.Properties) != 0 {
		return false
	}
	_, err := phoneproto.ValidateMobileForm(a, nil)
	return err == nil
}

func (m *App) approvalPage(c *ui.Context) {
	p := m.Pal
	var approval *interaction.Request
	for _, a := range m.State.Approvals {
		if textutil.SameJSON(a.ID, m.approvalID) {
			copy := a
			approval = &copy
			break
		}
	}
	title := tr("Confirm request")
	if approval != nil && approval.Kind() == interaction.Questions {
		title = tr("Enter your answer")
	}
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/approval")
		ui.Text(c, title).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	if approval == nil {
		ui.Text(c, tr("Request already handled or expired")).Margin(ui.Auto).TextColor(p.muted)
		return
	}
	a := *approval
	l := m.lookOf(a)
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(12, 16, 24, 16).Gap(20).Children(func() {
			m.requestHeader(c, l, p.Bg)
			m.desktopControlAction(c, a)
			switch {
			case a.Kind() == interaction.Questions:
				m.questionFields(c, a)
			case a.Kind() == interaction.Form:
				m.elicitationFields(c, a)
			case !m.State.DesktopWindows:
				m.approvalBody(c, l.view, p.surface, false)
			}
		})
	})
	ui.Column(c).FillWidth().Padding(12, 16).Gap(10).Background(p.Bg).Children(func() {
		m.requestError(c, string(a.ID))
		if m.ApprovalPending[string(a.ID)] {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, tr("Submitting…")).TextColor(p.muted).FontSize(m.Font(13))
			})
		}
		switch a.Kind() {
		case interaction.DesktopControl:
			m.takeoverRequestActions(c, a)
		case interaction.Questions:
			m.questionActions(c, a)
		default:
			m.elicitationActions(c, a)
		}
	})
}

type mobileChoice struct {
	label, detail string
	chosen        bool
}

// choiceList is an inset grouped list, as iOS settings draw choices: one
// row per answer, a checkmark on the chosen one. It returns the index of
// the row tapped, or -1.
func (m *App) choiceList(c *ui.Context, choices []mobileChoice) int {
	p := m.Pal
	tapped := -1
	ui.Column(c).FillWidth().Radius(14).Clip().Background(p.surface).Children(func() {
		for i, choice := range choices {
			if i > 0 {
				ui.Row(c.Key(fmt.Sprintf("divider-%d", i))).FillWidth().Padding(0, 0, 0, 16).Children(func() {
					ui.Box(c).FillWidth().Height(1).Background(p.line)
				})
			}
			b := m.phoneButton(c.Key(i)).FillWidth().MinHeight(50).Padding(11, 16).Radius(0).Gap(12).Label(choice.label)
			if b.Pressed() {
				b.Background(p.line)
			}
			b.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, choice.label).FillWidth().Wrap().FontSize(m.Font(16))
					if choice.detail != "" {
						ui.Text(c, choice.detail).FillWidth().Wrap().FontSize(m.Font(13)).LineHeight(1.35).TextColor(p.muted)
					}
				})
				if choice.chosen {
					widgets.Icon(c, "check", 19, p.link)
				} else {
					ui.Box(c).Size(19, 19).Shrink(0)
				}
			})
			if b.Clicked() {
				tapped = i
			}
		}
	})
	return tapped
}

func (m *App) questionFields(c *ui.Context, a interaction.Request) {
	p := m.Pal
	fields := m.FieldsFor(a)
	for i, q := range a.Questions {
		ui.Column(c.Key(q.ID)).FillWidth().Gap(10).Children(func() {
			var eyebrow []string
			if len(a.Questions) > 1 {
				eyebrow = append(eyebrow, fmt.Sprintf(tr("Question %d of %d"), i+1, len(a.Questions)))
			}
			if q.Header != "" && q.Header != q.Question {
				eyebrow = append(eyebrow, q.Header)
			}
			if len(eyebrow) > 0 {
				ui.Text(c, strings.Join(eyebrow, " · ")).FillWidth().Wrap().FontSize(m.Font(13)).FontWeight(500).TextColor(p.muted)
			}
			ui.Text(c, q.Question).FillWidth().Wrap().FontSize(m.Font(17)).FontWeight(600).LineHeight(1.35).Selectable()
			if len(q.Options) > 0 {
				choices := make([]mobileChoice, len(q.Options))
				for j, option := range q.Options {
					choices[j] = mobileChoice{option.Label, option.Description, fields[q.ID] == option.Label}
				}
				if j := m.choiceList(c.Key("options"), choices); j >= 0 {
					fields[q.ID] = q.Options[j].Label
				}
			}
			value := fields[q.ID]
			if q.HasOption(value) {
				// A chosen option is not also typed into the answer field.
				value = ""
			}
			placeholder := tr("Type your answer…")
			if len(q.Options) > 0 {
				placeholder = tr("You can also enter your own answer")
			}
			input := ui.TextInput(c.Key("answer"), &value).FillWidth().MinHeight(48).FontSize(m.Font(16)).Placeholder(placeholder).Label(textutil.FirstNonEmpty(q.Header, q.Question))
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
	submit, skip := tr("Submit answer"), tr("Skip question")
	if len(a.Questions) > 1 {
		skip = tr("Skip questions")
	}
	if m.ApprovalButton(c, "submit", submit, true, !complete || pending).FillWidth().Clicked() {
		answers := map[string]string{}
		for _, q := range a.Questions {
			answers[q.ID] = fields[q.ID]
		}
		m.submitApproval(a, interaction.Response{Action: interaction.Submit, Answers: answers}, false)
	}
	if m.textButton(c, "skip", skip, pending).Clicked() {
		m.submitApproval(a, interaction.Response{Action: interaction.Skip}, false)
	}
}

// textButton is a full-width borderless action, the quieter choice under a
// primary button.
func (m *App) textButton(c *ui.Context, key, label string, disabled bool) ui.Element {
	color := m.Pal.link
	if disabled {
		color = m.Pal.muted
	}
	b := m.phoneButton(c.Key(key)).FillWidth().MinHeight(44).Justify(ui.Center).Disabled(disabled).Label(label)
	b.Children(func() { ui.Text(c, label).FontSize(m.Font(15)).FontWeight(500).TextColor(color) })
	return b
}

func (m *App) elicitationFields(c *ui.Context, a interaction.Request) {
	p := m.Pal
	fields := m.FieldsFor(a)
	if a.Message != "" {
		ui.Text(c, a.Message).FillWidth().Wrap().FontSize(m.Font(15)).LineHeight(1.45).Selectable()
	}
	if a.Mode == "url" {
		ui.Text(c, tr("Complete the action in your browser, then return here to confirm.")).FillWidth().Wrap().FontSize(m.Font(13)).TextColor(p.muted)
		ui.Link(c, a.URL, a.URL).FontSize(m.Font(14))
		return
	}
	var schema phoneproto.FormSchema
	_ = json.Unmarshal(a.RequestedSchema, &schema)
	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := schema.Properties[key]
		name := textutil.FirstNonEmpty(field.Title, key)
		ui.Column(c.Key(key)).FillWidth().Gap(8).Children(func() {
			heading := name
			if slices.Contains(schema.Required, key) {
				heading += tr(" · Required")
			}
			ui.Text(c, heading).FillWidth().Wrap().FontSize(m.Font(15)).FontWeight(600)
			if field.Description != "" {
				ui.Text(c, field.Description).FillWidth().Wrap().FontSize(m.Font(13)).TextColor(p.muted)
			}
			switch {
			case len(field.Enum) > 0:
				choices := make([]mobileChoice, len(field.Enum))
				for i, raw := range field.Enum {
					var value any
					_ = json.Unmarshal(raw, &value)
					choices[i] = mobileChoice{label: fmt.Sprint(value), chosen: fields[key] == string(raw)}
				}
				if i := m.choiceList(c.Key("enum"), choices); i >= 0 {
					fields[key] = string(field.Enum[i])
				}
			case field.Type == "boolean":
				values := []string{"true", "false"}
				choices := []mobileChoice{{label: tr("Yes"), chosen: fields[key] == "true"}, {label: tr("No"), chosen: fields[key] == "false"}}
				if i := m.choiceList(c.Key("boolean"), choices); i >= 0 {
					fields[key] = values[i]
				}
			default:
				value := fields[key]
				if ui.TextInput(c.Key("value"), &value).FillWidth().MinHeight(48).FontSize(m.Font(16)).Label(name).Changed() {
					fields[key] = value
				}
			}
		})
	}
}

func (m *App) elicitationActions(c *ui.Context, a interaction.Request) {
	id := string(a.ID)
	fields := m.FieldsFor(a)
	pending := m.ApprovalPending[id]
	label := tr("Confirm")
	if mcpaccess.Rememberable(a) {
		label = tr("Allow once")
	}
	// The default action sits on the right, as in iOS alerts.
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		if m.ApprovalButton(c, "decline", tr("Deny"), false, pending).Grow(1).Clicked() {
			m.submitApproval(a, interaction.Response{Action: interaction.Decline}, false)
		}
		if m.ApprovalButton(c, "confirm", label, true, pending).Grow(1).Clicked() {
			result := interaction.Response{Action: interaction.Allow}
			if a.Mode != "url" {
				content, err := phoneproto.ValidateMobileForm(a, fields)
				if err != nil {
					m.FormErrors[id] = err.Error()
					return
				}
				result.Content = content
			}
			m.submitApproval(a, result, false)
		}
	})
	if mcpaccess.Rememberable(a) && m.ApprovalButton(c, "remember", tr("Always allow in this conversation"), false, pending).FillWidth().Clicked() {
		m.submitApproval(a, interaction.Response{Action: interaction.Allow}, true)
	}
	if m.textButton(c, "cancel", tr("Cancel request"), pending).Clicked() {
		m.submitApproval(a, interaction.Response{Action: interaction.Cancel}, false)
	}
}
