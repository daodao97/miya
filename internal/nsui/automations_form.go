package nsui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

type automationRecipes struct {
	recipes  []automation.Recipe
	problems []string
}
type automationFormState struct {
	editing                     *core.Signal[string]
	id, request, project, agent *core.Signal[string]
	enabled, drafting, saving   *core.Signal[bool]
	draft                       *core.Signal[*automation.View]
	err                         *core.Signal[string]
	generation                  *core.Signal[int]
	recipes                     *core.Signal[automationRecipes]
}

func newAutomationForm(cx *core.Scope) *automationFormState {
	return &automationFormState{editing: core.NewSignal(cx, ""), id: core.NewSignal(cx, ""), request: core.NewSignal(cx, ""), project: core.NewSignal(cx, ""), agent: core.NewSignal(cx, ""), enabled: core.NewSignal(cx, true), drafting: core.NewSignal(cx, false), saving: core.NewSignal(cx, false), draft: core.NewSignal[*automation.View](cx, nil), err: core.NewSignal(cx, ""), generation: core.NewSignal(cx, 0), recipes: core.NewSignalFunc(cx, automationRecipes{}, func(a, b automationRecipes) bool { return reflect.DeepEqual(a, b) })}
}
func (f *automationFormState) invalidate() {
	f.editing.Set("")
	f.generation.Update(func(n int) int { return n + 1 })
	f.draft.Set(nil)
	f.drafting.Set(false)
	f.err.Set("")
}
func (a *automationsState) openForm(v *automation.View) {
	f := a.form
	f.invalidate()
	f.id.Set("")
	f.request.Set("")
	f.project.Set(a.host.State().Cwd)
	f.agent.Set(model.Backends[0])
	f.enabled.Set(true)
	if get := a.host.State().Agent; get != nil {
		f.agent.Set(get())
	}
	if v != nil {
		copy := *v
		f.id.Set(v.ID)
		f.request.Set(textutil.FirstNonEmpty(v.Request, v.Instructions))
		f.project.Set(v.Project)
		f.agent.Set(v.Agent)
		f.enabled.Set(v.Enabled)
		f.draft.Set(&copy)
	}
	a.err.Set("")
	a.route.Set(automationRoute{"form", f.id.Get()})
	go func() {
		recipes, problems := automation.Recipes()
		a.cx.Post(func() { f.recipes.Set(automationRecipes{recipes, problems}) })
	}()
}
func (a *automationsState) cancelForm() {
	f := a.form
	f.invalidate()
	route := automationRoute{}
	if f.id.Get() != "" {
		route = automationRoute{"detail", f.id.Get()}
	}
	a.route.Set(route)
}
func (a *automationsState) generate() {
	f := a.form
	if strings.TrimSpace(f.request.Get()) == "" || f.drafting.Get() || f.saving.Get() {
		return
	}
	f.invalidate()
	gen := f.generation.Get()
	f.drafting.Set(true)
	req := automation.Request{Request: f.request.Get(), Project: f.project.Get(), Agent: f.agent.Get()}
	a.call("automationDraft", req, func(raw json.RawMessage, err error) {
		if gen != f.generation.Get() {
			return
		}
		f.drafting.Set(false)
		var v automation.View
		if err == nil {
			if json.Unmarshal(raw, &v) != nil {
				f.err.Set(tr("Unable to read generated settings"))
				return
			}
		}
		if err != nil {
			f.err.Set(err.Error())
			return
		}
		f.draft.Set(&v)
	})
}
func (a *automationsState) saveForm(run bool) {
	f := a.form
	if f.draft.Get() == nil || f.drafting.Get() || f.saving.Get() || a.pending.Get() || f.editing.Get() != "" {
		return
	}
	next := autoClone(f.draft.Get().Automation)
	next.ID, next.Request, next.Project, next.Agent, next.Enabled = f.id.Get(), strings.TrimSpace(f.request.Get()), f.project.Get(), f.agent.Get(), f.enabled.Get()
	// Validate before sending, keeping a new record's ID service-owned.
	id := next.ID
	if err := next.Normalize(); err != nil {
		f.err.Set(err.Error())
		return
	}
	next.ID = id
	f.saving.Set(true)
	a.pending.Set(true)
	a.generation.Update(func(n int) int { return n + 1 })
	a.loading.Set(false)
	f.err.Set("")
	a.call("automationSave", automation.Request{Automation: &next}, func(raw json.RawMessage, err error) {
		f.saving.Set(false)
		a.pending.Set(false)
		var v automation.View
		if err == nil {
			err = json.Unmarshal(raw, &v)
		}
		if err != nil {
			f.err.Set(err.Error())
			return
		}
		list := slices.Clone(a.list.Get())
		replaced := false
		for i := range list {
			if list[i].ID == v.ID {
				list[i] = v
				replaced = true
			}
		}
		if !replaced {
			list = append(list, v)
		}
		a.list.Set(list)
		f.generation.Update(func(n int) int { return n + 1 })
		a.route.Set(automationRoute{"detail", v.ID})
		if run {
			a.run(v.ID)
		} else {
			a.load()
		}
	})
}
func (a *automationsState) formView(cx *core.Scope) core.Node {
	f := a.form
	title := tr("New automation")
	save := tr("Create")
	if f.id.GetUntracked() != "" {
		title = tr("Edit automation")
		save = tr("Save")
	}
	blocked := func() bool {
		return f.draft.Get() == nil || f.drafting.Get() || f.saving.Get() || a.pending.Get() || f.editing.Get() != ""
	}
	return autoColumn(
		ui.Button{TestID: "automation-form-back", Text: tr("Automations"), Variant: ui.ButtonGhost, Disabled: f.saving.Get, OnPress: a.cancelForm},
		autoHeading(title), autoMuted(cx, tr("Describe what to do and when, then click Generate settings to organize the schedule, completion criteria, and turn limit.")),
		autoCard(cx, ui.Input{TestID: "automation-request", Label: tr("What to do"), Multiline: true, Value: f.request.Get, Disabled: f.saving.Get,
			Placeholder: tr("For example: Check new issues every weekday at 9 AM, fix what you can and pass go test, then list anything unresolved"), OnChange: func(s string) { f.request.Set(s); f.invalidate() }},
			autoRow(core.Dynamic(func(cx *core.Scope) core.Node {
				paths := Live(func() []string {
					var out []string
					if get := a.host.State().Projects; get != nil {
						for _, p := range get() {
							out = append(out, p.Path)
						}
					}
					return out
				})()
				selected := f.project.GetUntracked()
				if !slices.Contains(paths, selected) {
					paths = append(paths, selected)
				}
				options := make([]ui.Option[string], 0, len(paths))
				for _, p := range paths {
					options = append(options, ui.Option[string]{Value: p, Label: filepath.Base(p)})
				}
				return ui.Select[string]{TestID: "automation-project", Label: tr("Projects"), Value: f.project.Get, Options: options, Disabled: f.saving.Get, Searchable: true,
					OnChange: func(s string) {
						if s != f.project.Get() {
							f.project.Set(s)
							f.invalidate()
						}
					}}
			}), core.Dynamic(func(cx *core.Scope) core.Node {
				enabled := Live(a.host.EnabledAgents)()
				selected := f.agent.GetUntracked()
				var options []ui.Option[string]
				for _, ag := range model.Backends {
					if ag == selected || slices.Contains(enabled, ag) {
						options = append(options, ui.Option[string]{Value: ag, Label: model.AgentName(ag)})
					}
				}
				return ui.Select[string]{TestID: "automation-agent", Label: tr("Agent"), Value: f.agent.Get, Options: options, Disabled: f.saving.Get, OnChange: func(s string) {
					if s != f.agent.Get() {
						f.agent.Set(s)
						f.invalidate()
					}
				}}
			})),
		),
		autoRow(autoText(tr("Generated settings")), nswidgets.MutedText(cx, func() string {
			if f.drafting.Get() {
				return tr("Generating…")
			}
			if d := f.draft.Get(); d != nil {
				if d.TimeMissing {
					return tr("Schedule needed")
				}
				return tr("Ready")
			}
			return tr("Click Generate settings when you are ready")
		}), ui.Button{TestID: "automation-generate", Text: tr("Generate settings"), Disabled: func() bool { return strings.TrimSpace(f.request.Get()) == "" || f.drafting.Get() || f.saving.Get() }, OnPress: a.generate}),
		core.Show{When: func() bool { return f.err.Get() != "" }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "automation-form-error", ContentFunc: f.err.Get, Selectable: core.SelectableOn, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Danger(), FontSize: 12} }}
		}},
		core.Show{When: f.drafting.Get, Then: func(cx *core.Scope) core.Node {
			return nswidgets.WorkingLabel(cx, "clock", func() string { return tr("Generating…") })
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			if d := f.draft.Get(); d != nil {
				return a.draftView(cx, *d)
			}
			return a.recipesView(cx)
		}),
		autoRow(ui.Switch{TestID: "automation-form-enabled", Label: tr("Enable after saving"), Checked: f.enabled.Get, OnChange: f.enabled.Set}, autoText(tr("Enable after saving"))),
		autoRow(ui.Button{TestID: "automation-form-cancel", Text: tr("Cancel"), Variant: ui.ButtonSecondary, Disabled: f.saving.Get, OnPress: a.cancelForm},
			ui.Button{TestID: "automation-save-run", Text: tr("Save and run once"), Variant: ui.ButtonSecondary, Disabled: blocked, OnPress: func() { a.saveForm(true) }},
			ui.Button{TestID: "automation-save", Text: save, Disabled: blocked, OnPress: func() { a.saveForm(false) }}),
	)
}

func (a *automationsState) draftView(cx *core.Scope, d automation.View) core.Node {
	children := []core.Node{a.draftField(cx, d, "name", "Name", d.Name), a.triggers(cx, d, true, nil)}
	if d.TimeMissing {
		children = append(children, ui.Badge{Text: tr("No time specified. Using ") + automation.DescribeCron(d.Schedule), Variant: ui.BadgeWarn})
	}
	// The schedule may be cleared for trigger-only automations or added to one.
	children = append(children, a.draftField(cx, d, "schedule", "Schedule", d.Schedule))
	for _, at := range d.Upcoming {
		children = append(children, autoMuted(cx, automation.FormatTime(at)))
	}
	children = append(children, a.draftField(cx, d, "check", "Completion check", d.Check), a.draftField(cx, d, "rounds", "Turns", strconv.Itoa(d.Rounds)), a.draftField(cx, d, "instructions", "Instructions", d.Instructions))
	return autoCard(cx, children...)
}

// A field owns its typing state; the accepted draft only changes on Done.
// This keeps native inputs mounted while typing and validates each edit.
func (a *automationsState) draftField(cx *core.Scope, d automation.View, key, label, raw string) core.Node {
	open := func() bool { return a.form.editing.Get() == key }
	text := core.NewSignal(cx, raw)
	problem := core.NewSignal(cx, "")
	apply := func() {
		value := strings.TrimSpace(text.Get())
		next := autoClone(d.Automation)
		id := next.ID
		switch key {
		case "name":
			next.Name = value
		case "schedule":
			next.Schedule = value
		case "check":
			next.Check = value
			if value != "" && next.Rounds < 2 {
				next.Rounds = 3
			}
		case "rounds":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 5 {
				problem.Set(tr("Enter a number from 1 to 5"))
				return
			}
			next.Rounds = n
		case "instructions":
			next.Instructions = value
		}
		if err := next.Normalize(); err != nil {
			problem.Set(err.Error())
			return
		}
		next.ID = id
		view := automation.ViewOf(next, d.Runs, d.Running, time.Now())
		view.TimeMissing = d.TimeMissing && key != "schedule"
		view.Trials = d.Trials
		a.form.editing.Set("")
		a.form.draft.Set(&view)
	}
	return autoColumn(autoRow(autoText(tr(label)), autoButton("automation-change-"+key, "Change", func() { text.Set(raw); problem.Set(""); a.form.editing.Set(key) })),
		core.Show{When: open, Then: func(cx *core.Scope) core.Node {
			return autoColumn(
				ui.Input{TestID: "automation-field-" + key, Label: tr(label), Value: text.Get, OnChange: text.Set, Multiline: key == "instructions", AutoFocus: true, OnSubmit: func(string) { apply() }},
				core.Show{When: func() bool { return key == "schedule" }, Then: func(cx *core.Scope) core.Node {
					return nswidgets.MutedText(cx, func() string {
						if _, err := automation.ParseCron(text.Get()); err == nil {
							return automation.DescribeCron(text.Get())
						}
						return tr("5-field cron: minute hour day month weekday")
					})
				}},
				core.Text{ContentFunc: problem.Get, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Danger(), FontSize: 12} }},
				autoRow(autoButton("automation-field-done-"+key, "Done", apply), autoButton("automation-field-cancel-"+key, "Cancel", func() { a.form.editing.Set("") })),
			)
		}, Else: func(cx *core.Scope) core.Node {
			shown := raw
			switch key {
			case "schedule":
				shown = automation.DescribeCron(raw) + " · " + raw
			case "check":
				if raw == "" {
					shown = tr("No check; finish after one turn")
				}
			case "rounds":
				shown = fmt.Sprintf(tr("Up to %d turns"), d.Rounds)
			}
			return autoText(shown)
		}},
	)
}
func (a *automationsState) recipesView(cx *core.Scope) core.Node {
	r := a.form.recipes.Get()
	children := []core.Node{autoText(tr("Triggers you can ask for"))}
	for _, recipe := range r.recipes {
		label := recipe.Name
		if !recipe.Bundled {
			label += " · " + tr("Added by you")
		}
		summary := recipe.Description
		if recipe.Text != "" {
			summary = tr(recipe.Text)
		}
		for _, p := range recipe.Params {
			summary += " · " + p.Name
		}
		children = append(children, autoRow(nswidgets.Icon("bulb", 14, nswidgets.ColorsOf(cx).Muted), autoText(label)), autoMuted(cx, summary))
	}
	for _, p := range r.problems {
		children = append(children, autoText(tr("Cannot be used: ")+p))
	}
	children = append(children, autoMuted(cx, tr("Mention one in your request. To add a kind of trigger, put a .json file in ")+automation.RecipeDir()))
	return autoCard(cx, children...)
}

func autoClone(a automation.Automation) automation.Automation { return automation.Clone(a) }
