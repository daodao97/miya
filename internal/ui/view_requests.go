package desktopui

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	textutil "miya/internal/textutil"
	widgets "miya/internal/widgets"
	strings "strings"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) approvalView(c *ui.Context, a codex.Approval) {
	switch a.Method {
	case "item/tool/requestUserInput":
		app.questionView(c, a)
		return
	case "mcpServer/elicitation/request":
		app.elicitationView(c, a)
		return
	}
	ui.Column(c).Padding(14).Gap(8).Background(widgets.WarnBg).Border(1, widgets.WarnLine).Radius(10).Children(func() {
		ui.Text(c, widgets.RequestAgent(a)+" requests approval").FontWeight(600).FontSize(12)
		ui.Scroll(c).MaxHeight(180).Children(func() {
			ui.Text(c, app.C.ApprovalDetail(a)).FontSize(12).LineHeight(1.5).Selectable()
		})
		ui.Row(c).Gap(8).Children(func() {
			allowLabel, declineLabel := tr("Allow once"), tr("Decline")
			if a.Method == "miya/desktopControl" {
				allowLabel, declineLabel = tr("Done, continue"), tr("Cancel takeover")
			}
			if ui.PrimaryButton(c.Key("allow"), allowLabel).Clicked() {
				app.C.Respond(a, appmodel.ApprovalResult(a, true))
			}
			if ui.Button(c.Key("decline"), declineLabel).Clicked() {
				app.C.Respond(a, appmodel.ApprovalResult(a, false))
			}
		})
	})
}

func (app *App) questionView(c *ui.Context, a codex.Approval) {
	id := string(a.ID)
	answers := app.answers[id]
	if answers == nil {
		answers = map[string]string{}
		app.answers[id] = answers
	}
	widgets.Card(c).Children(func() {
		ui.Text(c, widgets.RequestAgent(a)+" has a question").FontWeight(600)
		ui.Scroll(c).MaxHeight(260).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				for _, q := range a.Questions {
					app.questionField(c.Key(q.ID), q, answers)
				}
			})
		})
		complete := true
		for _, q := range a.Questions {
			if strings.TrimSpace(answers[q.ID]) == "" {
				complete = false
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("submit"), tr("Submit answers")).Disabled(!complete).Clicked() {
				result := map[string]any{}
				for qid, value := range answers {
					result[qid] = map[string]any{"answers": []string{value}}
				}
				delete(app.answers, id)
				app.C.Respond(a, map[string]any{"answers": result})
			}
			if ui.Button(c.Key("skip"), tr("Skip questions")).Clicked() {
				delete(app.answers, id)
				app.C.Respond(a, map[string]any{"answers": map[string]any{}})
			}
		})
	})
}

func (app *App) questionField(c *ui.Context, q codex.Question, answers map[string]string) {
	ui.Column(c).Gap(6).Children(func() {
		if q.Header != "" && q.Header != q.Question {
			ui.Text(c, q.Header).FontSize(11).TextColor(widgets.Muted)
		}
		ui.Text(c, q.Question).FontSize(12).LineHeight(1.5)
		for i, o := range q.Options {
			b := widgets.Quiet(c.Key(i)).FillWidth().Height(0).MinHeight(32).Padding(6, 9)
			if answers[q.ID] == o.Label {
				b.Background(widgets.Chosen)
			}
			b.Children(func() {
				text := o.Label
				if o.Description != "" {
					text += " · " + o.Description
				}
				ui.Text(c, text).Grow(1).MinWidth(0)
			})
			if b.Clicked() {
				answers[q.ID] = o.Label
			}
		}
		value := answers[q.ID]
		in := ui.TextInput(c.Key("answer"), &value).Placeholder(tr("Type your answer…")).Label(textutil.FirstNonEmpty(q.Header, q.Question))
		if q.IsSecret {
			in.Password()
		}
		if in.Changed() {
			answers[q.ID] = value
		}
	})
}

func (app *App) elicitationView(c *ui.Context, a codex.Approval) {
	id := string(a.ID)
	rememberable := appmodel.RememberableMCPAccess(a)
	value, ok := app.forms[id]
	if !ok {
		value = "{}"
	}
	finish := func(result any) {
		delete(app.forms, id)
		delete(app.formErrors, id)
		app.C.Respond(a, result)
	}
	// MCP forms are submitted as structured JSON; never infer consent from defaults.
	widgets.Card(c).Children(func() {
		ui.Text(c, tr("MCP request · ")+a.ServerName).FontWeight(600)
		ui.Text(c, a.Message).FontSize(12).LineHeight(1.5).Selectable()
		if a.Mode == "url" {
			ui.Text(c, tr("Complete this request in your browser, then confirm:")).FontSize(12)
			ui.Link(c, a.URL, a.URL).FontSize(12)
		} else if !rememberable {
			ui.Scroll(c).MaxHeight(100).Children(func() {
				ui.Text(c, appmodel.JsonText(a.RequestedSchema)).Font(widgets.MonoFont).FontSize(11).Selectable()
			})
			if ui.TextArea(c.Key("form"), &value).Lines(3, 8).Font(widgets.MonoFont).FontSize(12).Label(tr("MCP form JSON")).Changed() {
				app.forms[id] = value
			}
		}
		if msg := app.formErrors[id]; msg != "" {
			ui.Text(c, msg).TextColor(widgets.Danger).FontSize(11)
		}
		if rememberable {
			ui.Text(c, tr("Allowed only in the current conversation. Permission must be granted again after the background service exits.")).FontSize(12).TextColor(widgets.Muted)
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			label := tr("Confirm")
			if rememberable {
				label = tr("Allow once")
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
			if ui.PrimaryButton(c.Key("confirm"), label).Clicked() {
				if a.Mode == "url" {
					finish(map[string]any{"action": "accept"})
					return
				}
				var content map[string]any
				if err := json.Unmarshal([]byte(value), &content); err != nil || content == nil {
					app.formErrors[id] = tr("Enter a JSON object matching the requested fields.")
					return
				}
				if err := appmodel.ValidateForm(a.RequestedSchema, content); err != nil {
					app.formErrors[id] = err.Error()
					return
				}
				finish(map[string]any{"action": "accept", "content": content})
			}
			if ui.Button(c.Key("decline"), tr("Decline")).Clicked() {
				finish(map[string]any{"action": "decline"})
			}
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				finish(map[string]any{"action": "cancel"})
			}
		})
	})
}
