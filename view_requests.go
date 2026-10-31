package main

import (
	"encoding/json"
	"strings"

	"miya/internal/codex"

	"github.com/egoist/mygo/ui"
)

func requestAgent(a codex.Approval) string { return approvalOwner(a).Name() }

func (app *chatApp) approvalView(c *ui.Context, a codex.Approval) {
	switch a.Method {
	case "item/tool/requestUserInput":
		app.questionView(c, a)
		return
	case "mcpServer/elicitation/request":
		app.elicitationView(c, a)
		return
	}
	ui.Column(c).Padding(14).Gap(8).Background(warnBg).Border(1, warnLine).Radius(10).Children(func() {
		ui.Text(c, requestAgent(a)+" requests approval").FontWeight(600).FontSize(12)
		ui.Scroll(c).MaxHeight(180).Children(func() {
			ui.Text(c, app.c.approvalDetail(a)).FontSize(12).LineHeight(1.5).Selectable()
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("allow"), "Allow once").Clicked() {
				app.c.respond(a, approvalResult(a, true))
			}
			if ui.Button(c.Key("decline"), "Decline").Clicked() {
				app.c.respond(a, approvalResult(a, false))
			}
		})
	})
}

func (app *chatApp) questionView(c *ui.Context, a codex.Approval) {
	id := string(a.ID)
	answers := app.answers[id]
	if answers == nil {
		answers = map[string]string{}
		app.answers[id] = answers
	}
	card(c).Children(func() {
		ui.Text(c, requestAgent(a)+" has a question").FontWeight(600)
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
			if ui.PrimaryButton(c.Key("submit"), "Submit answers").Disabled(!complete).Clicked() {
				result := map[string]any{}
				for qid, value := range answers {
					result[qid] = map[string]any{"answers": []string{value}}
				}
				delete(app.answers, id)
				app.c.respond(a, map[string]any{"answers": result})
			}
			if ui.Button(c.Key("skip"), "Skip questions").Clicked() {
				delete(app.answers, id)
				app.c.respond(a, map[string]any{"answers": map[string]any{}})
			}
		})
	})
}

func (app *chatApp) questionField(c *ui.Context, q codex.Question, answers map[string]string) {
	ui.Column(c).Gap(6).Children(func() {
		if q.Header != "" && q.Header != q.Question {
			ui.Text(c, q.Header).FontSize(11).TextColor(muted)
		}
		ui.Text(c, q.Question).FontSize(12).LineHeight(1.5)
		for i, o := range q.Options {
			b := quiet(c.Key(i)).FillWidth().Height(0).MinHeight(32).Padding(6, 9)
			if answers[q.ID] == o.Label {
				b.Background(chosen)
			}
			b.Children(func() {
				text := o.Label
				if o.Description != "" {
					text += " — " + o.Description
				}
				ui.Text(c, text).Grow(1).MinWidth(0)
			})
			if b.Clicked() {
				answers[q.ID] = o.Label
			}
		}
		value := answers[q.ID]
		in := ui.TextInput(c.Key("answer"), &value).Placeholder("Type your answer…").Label(firstNonEmpty(q.Header, q.Question))
		if q.IsSecret {
			in.Password()
		}
		if in.Changed() {
			answers[q.ID] = value
		}
	})
}

func (app *chatApp) elicitationView(c *ui.Context, a codex.Approval) {
	id := string(a.ID)
	rememberable := rememberableMCPAccess(a)
	value, ok := app.forms[id]
	if !ok {
		value = "{}"
	}
	finish := func(result any) {
		delete(app.forms, id)
		delete(app.formErrors, id)
		app.c.respond(a, result)
	}
	// MCP forms are submitted as structured JSON; never infer consent from defaults.
	card(c).Children(func() {
		ui.Text(c, "MCP request · "+a.ServerName).FontWeight(600)
		ui.Text(c, a.Message).FontSize(12).LineHeight(1.5).Selectable()
		if a.Mode == "url" {
			ui.Text(c, "Complete this request in your browser, then confirm:").FontSize(12)
			ui.Link(c, a.URL, a.URL).FontSize(12)
		} else if !rememberable {
			ui.Scroll(c).MaxHeight(100).Children(func() {
				ui.Text(c, jsonText(a.RequestedSchema)).Font(monoFont).FontSize(11).Selectable()
			})
			if ui.TextArea(c.Key("form"), &value).Lines(3, 8).Font(monoFont).FontSize(12).Label("MCP form JSON").Changed() {
				app.forms[id] = value
			}
		}
		if msg := app.formErrors[id]; msg != "" {
			ui.Text(c, msg).TextColor(danger).FontSize(11)
		}
		if rememberable {
			ui.Text(c, "始终允许仅对当前会话访问此应用生效，后台服务退出后失效。").FontSize(12).TextColor(muted)
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			label := "Confirm"
			if rememberable {
				label = "允许一次"
			}
			if rememberable && ui.Button(c.Key("always-allow"), "此会话始终允许").Clicked() {
				if err := app.c.respondMCPAlways(a); err != nil {
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
					app.formErrors[id] = "Enter a JSON object matching the requested fields."
					return
				}
				if err := validateForm(a.RequestedSchema, content); err != nil {
					app.formErrors[id] = err.Error()
					return
				}
				finish(map[string]any{"action": "accept", "content": content})
			}
			if ui.Button(c.Key("decline"), "Decline").Clicked() {
				finish(map[string]any{"action": "decline"})
			}
			if ui.Button(c.Key("cancel"), "Cancel").Clicked() {
				finish(map[string]any{"action": "cancel"})
			}
		})
	})
}
