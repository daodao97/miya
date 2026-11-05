package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// automationsUI is the Automations page's state; the automations live in
// the service, which the page asks through automationCall.
type automationsUI struct {
	page     string // "", "detail" or "form"
	detail   string // the automation the detail page shows
	list     []automationView
	loaded   bool
	loading  bool
	loadedAt time.Time
	err      string
	// confirmDelete is the automation whose delete button was pressed once.
	confirmDelete string
	// openReports flips a run\'s result open or shut, by its start.
	openReports map[int64]bool

	// The form: what the person wrote, where it runs, and what the agent
	// made of it.
	editID      string
	request     string
	project     string
	agent       string
	projectOpen bool
	enable      bool
	draft       *automationView
	drafting    bool
	draftErr    string
	gen         int
	saving      bool
	editing     string
	editText    string
	editErr     string
}

var (
	autoOK     = ui.Hex("#15803d")
	autoBad    = ui.Hex("#dc2626")
	autoRun    = ui.Hex("#2563eb")
	autoWarn   = ui.Hex("#b45309")
	autoOKBg   = ui.RGBA(21, 128, 61, 0.1)
	autoBadBg  = ui.RGBA(220, 38, 38, 0.1)
	autoRunBg  = ui.RGBA(37, 99, 235, 0.1)
	autoWarnBg = ui.RGBA(217, 119, 6, 0.12)
	autoGrayBg = ui.RGBA(120, 120, 128, 0.12)
)

// agentTint is the color an agent's adapter gives its icon.
func agentTint(agent string) ui.Color {
	if spec := backendFor(agent).Icon(); spec.Color != "" {
		return ui.Hex(spec.Color)
	}
	return ui.Hex("#7c6cf6")
}

// autoCall runs an automation operation and then done on the main thread,
// never making the window wait.
func (app *chatApp) autoCall(op string, r automationRequest, done func(json.RawMessage, error)) {
	c := app.c
	if c.remote != nil {
		go func() {
			var out json.RawMessage
			err := c.remote.call(op, r, &out)
			c.dispatch(func() { done(out, err) })
		}()
		return
	}
	finish := func(res any, err error) {
		var raw json.RawMessage
		if err == nil && res != nil {
			raw, _ = json.Marshal(res)
		}
		done(raw, err)
	}
	res, err := c.automationOp(op, r)
	if d, ok := res.(deferred); ok && err == nil {
		go func() {
			v, err := d()
			c.dispatch(func() { finish(v, err) })
		}()
		return
	}
	finish(res, err)
}

func (app *chatApp) showAutomations() {
	app.section = "Automations"
	app.autos.page = ""
	app.autos.loadedAt = time.Time{}
}

func (app *chatApp) loadAutomations() {
	a := &app.autos
	if a.loading {
		return
	}
	a.loading = true
	app.autoCall("automationList", automationRequest{}, func(raw json.RawMessage, err error) {
		a.loading, a.loadedAt = false, time.Now()
		if err != nil {
			a.err = err.Error()
			return
		}
		var list []automationView
		_ = json.Unmarshal(raw, &list)
		a.list, a.loaded, a.err = list, true, ""
	})
}

func (app *chatApp) automationsView(c *ui.Context) {
	a := &app.autos
	if !a.loading && time.Since(a.loadedAt) > 5*time.Second {
		app.loadAutomations()
	}
	ui.Column(c).FillWidth().MaxWidth(880).AlignSelf(ui.Center).Gap(18).Children(func() {
		switch a.page {
		case "form":
			app.automationForm(c)
		case "detail":
			app.automationDetail(c)
		default:
			app.automationList(c)
		}
	})
}

func autoTile(c *ui.Context, agent string, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.28).Background(agentTint(agent)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		icon(c, "clock", size*0.46, ui.Hex("#ffffff"))
	})
}

// runBadge says how a run went, or that it runs.
func runBadge(c *ui.Context, run AutomationRun, has, running bool) {
	label, fg, bg := tr("Not run yet"), muted, autoGrayBg
	switch {
	case running:
		label, fg, bg = fmt.Sprintf(tr("Running · Turn %d/%d"), max(run.Round, 1), max(run.Rounds, 1)), autoRun, autoRunBg
	case !has:
	case run.Result == runPassed:
		label, fg, bg = tr("Passed"), autoOK, autoOKBg
		if run.Rounds > 1 {
			label += fmt.Sprintf(tr(" · Turn %d"), run.Round)
		}
	case run.Result == runFailed:
		label, fg, bg = tr("Failed"), autoBad, autoBadBg
		if run.Rounds > 1 {
			label += fmt.Sprintf(tr(" · %d/%d turns"), run.Round, run.Rounds)
		}
	default:
		label = runResultLabel(run.Result)
	}
	ui.Row(c).Height(22).PaddingX(9).Gap(5).Radius(11).AlignItems(ui.Center).AlignSelf(ui.Start).Background(bg).Children(func() {
		if running {
			ui.Spinner(c).FontSize(10)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).SingleLine()
	})
}

func lastAutomationRun(v automationView) (AutomationRun, bool) {
	for i := len(v.Runs) - 1; i >= 0; i-- {
		if v.Runs[i].Result != runSkipped {
			return v.Runs[i], true
		}
	}
	return AutomationRun{}, false
}

func automationSubline(v automationView) string {
	parts := []string{filepath.Base(v.Project), backendFor(v.Agent).Name()}
	if v.Check != "" {
		parts = append(parts, tr("Completion criteria ")+v.Check)
	} else {
		parts = append(parts, tr("Single turn"))
	}
	return strings.Join(parts, " · ")
}

func (app *chatApp) automationList(c *ui.Context) {
	a := &app.autos
	ui.Row(c).FillWidth().AlignItems(ui.End).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, tr("Automations")).FontSize(26).FontWeight(600)
			ui.Text(c, tr("Start a new conversation in the project on schedule, then send the result to your phone.")).TextColor(muted)
		})
		if ui.PrimaryButton(c.Key("new"), tr("New")).Clicked() {
			app.openAutomationForm(nil)
		}
	})
	if a.err != "" {
		ui.Text(c, a.err).FontSize(12).TextColor(failed)
	}
	switch {
	case !a.loaded:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).FontSize(12)
			ui.Text(c, tr("Loading…")).TextColor(muted)
		})
	case len(a.list) == 0:
		ui.Column(c).FillWidth().Padding(28).Gap(6).AlignItems(ui.Center).Border(1, line).Radius(12).Children(func() {
			icon(c, "clock", 22, muted)
			ui.Text(c, tr("No automations yet")).FontWeight(600)
			ui.Text(c, tr("Click New to describe a task, or ask an agent to create one in any conversation.")).FontSize(12).TextColor(muted)
		})
	default:
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for _, v := range a.list {
				app.automationRow(c.Key(v.ID), v)
			}
		})
	}
	ui.Row(c).FillWidth().Gap(12).Padding(14, 16, 14, 16).Border(1, line).Radius(12).AlignItems(ui.Start).Children(func() {
		icon(c, "message-circle", 16, ink)
		ui.Text(c, tr("Describe a recurring task in any conversation. It appears here after confirmation. Enabled automations keep running when the window is closed.")).
			Grow(1).MinWidth(0).FontSize(12).LineHeight(1.6).TextColor(muted)
	})
}

func (app *chatApp) automationRow(c *ui.Context, v automationView) {
	a := &app.autos
	last, has := lastAutomationRun(v)
	ui.Row(c).FillWidth().Gap(14).Padding(14, 16, 14, 16).AlignItems(ui.Center).Border(1, line).Radius(12).Children(func() {
		autoTile(c, v.Agent, 36)
		open := quiet(c.Key("open")).Grow(1).MinWidth(0).Height(0).MinHeight(40).Padding(0).Column().AlignItems(ui.Start).Justify(ui.Center).Gap(2).Label(v.Name)
		open.Children(func() {
			name := ui.Text(c, v.Name).FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			if !v.Enabled {
				name.TextColor(muted)
			}
			ui.Text(c, automationSubline(v)).FontSize(12).TextColor(muted).SingleLine().Ellipsis("…")
		})
		if open.Clicked() {
			a.page, a.detail, a.confirmDelete = "detail", v.ID, ""
		}
		ui.Column(c).Width(150).Shrink(0).Gap(1).Children(func() {
			ui.Text(c, describeCron(v.Schedule)).FontWeight(500).SingleLine().Ellipsis("…")
			next := tr("Paused")
			if v.Enabled {
				next = tr("Next ") + formatAutomationTime(v.Next)
			}
			ui.Text(c, next).FontSize(12).TextColor(muted).SingleLine()
		})
		ui.Column(c).Width(140).Shrink(0).Children(func() {
			if v.Running {
				last, has = v.Runs[len(v.Runs)-1], true
			}
			runBadge(c, last, has, v.Running)
		})
		on := v.Enabled
		if ui.Switch(c.Key("on"), &on).Shrink(0).Changed() {
			app.toggleAutomation(v.ID, on)
		}
		ui.Row(c).Width(64).Shrink(0).Justify(ui.End).Gap(2).Children(func() {
			if !v.Running && toolButton(c.Key("run"), "arrow-right", tr("Run now")).Clicked() {
				app.runAutomationNow(v.ID)
			}
			if has && last.Thread != 0 && toolButton(c.Key("last"), "message-circle", tr("Open last conversation")).Clicked() {
				app.openAutomationThread(v.Project, last.Thread)
			}
		})
	})
}

func (app *chatApp) toggleAutomation(id string, on bool) {
	a := &app.autos
	for i := range a.list {
		if a.list[i].ID == id {
			a.list[i].Enabled = on
		}
	}
	app.autoCall("automationToggle", automationRequest{ID: id, Enabled: on}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		app.loadAutomations()
	})
}

func (app *chatApp) runAutomationNow(id string) {
	a := &app.autos
	app.autoCall("automationRunNow", automationRequest{ID: id}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		app.loadAutomations()
	})
}

// openAutomationThread opens a run's conversation, in its project.
func (app *chatApp) openAutomationThread(project string, id int) {
	c := app.c
	if project != "" && project != c.cwd {
		if err := c.changeWorkspace(project); err != nil {
			app.autos.err = err.Error()
			return
		}
	}
	if t := c.threadWithID(id); t.ID != 0 {
		c.selectThread(t)
	} else {
		c.setActive(id)
	}
	app.section = ""
}

func backLink(c *ui.Context, label string) bool {
	b := quiet(c.Key("back")).AlignSelf(ui.Start).PaddingX(6).Gap(4).Label(label)
	b.Children(func() {
		icon(c, "arrow-left", 14, muted)
		ui.Text(c, label).TextColor(muted)
	})
	return b.Clicked()
}

func (app *chatApp) automationDetail(c *ui.Context) {
	a := &app.autos
	var v automationView
	found := false
	for _, x := range a.list {
		if x.ID == a.detail {
			v, found = x, true
		}
	}
	if backLink(c, "Automations") || (!found && a.loaded) {
		a.page = ""
		return
	}
	if !found {
		return
	}
	ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
		autoTile(c, v.Agent, 48)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, v.Name).FontSize(24).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, filepath.Base(v.Project)+" · "+backendFor(v.Agent).Name()+" · "+describeCron(v.Schedule)).TextColor(muted).SingleLine().Ellipsis("…")
		})
		on := v.Enabled
		if ui.Switch(c.Key("on"), &on).Shrink(0).Changed() {
			app.toggleAutomation(v.ID, on)
		}
		if ui.Button(c.Key("edit"), tr("Edit")).Clicked() {
			app.openAutomationForm(&v)
		}
		if ui.PrimaryButton(c.Key("run"), tr("Run now")).Disabled(v.Running).Clicked() {
			app.runAutomationNow(v.ID)
		}
		if a.confirmDelete == v.ID {
			if ui.Button(c.Key("delete-confirm"), tr("Confirm deletion")).Clicked() {
				app.deleteAutomation(v.ID)
			}
		} else if toolButton(c.Key("delete"), "trash-2", tr("Delete")).Clicked() {
			a.confirmDelete = v.ID
		}
	})
	passed, total, rounds := 0, 0, 0
	for _, r := range v.Runs {
		if r.Result == runSkipped || r.Result == runRunning {
			continue
		}
		total++
		rounds += max(r.Round, 1)
		if r.Result == runPassed {
			passed++
		}
	}
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		next := tr("Paused")
		if v.Enabled {
			next = formatAutomationTime(v.Next)
		}
		stat(c, tr("Next run"), next)
		if total > 0 {
			stat(c, fmt.Sprintf(tr("Last %d runs"), total), fmt.Sprintf(tr("%d passed"), passed))
			stat(c, tr("Average"), fmt.Sprintf(tr("%.1f turns"), float64(rounds)/float64(total)))
		} else {
			stat(c, tr("Run history"), tr("No runs yet"))
		}
	})
	ui.Column(c).FillWidth().Gap(10).Padding(16).Border(1, line).Radius(12).Children(func() {
		ui.Text(c, tr("Instructions")).FontSize(12).TextColor(muted)
		ui.Text(c, v.Instructions).FontSize(14).LineHeight(1.7).Selectable()
		ui.Row(c).Gap(8).Children(func() {
			settingChip(c, "clock", v.Schedule)
			if v.Check != "" {
				settingChip(c, "terminal-2", v.Check)
				settingChip(c, "refresh", fmt.Sprintf(tr("Up to %d turns"), v.Rounds))
			} else {
				settingChip(c, "refresh", tr("Single turn"))
			}
		})
		if v.Request != "" && v.Request != v.Instructions {
			ui.Text(c, tr("Original request: ")+v.Request).FontSize(12).TextColor(muted).LineHeight(1.5)
		}
	})
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		carry := !v.NoCarryOver
		if ui.Switch(c.Key("carry"), &carry).Label(tr("Include previous result")).Shrink(0).Changed() {
			next := v.Automation
			next.NoCarryOver = !carry
			app.autoCall("automationSave", automationRequest{Automation: &next}, func(_ json.RawMessage, err error) {
				if err != nil {
					app.autos.err = err.Error()
				}
				app.loadAutomations()
			})
		}
		ui.Text(c, tr("Include the previous structured result with each run so the agent can compare changes")).FontSize(12).TextColor(muted)
	})
	ui.Row(c).FillWidth().AlignItems(ui.End).Margin(8, 0, 0, 0).Children(func() {
		ui.Text(c, tr("Run history")).FontWeight(600).Grow(1)
		ui.Text(c, fmt.Sprintf(tr("Last %d runs"), automationRunHistory)).FontSize(12).TextColor(muted)
	})
	if len(v.Runs) == 0 {
		ui.Text(c, tr("No runs yet.")).TextColor(muted)
		return
	}
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		for i := len(v.Runs) - 1; i >= 0; i-- {
			app.runRow(c.Key(i), v, v.Runs[i], v.Running && i == len(v.Runs)-1)
			if rep := v.Runs[i].Report; rep != nil {
				app.reportView(c.Key(fmt.Sprintf("report:%d", i)), v.Runs[i], i == latestReportIndex(v.Runs))
			}
		}
	})
}

func stat(c *ui.Context, label, value string) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(2).Padding(12, 14, 12, 14).Border(1, line).Radius(12).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(muted)
		ui.Text(c, value).FontSize(17).FontWeight(600).SingleLine().Ellipsis("…")
	})
}

func settingChip(c *ui.Context, symbol, text string) {
	ui.Row(c).Gap(5).Height(24).PaddingX(9).Radius(7).Border(1, line).AlignItems(ui.Center).Children(func() {
		icon(c, symbol, 12, muted)
		ui.Text(c, text).FontSize(12).Font(monoFont).SingleLine()
	})
}

func (app *chatApp) runRow(c *ui.Context, v automationView, r AutomationRun, running bool) {
	color, title := ui.Color(muted), r.Summary
	switch {
	case running:
		color, title = autoRun, fmt.Sprintf(tr("Running · Turn %d/%d"), max(r.Round, 1), max(r.Rounds, 1))
	case r.Result == runPassed:
		color = autoOK
	case r.Result == runFailed:
		color = autoBad
	}
	title = firstNonEmpty(title, runResultLabel(r.Result))
	if r.Result == runSkipped {
		title = tr("Skipped, ") + r.Summary
	}
	meta := r.Started.Format("Jan 2 ") + tr(weekdayNames[r.Started.Weekday()]) + r.Started.Format(" 15:04")
	if r.CatchUp {
		meta += tr(" · Catch-up run")
	}
	if r.Result != runSkipped {
		meta += fmt.Sprintf(tr(" · %s · Turn %d/%d"), runResultLabel(r.Result), max(r.Round, 1), max(r.Rounds, 1))
		if !r.Finished.IsZero() {
			meta += fmt.Sprintf(tr(" · %d minutes"), max(int(r.Finished.Sub(r.Started).Minutes()+0.5), 1))
		}
	}
	// The caller keys each row.
	row := quiet(c).FillWidth().Height(0).MinHeight(52).Padding(8, 12, 8, 12).Gap(12).AlignItems(ui.Center).Label(title)
	row.Children(func() {
		ui.Box(c).Size(9, 9).Radius(4.5).Shrink(0).Background(color)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			t := ui.Text(c, title).FontWeight(500).SingleLine().Ellipsis("…")
			if r.Result == runSkipped {
				t.TextColor(muted)
			}
			ui.Text(c, meta).FontSize(12).TextColor(muted).SingleLine().Ellipsis("…")
		})
		if r.Thread != 0 {
			ui.Text(c, tr("Open conversation")).FontSize(12).TextColor(muted)
			icon(c, "arrow-right", 12, muted)
		}
	})
	if row.Clicked() && r.Thread != 0 {
		app.openAutomationThread(v.Project, r.Thread)
	}
}

func (app *chatApp) deleteAutomation(id string) {
	a := &app.autos
	app.autoCall("automationDelete", automationRequest{ID: id}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		a.page, a.confirmDelete = "", ""
		app.loadAutomations()
	})
}

// openAutomationForm starts a new automation, or edits v.
func (app *chatApp) openAutomationForm(v *automationView) {
	a := &app.autos
	c := app.c
	a.page, a.editID, a.request, a.draft, a.draftErr, a.drafting, a.editing, a.editErr = "form", "", "", nil, "", false, "", ""
	a.enable, a.project, a.agent, a.projectOpen = true, c.cwd, agentBackends[0].ID(), false
	if c.agent != nil {
		a.agent = c.agent()
	}
	a.gen++
	if v != nil {
		draft := *v
		a.editID, a.request, a.draft = v.ID, firstNonEmpty(v.Request, v.Instructions), &draft
		a.project, a.agent, a.enable = v.Project, v.Agent, v.Enabled
	}
}

// invalidateDraft discards settings and in-flight results after an input changes.
// Only the explicit generate button starts another request.
func (app *chatApp) invalidateDraft() {
	a := &app.autos
	a.gen++
	a.draft, a.drafting, a.draftErr, a.editing, a.editErr = nil, false, "", "", ""
}

func (app *chatApp) startDraft() {
	a := &app.autos
	if strings.TrimSpace(a.request) == "" || a.drafting || a.saving {
		return
	}
	app.invalidateDraft()
	g := a.gen
	a.drafting, a.draftErr = true, ""
	req := automationRequest{Request: a.request, Project: a.project, Agent: a.agent}
	app.autoCall("automationDraft", req, func(raw json.RawMessage, err error) {
		if g != a.gen {
			return
		}
		a.drafting = false
		if err != nil {
			a.draftErr = err.Error()
			return
		}
		var v automationView
		if json.Unmarshal(raw, &v) != nil {
			a.draftErr = tr("Unable to read generated settings")
			return
		}
		a.draft, a.editing = &v, ""
	})
}

func (app *chatApp) automationForm(c *ui.Context) {
	a := &app.autos
	ui.Column(c).FillWidth().MaxWidth(680).AlignSelf(ui.Center).Gap(16).Children(func() {
		if backLink(c, "Automations") {
			a.page = ""
			if a.editID != "" {
				a.page = "detail"
			}
			return
		}
		title := tr("New automation")
		if a.editID != "" {
			title = tr("Edit automation")
		}
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(24).FontWeight(600)
			ui.Text(c, tr("Describe what to do and when, then click Generate settings to organize the schedule, completion criteria, and turn limit.")).TextColor(muted)
		})
		app.automationComposer(c)
		app.draftStatus(c)
		switch {
		case a.draft != nil:
			app.draftPreview(c, a.draft)
		case a.drafting:
			draftSkeleton(c)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Margin(4, 0, 0, 0).Children(func() {
			ui.Switch(c.Key("enable"), &a.enable).Label(tr("Enable after saving")).Shrink(0)
			ui.Text(c, tr("Enable after saving")).TextColor(muted)
			ui.Spacer(c)
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				a.page = ""
				if a.editID != "" {
					a.page = "detail"
				}
			}
			blocked := a.draft == nil || a.drafting || a.saving || a.editing != ""
			if ui.Button(c.Key("save-run"), tr("Save and run once")).Disabled(blocked).Clicked() {
				app.saveAutomationForm(true)
			}
			label := tr("Create")
			if a.editID != "" {
				label = tr("Save")
			}
			if ui.PrimaryButton(c.Key("save"), label).Disabled(blocked).Clicked() {
				app.saveAutomationForm(false)
			}
		})
	})
}

func (app *chatApp) automationComposer(c *ui.Context) {
	a := &app.autos
	ui.Column(c).FillWidth().Border(1, line).Radius(16).Background(canvas).Children(func() {
		in := ui.TextAreaBase(c.Key("request"), &a.request).Label(tr("What to do")).
			Placeholder(tr("For example: Check new issues every weekday at 9 AM, fix what you can and pass go test, then list anything unresolved")).
			Lines(3, 8).FillWidth().Padding(16, 18, 6, 18).FontSize(15).LineHeight(1.6)
		if in.Changed() {
			app.invalidateDraft()
		}
		ui.Row(c).FillWidth().Gap(4).Padding(4, 10, 10, 10).AlignItems(ui.Center).Children(func() {
			pb := quiet(c.Key("project")).Height(28).Gap(6).PaddingX(9).Label(tr("Projects"))
			pb.Children(func() {
				icon(c, "folder", 14, muted)
				ui.Text(c, filepath.Base(a.project)).FontWeight(500).SingleLine()
				icon(c, "chevron-down", 12, muted)
			})
			if pb.Clicked() {
				a.projectOpen = !a.projectOpen
			}
			ui.PopoverBase(c, pb, &a.projectOpen, func(panel ui.Element) {
				popupPanel(panel, 300)
				for _, p := range app.c.projects() {
					row := quiet(c.Key("p:" + p.Path)).FillWidth().Gap(8).Label(p.Path)
					if p.Path == a.project {
						row.Background(selectedBg)
					}
					row.Children(func() {
						icon(c, "folder", 14, muted)
						ui.Text(c, filepath.Base(p.Path)).Grow(1).SingleLine().Ellipsis("…")
					})
					if row.Clicked() {
						changed := a.project != p.Path
						a.project, a.projectOpen = p.Path, false
						if changed {
							app.invalidateDraft()
						}
					}
				}
			})
			for _, backend := range agentBackends {
				ag := backend.ID()
				b := quiet(c.Key("agent:" + ag)).Height(28).Gap(6).PaddingX(9).Label(backendFor(ag).Name())
				if a.agent == ag {
					b.Background(selectedBg)
				}
				b.Children(func() {
					agentIcon(c, ag, 14)
					ui.Text(c, backendFor(ag).Name()).FontWeight(500).SingleLine()
				})
				if b.Clicked() && a.agent != ag {
					a.agent = ag
					app.invalidateDraft()
				}
			}
			ui.Spacer(c)
			ui.Text(c, tr("Type @ to select and reference other conversations")).FontSize(12).TextColor(muted).SingleLine()
		})
	})
}

func (app *chatApp) draftStatus(c *ui.Context) {
	a := &app.autos
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Margin(6, 2, 0, 2).Children(func() {
		icon(c, "check", 14, ink)
		ui.Text(c, tr("Generated settings")).FontWeight(600).Grow(1)
		switch {
		case a.drafting:
			ui.Spinner(c).FontSize(11)
			ui.Text(c, tr("Generating…")).FontSize(12).TextColor(muted)
		case a.draft != nil && a.draft.TimeMissing:
			ui.Text(c, tr("Schedule needed")).FontSize(12).TextColor(autoWarn)
		case a.draft != nil:
			ui.Text(c, tr("Ready")).FontSize(12).TextColor(autoOK)
		default:
			ui.Text(c, tr("Click Generate settings when you are ready")).FontSize(12).TextColor(muted)
		}
		label := tr("Generate settings")
		if a.draftErr != "" {
			label = tr("Retry")
		}
		if ui.Button(c.Key("generate"), label).Disabled(strings.TrimSpace(a.request) == "" || a.drafting || a.saving).Clicked() {
			app.startDraft()
		}
	})
	if a.draftErr != "" {
		ui.Text(c, a.draftErr).FontSize(12).TextColor(failed).LineHeight(1.5).Selectable()
	}
}

func draftSkeleton(c *ui.Context) {
	widths := [][]float32{{140}, {200, 320}, {160}, {90}, {420, 300}}
	ui.Column(c).FillWidth().Border(1, line).Radius(14).Children(func() {
		for i, ws := range widths {
			ui.Row(c.Key(i)).FillWidth().Gap(12).Padding(14).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Size(70, 10).Radius(5).Background(subtle)
				ui.Column(c).Gap(8).Children(func() {
					for j, w := range ws {
						ui.Box(c.Key(j)).Size(w, 10).Radius(5).Background(subtle)
					}
				})
			})
		}
	})
}

func (app *chatApp) draftPreview(c *ui.Context, d *automationView) {
	ui.Column(c).FillWidth().Border(1, line).Radius(14).Clip().Children(func() {
		app.draftRow(c, "name", "edit", tr("Name"), d.Name, false, func() {
			ui.Text(c, d.Name).Grow(1).MinWidth(0)
		})
		app.draftRow(c, "schedule", "clock", tr("Schedule"), d.Schedule, d.TimeMissing, func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if d.TimeMissing {
						ui.Text(c, tr("No time specified. Using ")+describeCron(d.Schedule)).TextColor(autoWarn)
					} else {
						ui.Text(c, describeCron(d.Schedule)).FontWeight(500)
					}
					codeChip(c, d.Schedule)
				})
				ui.Row(c).Gap(6).Children(func() {
					for i, t := range d.Upcoming {
						chip := ui.Text(c.Key(i), fmt.Sprintf(tr("%d/%d %s %s"), t.Month(), t.Day(), tr(weekdayNames[t.Weekday()]), t.Format("15:04"))).
							FontSize(12).Padding(1, 8, 1, 8).Radius(6).Background(subtle).TextColor(muted)
						if i == 0 {
							chip.TextColor(ink)
						}
					}
				})
			})
		})
		app.draftRow(c, "check", "terminal-2", tr("Completion check"), d.Check, false, func() {
			if d.Check == "" {
				ui.Text(c, tr("No check; finish after one turn")).TextColor(muted).Grow(1)
				return
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					codeChip(c, d.Check)
					ui.Text(c, tr("Passed"))
				})
				ui.Text(c, tr("If the check fails, send the output back to the same conversation to continue fixing")).FontSize(12).TextColor(muted)
			})
		})
		app.draftRow(c, "rounds", "refresh", tr("Turns"), strconv.Itoa(d.Rounds), false, func() {
			text := tr("1 turn")
			if d.Check != "" {
				text = fmt.Sprintf(tr("Up to %d turns"), d.Rounds)
			}
			ui.Text(c, text).Grow(1)
		})
		app.draftRow(c, "instructions", "file-text", tr("Instructions"), d.Instructions, false, func() {
			ui.Text(c, d.Instructions).Grow(1).MinWidth(0).TextColor(muted).LineHeight(1.6).Selectable()
		})
	})
}

func codeChip(c *ui.Context, text string) {
	ui.Text(c, text).Font(monoFont).FontSize(12).Padding(1, 6, 1, 6).Radius(5).Background(subtle).SingleLine()
}

// draftRow is one setting of the draft, which the person can change.
func (app *chatApp) draftRow(c *ui.Context, key, symbol, label, raw string, warn bool, value func()) {
	a := &app.autos
	row := ui.Row(c.Key(key)).FillWidth().Gap(10).Padding(12, 14, 12, 14).AlignItems(ui.Start).BorderWidth(1, 0, 0, 0).BorderColor(line)
	if key == "name" {
		row.BorderWidth(0, 0, 0, 0)
	}
	if warn {
		row.Background(autoWarnBg)
	}
	row.Children(func() {
		color := muted
		if warn {
			color = autoWarn
		}
		icon(c, symbol, 14, color)
		ui.Text(c, label).Width(64).Shrink(0).TextColor(color)
		if a.editing != key {
			value()
			if toolButton(c.Key("edit"), "edit", tr("Change")+label).Clicked() {
				a.editing, a.editText, a.editErr = key, raw, ""
			}
			return
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			var in ui.Element
			if key == "instructions" {
				in = ui.TextArea(c.Key("input"), &a.editText).Lines(2, 8).FillWidth()
			} else {
				in = ui.TextInput(c.Key("input"), &a.editText).FillWidth().AutoFocus()
				if key == "schedule" || key == "check" {
					in.Font(monoFont).FontSize(12)
				}
			}
			if key == "schedule" {
				hint := tr("5-field cron: minute hour day month weekday")
				if s := strings.TrimSpace(a.editText); s != "" {
					if _, err := parseCron(s); err == nil {
						hint = describeCron(s)
					}
				}
				ui.Text(c, hint).FontSize(12).TextColor(muted)
			}
			if a.editErr != "" {
				ui.Text(c, a.editErr).FontSize(12).TextColor(failed)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c.Key("done"), tr("Done")).Clicked() || in.Submitted() {
					app.applyDraftEdit(key)
				}
				if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
					a.editing = ""
				}
			})
		})
	})
}

// applyDraftEdit applies the setting the person changed in the draft.
func (app *chatApp) applyDraftEdit(key string) {
	a := &app.autos
	d := a.draft
	if d == nil {
		return
	}
	text := strings.TrimSpace(a.editText)
	next := d.Automation
	switch key {
	case "name":
		next.Name = text
	case "schedule":
		next.Schedule = text
	case "check":
		next.Check = text
		if text != "" && next.Rounds < 2 {
			next.Rounds = 3
		}
	case "rounds":
		n, err := strconv.Atoi(strings.TrimSuffix(text, tr("turns")))
		if err != nil {
			a.editErr = tr("Enter a number from 1 to 5")
			return
		}
		next.Rounds = n
	case "instructions":
		next.Instructions = text
	}
	if err := next.normalize(); err != nil {
		a.editErr = err.Error()
		return
	}
	if d.ID == "" || d.ID != a.editID {
		// A new automation's id comes from the service as it is saved.
		next.ID = d.ID
	}
	v := automationViewOf(next, d.Runs, d.Running, time.Now())
	v.TimeMissing = d.TimeMissing && key != "schedule"
	a.draft, a.editing, a.editErr = &v, "", ""
}

func (app *chatApp) saveAutomationForm(runNow bool) {
	a := &app.autos
	if a.draft == nil || a.drafting || a.saving || a.editing != "" {
		return
	}
	d := a.draft.Automation
	d.ID, d.Enabled, d.Request, d.Project, d.Agent = a.editID, a.enable, strings.TrimSpace(a.request), a.project, a.agent
	a.saving = true
	app.autoCall("automationSave", automationRequest{Automation: &d}, func(raw json.RawMessage, err error) {
		a.saving = false
		if err != nil {
			a.draftErr = err.Error()
			return
		}
		var v automationView
		_ = json.Unmarshal(raw, &v)
		a.page, a.detail = "detail", v.ID
		replaced := false
		for i := range a.list {
			if a.list[i].ID == v.ID {
				a.list[i], replaced = v, true
			}
		}
		if !replaced {
			a.list = append(a.list, v)
		}
		if runNow {
			app.runAutomationNow(v.ID)
			return
		}
		app.loadAutomations()
	})
}

// latestReportIndex is the newest run with a result, shown open.
func latestReportIndex(runs []AutomationRun) int {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Report != nil {
			return i
		}
	}
	return -1
}

// reportView shows a run's structured result under its row; older ones
// open on a click.
func (app *chatApp) reportView(c *ui.Context, r AutomationRun, latest bool) {
	a := &app.autos
	if a.openReports == nil {
		a.openReports = map[int64]bool{}
	}
	key := r.Started.UnixNano()
	open := latest != a.openReports[key]
	rep := r.Report
	// Indented under the run row, within the page width.
	ui.Row(c).FillWidth().Padding(0, 0, 6, 33).Children(func() {
		ui.Column(c.Key("body")).Grow(1).MinWidth(0).Gap(8).Padding(10, 14, 12, 14).Background(subtle).Radius(10).Children(func() {
			head := quiet(c.Key("head")).FillWidth().Height(0).MinHeight(24).Padding(0).Gap(6).Label(tr("Structured result"))
			head.Children(func() {
				label := tr("Structured result")
				if rep.Auto {
					label = tr("Automatic summary · Agent did not report a result")
				}
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(muted)
				if rep.NeedsUser {
					ui.Text(c, tr("Needs attention")).FontSize(11).Padding(1, 8, 1, 8).Radius(8).Background(autoWarnBg).TextColor(autoWarn)
				}
				ui.Spacer(c)
				app.chevron(c, open)
			})
			if head.Clicked() {
				a.openReports[key] = !a.openReports[key]
			}
			if !open {
				ui.Text(c, firstLine(rep.Summary)).FontSize(12).SingleLine().Ellipsis("…")
				return
			}
			ui.Text(c, rep.Summary).FontSize(13).LineHeight(1.6).Selectable()
			if len(rep.Metrics) > 0 {
				ui.Row(c).Gap(6).Children(func() {
					for i, m := range rep.Metrics {
						ui.Column(c.Key(i)).Gap(1).Padding(6, 10, 6, 10).Radius(8).Background(canvas).Children(func() {
							ui.Text(c, m.Name).FontSize(11).TextColor(muted).SingleLine()
							ui.Text(c, string(m.Value)+m.Unit).FontSize(14).FontWeight(600).SingleLine()
						})
					}
				})
			}
			for i, f := range rep.Findings {
				ui.Text(c.Key(fmt.Sprintf("f%d", i)), "· "+f).FontSize(12).LineHeight(1.5).Selectable()
			}
			if len(rep.Artifacts) > 0 {
				ui.Text(c, tr("Artifacts: ")+strings.Join(rep.Artifacts, ", ")).FontSize(12).TextColor(muted).Selectable()
			}
		})
	})
}
