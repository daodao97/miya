package desktopui

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
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// Pending requests sit above the composer as cards of one shape: a tinted
// icon and a title saying what is asked, who asks, the details, then the
// buttons at the bottom right with the default action last, as macOS
// dialogs place them.

func (app *App) approvalView(c *ui.Context, a interaction.Request) {
	switch a.Kind() {
	case interaction.Questions:
		app.questionView(c, a)
		return
	case interaction.Form:
		app.elicitationView(c, a)
		return
	}
	v := app.C.DescribeApproval(a)
	subtitle := widgets.RequestAgent(a)
	if v.Thread != "" {
		subtitle += " · " + v.Thread
	}
	requestCard(c, v.Icon, widgets.WarnInk, v.Title, subtitle, func() {
		approvalBody(c, v)
	}, func() {
		allow, deny := tr("Allow once"), tr("Deny")
		if a.Kind() == interaction.DesktopControl {
			allow, deny = tr("Done, continue"), tr("Cancel takeover")
		}
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("decline"), deny).Clicked() {
			app.C.Answer(a, interaction.Response{Action: interaction.Decline})
		}
		if ui.PrimaryButton(c.Key("allow"), allow).Clicked() {
			app.C.Answer(a, interaction.Response{Action: interaction.Allow})
		}
	})
}

// approvalBody puts the agent's reason first, as prose, then exactly what
// would run or change, in monospace so it reads and copies verbatim.
func approvalBody(c *ui.Context, v appmodel.ApprovalView) {
	if v.Summary != "" {
		ui.Text(c, v.Summary).FillWidth().Wrap().FontSize(13).LineHeight(1.45).Selectable()
	}
	if v.Code != "" {
		codeBlock(c.Key("code"), v.Code, 160)
	}
	if v.Changes != "" {
		codeBlock(c.Key("changes"), v.Changes, 220)
	}
	if len(v.Facts) > 0 {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			for i, f := range v.Facts {
				ui.Row(c.Key(i)).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
					ui.Text(c, f.Label).FontSize(12).TextColor(widgets.Muted).MinWidth(96).Shrink(0)
					ui.Text(c, f.Value).FontSize(12).Grow(1).MinWidth(0).Wrap().Selectable()
				})
			}
		})
	}
}

func requestCard(c *ui.Context, icon string, tint ui.Color, title, subtitle string, body, footer func()) {
	ui.Column(c).FillWidth().Padding(14, 16).Gap(12).Background(widgets.Panel).Border(1, widgets.Border).Radius(12).
		Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.06)).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Size(28, 28).Shrink(0).Radius(7).Background(tint.Mix(widgets.Panel, 0.86)).
				Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				widgets.Icon(c, icon, 16, tint)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(13).FontWeight(600).SingleLine().Ellipsis("…")
				if subtitle != "" {
					ui.Text(c, subtitle).FontSize(11).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
				}
			})
		})
		body()
		ui.Row(c.Key("actions")).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(footer)
	})
}

func codeBlock(c *ui.Context, text string, maxHeight float32) {
	ui.Scroll(c).FillWidth().MaxHeight(maxHeight).Background(widgets.Subtle).Border(1, widgets.Line).Radius(8).Children(func() {
		ui.Column(c).FillWidth().Padding(8, 10).Children(func() {
			ui.Text(c, text).FillWidth().Wrap().Font(widgets.MonoFont).FontSize(12).LineHeight(1.45).Selectable()
		})
	})
}

// choiceRow is one answer of a question or form: a radio mark, the label
// and its description, the whole row the target.
func choiceRow(c *ui.Context, label, detail string, chosen bool) ui.Element {
	b := ui.ButtonBase(c).Row().FillWidth().AlignItems(ui.Start).Gap(10).Padding(8, 10).Radius(8).
		Border(1, widgets.Line).Cursor(ui.CursorPointer).Label(label)
	switch {
	case chosen:
		b.Border(1, widgets.LinkInk).Background(widgets.LinkInk.Mix(widgets.Panel, 0.92))
	case b.Hovered():
		b.Background(widgets.Hover)
	}
	b.Children(func() {
		mark := widgets.Faint
		if chosen {
			mark = widgets.LinkInk
		}
		ui.Row(c).Size(16, 16).Shrink(0).Margin(1, 0, 0, 0).Radius(8).Border(1, mark).
			Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if chosen {
				ui.Box(c).Size(8, 8).Radius(4).Background(widgets.LinkInk)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(13).FontWeight(500).FillWidth().Wrap()
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(widgets.Muted).LineHeight(1.4).FillWidth().Wrap()
			}
		})
	})
	return b
}

func (app *App) questionView(c *ui.Context, a interaction.Request) {
	id := string(a.ID)
	answers := app.answers[id]
	if answers == nil {
		answers = map[string]string{}
		app.answers[id] = answers
	}
	agent := widgets.RequestAgent(a)
	title := fmt.Sprintf(tr("%s has a question"), agent)
	submit, skip := tr("Submit answer"), tr("Skip question")
	if len(a.Questions) > 1 {
		title = fmt.Sprintf(tr("%s has some questions"), agent)
		submit, skip = tr("Submit answers"), tr("Skip questions")
	}
	answered := 0
	for _, q := range a.Questions {
		if strings.TrimSpace(answers[q.ID]) != "" {
			answered++
		}
	}
	complete := answered == len(a.Questions)
	requestCard(c, "message-circle", widgets.LinkInk, title, app.C.DescribeApproval(a).Thread, func() {
		ui.Scroll(c.Key("questions")).FillWidth().MaxHeight(340).Children(func() {
			ui.Column(c).FillWidth().Gap(18).Children(func() {
				for _, q := range a.Questions {
					app.questionField(c.Key(q.ID), q, answers)
				}
			})
		})
	}, func() {
		if len(a.Questions) > 1 {
			ui.Text(c, fmt.Sprintf(tr("%d of %d answered"), answered, len(a.Questions))).FontSize(12).TextColor(widgets.Muted)
		}
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("skip"), skip).Clicked() {
			delete(app.answers, id)
			app.C.Answer(a, interaction.Response{Action: interaction.Skip})
		}
		if ui.PrimaryButton(c.Key("submit"), submit).Disabled(!complete).Clicked() {
			delete(app.answers, id)
			app.C.Answer(a, interaction.Response{Action: interaction.Submit, Answers: answers})
		}
	})
}

func (app *App) questionField(c *ui.Context, q interaction.Question, answers map[string]string) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		if q.Header != "" && q.Header != q.Question {
			ui.Text(c, q.Header).FontSize(11).FontWeight(600).TextColor(widgets.Muted)
		}
		ui.Text(c, q.Question).FillWidth().Wrap().FontSize(13).FontWeight(500).LineHeight(1.45).Selectable()
		if len(q.Options) > 0 {
			ui.Column(c.Key("options")).FillWidth().Gap(6).Children(func() {
				for i, o := range q.Options {
					if choiceRow(c.Key(i), o.Label, o.Description, answers[q.ID] == o.Label).Clicked() {
						answers[q.ID] = o.Label
					}
				}
			})
		}
		value := answers[q.ID]
		if q.HasOption(value) {
			// A chosen option is not also typed into the answer field.
			value = ""
		}
		placeholder := tr("Type your answer…")
		if len(q.Options) > 0 {
			placeholder = tr("You can also enter your own answer")
		}
		in := ui.TextInput(c.Key("answer"), &value).FillWidth().Placeholder(placeholder).Label(textutil.FirstNonEmpty(q.Header, q.Question))
		if q.IsSecret {
			in.Password()
		}
		if in.Changed() {
			answers[q.ID] = value
		}
	})
}

func (app *App) elicitationView(c *ui.Context, a interaction.Request) {
	id := string(a.ID)
	rememberable := mcpaccess.Rememberable(a)
	fields := app.forms[id]
	if fields == nil {
		fields = map[string]string{}
		app.forms[id] = fields
	}
	finish := func(result interaction.Response) {
		delete(app.forms, id)
		delete(app.formErrors, id)
		app.C.Answer(a, result)
	}
	var schema phoneproto.FormSchema
	_ = json.Unmarshal(a.RequestedSchema, &schema)
	icon, title := "shield-check", tr("Confirm request")
	switch {
	case a.Mode == "url":
		icon, title = "arrow-right", tr("Continue in your browser")
	case len(schema.Properties) > 0:
		title = fmt.Sprintf(tr("%s needs some information"), textutil.FirstNonEmpty(a.ServerName, widgets.RequestAgent(a)))
	}
	subtitle := a.ServerName
	if subtitle == "" {
		subtitle = widgets.RequestAgent(a)
	}
	// MCP forms are submitted as structured content; never infer consent from defaults.
	requestCard(c, icon, widgets.LinkInk, title, subtitle, func() {
		if a.Message != "" {
			ui.Text(c, a.Message).FillWidth().Wrap().FontSize(13).LineHeight(1.45).Selectable()
		}
		if a.Mode == "url" {
			widgets.MutedText(c, tr("Complete this request in your browser, then confirm:")).FillWidth().Wrap()
			ui.Link(c, a.URL, a.URL).FontSize(12)
		} else {
			formFields(c.Key("fields"), schema, fields)
		}
		if rememberable {
			widgets.MutedText(c, tr("Allowed only in the current conversation. Permission must be granted again after the background service exits.")).FillWidth().Wrap()
		}
		if msg := app.formErrors[id]; msg != "" {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
				widgets.Icon(c, "alert-circle", 14, widgets.Danger)
				ui.Text(c, msg).TextColor(widgets.Danger).FontSize(12).Grow(1).MinWidth(0).Wrap()
			})
		}
	}, func() {
		if widgets.Quiet(c.Key("cancel")).Label(tr("Cancel request")).Children(func() {
			ui.Text(c, tr("Cancel request")).TextColor(widgets.Muted)
		}).Clicked() {
			finish(interaction.Response{Action: interaction.Cancel})
		}
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("decline"), tr("Deny")).Clicked() {
			finish(interaction.Response{Action: interaction.Decline})
		}
		if rememberable && ui.Button(c.Key("always-allow"), tr("Always allow in this conversation")).Clicked() {
			if err := app.C.RespondMCPAlways(a); err != nil {
				app.formErrors[id] = err.Error()
			} else {
				delete(app.forms, id)
				delete(app.formErrors, id)
			}
			return
		}
		label := tr("Confirm")
		if rememberable {
			label = tr("Allow once")
		}
		if ui.PrimaryButton(c.Key("confirm"), label).Clicked() {
			if a.Mode == "url" {
				finish(interaction.Response{Action: interaction.Allow})
				return
			}
			content, err := phoneproto.ValidateMobileForm(a, fields)
			if err != nil {
				app.formErrors[id] = err.Error()
				return
			}
			finish(interaction.Response{Action: interaction.Submit, Content: content})
		}
	})
}

// formFields draws an MCP form's fields from its schema: choices for enums
// and booleans, text for the rest. Values stay as typed; validation against
// the schema happens on confirm, as on the phone.
func formFields(c *ui.Context, schema phoneproto.FormSchema, fields map[string]string) {
	if len(schema.Properties) == 0 {
		return
	}
	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		for _, key := range keys {
			field := schema.Properties[key]
			name := textutil.FirstNonEmpty(field.Title, key)
			ui.Column(c.Key(key)).FillWidth().Gap(6).Children(func() {
				heading := name
				if slices.Contains(schema.Required, key) {
					heading += tr(" · Required")
				}
				ui.Text(c, heading).FillWidth().Wrap().FontSize(12).FontWeight(600)
				if field.Description != "" {
					widgets.MutedText(c, field.Description).FillWidth().Wrap()
				}
				switch {
				case len(field.Enum) > 0:
					for i, raw := range field.Enum {
						var value any
						_ = json.Unmarshal(raw, &value)
						if choiceRow(c.Key(i), fmt.Sprint(value), "", fields[key] == string(raw)).Clicked() {
							fields[key] = string(raw)
						}
					}
				case field.Type == "boolean":
					for _, option := range []struct{ value, label string }{{"true", tr("Yes")}, {"false", tr("No")}} {
						if choiceRow(c.Key(option.value), option.label, "", fields[key] == option.value).Clicked() {
							fields[key] = option.value
						}
					}
				default:
					value := fields[key]
					if ui.TextInput(c.Key("value"), &value).FillWidth().Label(name).Changed() {
						fields[key] = value
					}
				}
			})
		}
	})
}
