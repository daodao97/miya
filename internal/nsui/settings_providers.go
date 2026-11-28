package nsui

import (
	"context"
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/agent/provider"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

func providerSettings(cx *core.Scope, c Host) core.Node {
	editing := core.NewSignal[*provider.View](cx, nil)
	errText := core.NewSignal(cx, "")
	saving := core.NewSignal(cx, false)
	call := func(op string, args any, local func() error, success func()) {
		finish := func(err error) {
			saving.Set(false)
			if err != nil {
				errText.Set(err.Error())
				return
			}
			errText.Set("")
			if success != nil {
				success()
			}
		}
		if remote := c.State().Remote; remote != nil {
			saving.Set(true)
			go func() {
				err := remote.Call(op, args, nil)
				cx.Post(func() { finish(err) })
			}()
			return
		}
		finish(local())
	}
	return settingsColumn(
		settingsText(cx, errText.Get),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if draft := editing.Get(); draft != nil {
				return providerForm(cx, c, *draft, saving.Get, func(edit provider.Edit) {
					call("providerSave", edit, func() error { return c.SaveProvider(edit) }, func() { editing.Set(nil) })
				}, func() { errText.Set(""); editing.Set(nil) })
			}
			views := Live(c.ProviderViews)()
			use := Live(c.ProviderUse)()
			nodes := []core.Node{settingsFormActions(ui.Button{TestID: "provider-new", Text: tr("Add provider"), Icon: nswidgets.IconOf("plus"),
				OnPress: func() { errText.Set(""); editing.Set(&provider.View{}) }, Disabled: saving.Get})}
			if len(views) == 0 {
				nodes = append(nodes, settingsText(cx, func() string {
					return tr("No providers yet. Add an API key and base URL to use them in Claude Code, Codex or Pi.")
				}))
			}
			for _, v := range views {
				body := []core.Node{
					settingsSectionTitle(v.Name),
					settingsText(cx, func() string { return strings.Join(v.Models, ", ") }),
				}
				var bindings []core.Node
				for _, agent := range provider.Agents {
					if provider.Supports(agent, v.Endpoints) {
						bindings = append(bindings, ui.FormRow{Title: model.AgentName(agent), Control: settingsToggle("provider-"+v.ID+"-"+agent, model.AgentName(agent),
							func() bool { return use[agent] == v.ID }, func(on bool) {
								id := v.ID
								if !on {
									id = ""
								}
								call("providerUse", provider.Use{Agent: agent, ID: id},
									func() error { return c.EnableProvider(agent, v.ID, on) }, nil)
							}, saving.Get)})
					}
				}
				if len(bindings) > 0 {
					body = append(body, ui.FormSection{Children: bindings})
				}
				confirm := core.NewSignal(cx, false)
				body = append(body, settingsFormActions(
					settingsButton("provider-edit-"+v.ID, tr("Edit"), func() { editing.Set(&v) }, saving.Get),
					settingsButton("provider-delete-"+v.ID, tr("Delete"), func() { confirm.Set(true) }, saving.Get),
				), core.Show{When: confirm.Get, Then: func(cx *core.Scope) core.Node {
					return settingsFormActions(
						settingsButton("", tr("Cancel"), func() { confirm.Set(false) }, saving.Get),
						ui.Button{TestID: "provider-confirm-" + v.ID, Text: tr("Confirm deletion"), Variant: ui.ButtonDanger, Disabled: saving.Get, OnPress: func() {
							call("providerDelete", v.ID, func() error { return c.DeleteProvider(v.ID) }, func() { confirm.Set(false) })
						}},
					)
				}})
				nodes = append(nodes, autoCard(cx, body...))
			}
			return settingsColumn(nodes...)
		}),
	)
}

func providerForm(cx *core.Scope, c Host, v provider.View, saving func() bool, save func(provider.Edit), cancel func()) core.Node {
	name := core.NewSignal(cx, v.Name)
	key := core.NewSignal(cx, "")
	models := core.NewSignal(cx, strings.Join(v.Models, "\n"))
	busy := core.NewSignal(cx, false)
	message := core.NewSignal(cx, "")
	endpoints := map[provider.API]*core.Signal[string]{}
	keyHint := ""
	title := tr("Add provider")
	if v.ID != "" {
		title = tr("Edit provider")
	}
	if v.KeyHint != "" {
		keyHint = fmt.Sprintf(tr("Leave blank to keep ••••%s"), v.KeyHint)
	}
	disabled := func() bool { return busy.Get() || saving() }
	nodes := []core.Node{
		settingsSectionTitle(title),
		settingsInlineField(tr("Name"), ui.Input{TestID: "provider-name", Label: tr("Name"), Placeholder: tr("My provider"), Value: name.Get, OnChange: name.Set, Disabled: disabled}),
		settingsInlineField(tr("API key"), ui.Input{TestID: "provider-key", Label: tr("API key"), Placeholder: keyHint, Masked: true, Value: key.Get, OnChange: key.Set, Disabled: disabled}),
	}
	for _, api := range provider.APIs {
		value := ""
		for _, e := range v.Endpoints {
			if e.API == api {
				value = e.BaseURL
			}
		}
		endpoints[api] = core.NewSignal(cx, value)
		label := tr("Anthropic Messages")
		switch api {
		case provider.Responses:
			label = tr("OpenAI Responses")
		case provider.Chat:
			label = tr("OpenAI Chat Completions")
		}
		nodes = append(nodes, settingsInlineField(label, ui.Input{TestID: "provider-endpoint-" + string(api), Label: label,
			Placeholder: "https://", Value: endpoints[api].Get, OnChange: endpoints[api].Set, Disabled: disabled}))
	}
	edit := func() provider.Edit {
		result := provider.Edit{ID: v.ID, Name: name.Get(), APIKey: key.Get()}
		for _, api := range provider.APIs {
			if url := strings.TrimSpace(endpoints[api].Get()); url != "" {
				result.Endpoints = append(result.Endpoints, provider.Endpoint{API: api, BaseURL: url})
			}
		}
		for _, id := range strings.Split(models.Get(), "\n") {
			if id := strings.TrimSpace(id); id != "" {
				result.Models = append(result.Models, id)
			}
		}
		return result
	}
	fetch := func() {
		run := c.ProviderModelsFetch(edit())
		if run == nil {
			return
		}
		busy.Set(true)
		ctx, stop := context.WithCancel(context.Background())
		cx.Cleanup(stop)
		go func() {
			defer stop()
			found, err := run(ctx)
			cx.Post(func() {
				busy.Set(false)
				if err != nil {
					message.Set(err.Error())
					return
				}
				message.Set("")
				models.Set(strings.Join(found, "\n"))
			})
		}()
	}
	nodes = append(nodes,
		settingsField(tr("Models"), ui.Input{TestID: "provider-models", Label: tr("Models"), Placeholder: tr("One model ID per line; the first is the default"),
			Multiline: true, Value: models.Get, OnChange: models.Set, Disabled: disabled}),
		settingsText(cx, message.Get),
		settingsFormActions(
			ui.Button{TestID: "provider-fetch", Text: tr("Fetch models"), Variant: ui.ButtonSecondary, OnPress: fetch, Disabled: saving, Busy: busy.Get},
			settingsButton("provider-cancel", tr("Cancel"), cancel, saving),
			ui.Button{TestID: "provider-save", Text: tr("Save"), OnPress: func() { save(edit()) }, Disabled: func() bool {
				return disabled() || v.ID == "" && strings.TrimSpace(key.Get()) == "" || len(edit().Endpoints) == 0
			}},
		))
	return autoCard(cx, nodes...)
}

func agentEnvironmentSettings(cx *core.Scope, c Host) core.Node {
	get := Live(c.AgentEnvironment)
	draft := core.NewSignal(cx, strings.TrimPrefix(get().WSL, "*"))
	busy := func() bool { return get().Busy }
	return core.Show{When: func() bool { return get().Platform == "windows" }, Then: func(cx *core.Scope) core.Node {
		return settingsColumn(
			settingsToggle("agent-wsl", tr("Run agents in WSL"), func() bool { return get().WSL != "" }, func(on bool) {
				value := ""
				if on {
					value = "*"
				}
				c.SetAgentEnvironment(value)
			}, busy),
			core.Show{When: func() bool { return get().WSL != "" }, Then: func(cx *core.Scope) core.Node {
				return settingsActions(
					ui.Input{TestID: "agent-wsl-distribution", Label: tr("WSL distribution"), Placeholder: tr("Default distribution"), Value: draft.Get, OnChange: draft.Set, Disabled: busy},
					settingsButton("agent-wsl-save", tr("Save"), func() {
						value := strings.TrimSpace(draft.Get())
						if value == "" {
							value = "*"
						}
						c.SetAgentEnvironment(value)
					}, busy),
				)
			}},
		)
	}}
}
