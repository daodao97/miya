package nsui

import (
	"context"
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/agent/provider"
	"miya/internal/chat/model"
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
		settingsButton("provider-new", tr("Add provider"), func() { editing.Set(&provider.View{}) }, saving.Get),
		settingsText(cx, errText.Get),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if draft := editing.Get(); draft != nil {
				return providerForm(cx, c, *draft, saving.Get, func(edit provider.Edit) {
					call("providerSave", edit, func() error { return c.SaveProvider(edit) }, func() { editing.Set(nil) })
				}, func() { editing.Set(nil) })
			}
			views := Live(c.ProviderViews)()
			use := Live(c.ProviderUse)()
			nodes := []core.Node{}
			for _, v := range views {
				body := []core.Node{
					autoHeading(v.Name),
					settingsText(cx, func() string { return strings.Join(v.Models, ", ") }),
				}
				for _, agent := range provider.Agents {
					if provider.Supports(agent, v.Endpoints) {
						body = append(body, settingsToggle("provider-"+v.ID+"-"+agent, model.AgentName(agent),
							func() bool { return use[agent] == v.ID }, func(on bool) {
								id := v.ID
								if !on {
									id = ""
								}
								call("providerUse", provider.Use{Agent: agent, ID: id},
									func() error { return c.EnableProvider(agent, v.ID, on) }, nil)
							}, saving.Get))
					}
				}
				confirm := core.NewSignal(cx, false)
				body = append(body, settingsActions(
					settingsButton("provider-edit-"+v.ID, tr("Edit"), func() { editing.Set(&v) }, saving.Get),
					settingsButton("provider-delete-"+v.ID, tr("Delete"), func() { confirm.Set(true) }, saving.Get),
				), core.Show{When: confirm.Get, Then: func(cx *core.Scope) core.Node {
					return settingsActions(
						settingsButton("provider-confirm-"+v.ID, tr("Confirm deletion"), func() {
							call("providerDelete", v.ID, func() error { return c.DeleteProvider(v.ID) }, func() { confirm.Set(false) })
						}, saving.Get), settingsButton("", tr("Cancel"), func() { confirm.Set(false) }, saving.Get),
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
	keyPrompt := tr("API key")
	if v.KeyHint != "" {
		keyPrompt += " · ••••" + v.KeyHint
	}
	nodes := []core.Node{
		base.Input{TestID: "provider-name", Label: tr("Name"), Placeholder: tr("Name"), Value: name.Get, OnChange: name.Set},
		base.Input{TestID: "provider-key", Label: tr("API key"), Placeholder: keyPrompt, Masked: true, Value: key.Get, OnChange: key.Set},
	}
	for _, api := range provider.APIs {
		value := ""
		for _, e := range v.Endpoints {
			if e.API == api {
				value = e.BaseURL
			}
		}
		endpoints[api] = core.NewSignal(cx, value)
		label := fmt.Sprintf(tr("%s endpoint"), string(api))
		nodes = append(nodes, base.Input{TestID: "provider-endpoint-" + string(api), Label: label, Placeholder: label, Value: endpoints[api].Get, OnChange: endpoints[api].Set})
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
	disabled := func() bool { return busy.Get() || saving() }
	nodes = append(nodes, base.Input{TestID: "provider-models", Label: tr("Models"), Placeholder: tr("Models"), Multiline: true, Value: models.Get, OnChange: models.Set},
		settingsText(cx, message.Get), settingsActions(
			settingsButton("provider-fetch", tr("Fetch models"), fetch, disabled),
			settingsButton("provider-save", tr("Save"), func() { save(edit()) }, disabled),
			settingsButton("provider-cancel", tr("Cancel"), cancel, saving),
		))
	return settingsColumn(nodes...)
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
					base.Input{TestID: "agent-wsl-distribution", Label: tr("WSL distribution"), Placeholder: tr("Default distribution"), Value: draft.Get, OnChange: draft.Set, Disabled: busy},
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
