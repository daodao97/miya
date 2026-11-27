package desktopui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/agent/provider"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// providerEditor is the form adding or changing a provider: one switch and
// base URL per API, in provider.APIs' order.
type providerEditor struct {
	open     bool
	id       string // "" adds one
	keyHint  string
	name     string
	on       []bool
	urls     []string
	key      string
	models   string // one per line
	err      string
	note     string
	fetching bool
}

func newProviderEditor(v provider.View) providerEditor {
	e := providerEditor{open: true, id: v.ID, keyHint: v.KeyHint, name: v.Name, models: strings.Join(v.Models, "\n"),
		on: make([]bool, len(provider.APIs)), urls: make([]string, len(provider.APIs))}
	for _, ep := range v.Endpoints {
		if i := slices.Index(provider.APIs, ep.API); i >= 0 {
			e.on[i], e.urls[i] = true, ep.BaseURL
		}
	}
	return e
}

func apiLabel(api provider.API) string {
	switch api {
	case provider.Responses:
		return tr("OpenAI Responses")
	case provider.Chat:
		return tr("OpenAI Chat Completions")
	}
	return tr("Anthropic Messages")
}

// agentAPIList names the APIs agent can reach a provider through.
func agentAPIList(agent string) string {
	var labels []string
	for _, api := range provider.AgentAPIs(agent) {
		labels = append(labels, apiLabel(api))
	}
	return strings.Join(labels, " / ")
}

func apiPlaceholder(api provider.API) string {
	if api == provider.Anthropic {
		return "https://api.example.com/anthropic"
	}
	return "https://api.example.com/v1"
}

// providerSettings lists the providers configured in Miya, each with a
// switch per agent, and the form that adds or edits one.
func (app *App) providerSettings(c *ui.Context) {
	views := app.C.ProviderViews()
	use := app.C.ProviderUse()
	names := map[string]string{}
	for _, a := range app.C.Agents() {
		names[a.ID] = a.Name
	}
	rows := make([]func(), 0, len(views)+2)
	for _, v := range views {
		v := v
		rows = append(rows, func() { app.providerRow(c.Key("provider:"+v.ID), v, use, names) })
	}
	if len(views) == 0 {
		rows = append(rows, func() {
			widgets.MutedText(c, tr("No providers yet. Add an API key and base URL to use them in Claude Code, Codex or Pi."))
		})
	}
	if msg := app.C.ProviderError(); msg != "" {
		rows = append(rows, func() { ui.Text(c, msg).TextColor(widgets.Danger).FontSize(11).Selectable() })
	}
	widgets.SettingsGroupActions(c.Key("providers"), tr("Model providers"),
		tr("A provider reaches only the agents Miya starts, appended to their launch command; the CLIs' own configuration and sign-in stay as they are. Each agent uses one provider at a time, through an API it speaks: Claude Code Anthropic Messages, Codex OpenAI Responses, Pi the first the provider serves of the three. Changes apply to new turns; Codex restarts once idle."),
		func() {
			if widgets.SmallButton(c.Key("add"), tr("Add provider"), app.providerEdit.open).Clicked() {
				app.providerEdit = newProviderEditor(provider.View{})
			}
		}, rows...)
	if app.providerEdit.open {
		app.providerForm(c.Key("provider-form"))
	}
}

func (app *App) providerRow(c *ui.Context, v provider.View, use map[string]string, names map[string]string) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				title := v.Name
				if v.KeyHint != "" {
					title += " · ••••" + v.KeyHint
				}
				ui.Text(c, title).FontSize(13).TextColor(widgets.Ink)
				for _, ep := range v.Endpoints {
					ui.Text(c, apiLabel(ep.API)+" · "+ep.BaseURL).TextColor(widgets.Muted).FontSize(11).Font(widgets.MonoFont).SingleLine().Ellipsis("…").Selectable()
				}
				if len(v.Models) > 0 {
					ui.Text(c, strings.Join(v.Models, ", ")).TextColor(widgets.Muted).FontSize(11).SingleLine().Ellipsis("…")
				}
			})
			if widgets.SmallButton(c.Key("edit"), tr("Edit"), false).Clicked() {
				app.providerEdit = newProviderEditor(v)
			}
			if widgets.SmallButton(c.Key("delete"), tr("Delete"), false).Clicked() {
				if app.providerEdit.id == v.ID {
					app.providerEdit = providerEditor{}
				}
				if err := app.C.DeleteProvider(v.ID); err != nil {
					app.providerEdit.err = err.Error()
				}
			}
		})
		ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center).Wrap().Children(func() {
			for _, agent := range provider.Agents {
				agent := agent
				name := names[agent]
				if name == "" {
					name = agent
				}
				supported := provider.Supports(agent, v.Endpoints)
				on := use[agent] == v.ID
				ui.Row(c.Key("use:"+agent)).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.Switch(c.Key("switch"), &on).Label(name).Disabled(!supported).Shrink(0).Changed() {
						if err := app.C.EnableProvider(agent, v.ID, on); err != nil {
							app.providerEdit.err = err.Error()
						}
					}
					color := widgets.Ink
					if !supported {
						color = widgets.Muted
					}
					ui.Text(c, name).FontSize(12).TextColor(color).SingleLine()
					if !supported {
						ui.Text(c, fmt.Sprintf(tr("needs %s"), agentAPIList(agent))).FontSize(11).TextColor(widgets.Muted).SingleLine()
					}
				})
			}
		})
		if agent := providerWithoutModels(v, use); agent != "" {
			name := names[agent]
			if name == "" {
				name = agent
			}
			ui.Text(c, fmt.Sprintf(tr("%s uses this provider, which lists no models; add at least one."), name)).FontSize(11).TextColor(widgets.Danger)
		}
	})
}

// providerWithoutModels is an agent that uses v though v lists no model
// and the agent runs only listed ones (provider.NeedsModels).
func providerWithoutModels(v provider.View, use map[string]string) string {
	if len(v.Models) > 0 {
		return ""
	}
	for _, agent := range provider.Agents {
		if provider.NeedsModels(agent) && use[agent] == v.ID {
			return agent
		}
	}
	return ""
}

func (e *providerEditor) edit() provider.Edit {
	var models []string
	for _, line := range strings.FieldsFunc(e.models, func(r rune) bool { return r == '\n' || r == ',' }) {
		if m := strings.TrimSpace(line); m != "" {
			models = append(models, m)
		}
	}
	var eps []provider.Endpoint
	for i, api := range provider.APIs {
		if e.on[i] {
			eps = append(eps, provider.Endpoint{API: api, BaseURL: e.urls[i]})
		}
	}
	return provider.Edit{ID: e.id, Name: e.name, Endpoints: eps, APIKey: e.key, Models: models}
}

func (app *App) providerForm(c *ui.Context) {
	e := &app.providerEdit
	title := tr("Add provider")
	if e.id != "" {
		title = tr("Edit provider")
	}
	widgets.SettingsGroup(c, title, "", func() {
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			ui.Field(c, tr("Name"), func() {
				ui.TextInput(c.Key("name"), &e.name).Placeholder(tr("My provider")).FillWidth()
			})
			widgets.SettingLabel(c, tr("Base URL"), tr("Turn on each API the provider serves, with its base URL."))
			for i, api := range provider.APIs {
				i, api := i, api
				ui.Column(c.Key("api:" + string(api))).FillWidth().Gap(6).Children(func() {
					ui.Switch(c.Key("on"), &e.on[i]).Label(apiLabel(api))
					if e.on[i] {
						ui.TextInput(c.Key("url"), &e.urls[i]).Label(apiLabel(api)).Placeholder(apiPlaceholder(api)).Font(widgets.MonoFont).FontSize(12).FillWidth()
					}
				})
			}
			keyHint := tr("API key")
			if e.keyHint != "" {
				keyHint = fmt.Sprintf(tr("Leave blank to keep ••••%s"), e.keyHint)
			}
			ui.Field(c, tr("API key"), func() {
				ui.TextInput(c.Key("key"), &e.key).Password().Placeholder(keyHint).Font(widgets.MonoFont).FontSize(12).FillWidth()
			})
			ui.Field(c, tr("Models"), func() {
				ui.TextArea(c.Key("models"), &e.models).Lines(2, 8).FillWidth().Label(tr("Models")).Placeholder(tr("One model ID per line; the first is the default"))
			})
			if e.err != "" {
				ui.Text(c, e.err).TextColor(widgets.Danger).FontSize(11).Selectable()
			} else if e.note != "" {
				ui.Text(c, e.note).TextColor(widgets.Muted).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				fetchLabel := tr("Fetch models")
				if e.fetching {
					fetchLabel = tr("Fetching…")
				}
				if ui.Button(c.Key("fetch"), fetchLabel).Disabled(e.fetching).Clicked() {
					app.fetchProviderModels()
				}
				ui.Box(c).Grow(1)
				if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
					app.providerEdit = providerEditor{}
				}
				if ui.PrimaryButton(c.Key("save"), tr("Save")).Clicked() {
					app.saveProvider()
				}
			})
		})
	})
}

func (app *App) saveProvider() {
	e := &app.providerEdit
	e.err = ""
	if err := app.C.SaveProvider(e.edit()); err != nil {
		e.err = err.Error()
		return
	}
	app.providerEdit = providerEditor{}
}

func (app *App) fetchProviderModels() {
	e := &app.providerEdit
	edit := e.edit()
	e.fetching, e.err, e.note = true, "", ""
	fetch := app.C.ProviderModelsFetch(edit)
	uiruntime.GoAsync(func() {
		models, err := fetch(context.Background())
		app.C.Dispatch(func() {
			f := &app.providerEdit
			f.fetching = false
			if err != nil {
				f.err = err.Error()
				return
			}
			if len(models) == 0 {
				f.note = tr("The provider answered but listed no models; enter them by hand.")
				return
			}
			// the models already entered stay first, in their order
			kept := edit.Models
			for _, m := range models {
				if !slices.Contains(kept, m) {
					kept = append(kept, m)
				}
			}
			f.models = strings.Join(kept, "\n")
			f.note = fmt.Sprintf(tr("Connected. %d models found; remove the ones you don't use."), len(models))
		})
	})
}
