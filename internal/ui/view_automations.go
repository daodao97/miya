package desktopui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// automationsUI is the Automations page's state; the automations live in
// the service, which the page asks through automationCall.
type automationsUI struct {
	Page     string // "", "detail" or "form"
	Detail   string // the automation the detail page shows
	list     []automation.View
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
	Request     string
	project     string
	Agent       string
	projectOpen bool
	enable      bool
	Draft       *automation.View
	Drafting    bool
	DraftErr    string
	gen         int
	Saving      bool
	editing     string
	editText    string
	editErr     string
}

var autoOK = ui.Hex("#15803d")

var autoBad = ui.Hex("#dc2626")

var autoRun = ui.Hex("#2563eb")

var autoWarn = ui.Hex("#b45309")

var autoOKBg = ui.RGBA(21, 128, 61, 0.1)

var autoBadBg = ui.RGBA(220, 38, 38, 0.1)

var autoRunBg = ui.RGBA(37, 99, 235, 0.1)

var autoWarnBg = ui.RGBA(217, 119, 6, 0.12)

var autoGrayBg = ui.RGBA(120, 120, 128, 0.12)

// agentTint is the color an agent's adapter gives its icon.
func agentTint(agent string) ui.Color {
	if spec := widgets.AgentSpec(agent); spec.Color != "" {
		return ui.Hex(spec.Color)
	}
	return ui.Hex("#7c6cf6")
}

// autoCall runs an automation operation and then done on the main thread,
// never making the window wait.
func (app *App) autoCall(op string, r automation.Request, done func(json.RawMessage, error)) {
	c := app.C
	if c.State().Remote != nil {
		go func() {
			var out json.RawMessage
			err := c.State().Remote.Call(op, r, &out)
			c.Dispatch(func() { done(out, err) })
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
	res, err := c.AutomationOp(op, r)
	if d, ok := res.(async.Reply); ok && err == nil {
		go func() {
			v, err := d()
			c.Dispatch(func() { finish(v, err) })
		}()
		return
	}
	finish(res, err)
}

func (app *App) ShowAutomations() {
	app.Section = "Automations"
	app.Autos.Page = ""
	app.Autos.loadedAt = time.Time{}
}

func (app *App) loadAutomations() {
	a := &app.Autos
	if a.loading {
		return
	}
	a.loading = true
	app.autoCall("automationList", automation.Request{}, func(raw json.RawMessage, err error) {
		a.loading, a.loadedAt = false, time.Now()
		if err != nil {
			a.err = err.Error()
			return
		}
		var list []automation.View
		_ = json.Unmarshal(raw, &list)
		a.list, a.loaded, a.err = list, true, ""
	})
}

func (app *App) automationsView(c *ui.Context) {
	a := &app.Autos
	if !a.loading && time.Since(a.loadedAt) > 5*time.Second {
		app.loadAutomations()
	}
	ui.Column(c).FillWidth().MaxWidth(880).AlignSelf(ui.Center).Gap(18).Children(func() {
		switch a.Page {
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
		widgets.Icon(c, "clock", size*0.46, ui.Hex("#ffffff"))
	})
}

// runBadge says how a run went, or that it runs.
func runBadge(c *ui.Context, run automation.Run, has, running bool) {
	label, fg, bg := tr("Not run yet"), widgets.Muted, autoGrayBg
	switch {
	case running:
		label, fg, bg = fmt.Sprintf(tr("Running · Turn %d/%d"), max(run.Round, 1), max(run.Rounds, 1)), autoRun, autoRunBg
	case !has:
	case run.Result == automation.Passed:
		label, fg, bg = tr("Passed"), autoOK, autoOKBg
		if run.Rounds > 1 {
			label += fmt.Sprintf(tr(" · Turn %d"), run.Round)
		}
	case run.Result == automation.Failed:
		label, fg, bg = tr("Failed"), autoBad, autoBadBg
		if run.Rounds > 1 {
			label += fmt.Sprintf(tr(" · %d/%d turns"), run.Round, run.Rounds)
		}
	default:
		label = automation.ResultLabel(run.Result)
	}
	ui.Row(c).Height(22).PaddingX(9).Gap(5).Radius(11).AlignItems(ui.Center).AlignSelf(ui.Start).Background(bg).Children(func() {
		if running {
			ui.Spinner(c).FontSize(10)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(fg).SingleLine()
	})
}

func automationSubline(v automation.View) string {
	parts := []string{filepath.Base(v.Project), model.AgentName(v.Agent)}
	if v.Check != "" {
		parts = append(parts, tr("Completion criteria ")+v.Check)
	} else {
		parts = append(parts, tr("Single turn"))
	}
	return strings.Join(parts, " · ")
}

func (app *App) automationList(c *ui.Context) {
	a := &app.Autos
	ui.Row(c).FillWidth().AlignItems(ui.End).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, tr("Automations")).FontSize(26).FontWeight(600)
			ui.Text(c, tr("Start a new conversation in the project on schedule, then send the result to your phone.")).TextColor(widgets.Muted)
		})
		if ui.PrimaryButton(c.Key("new"), tr("New")).Clicked() {
			app.OpenAutomationForm(nil)
		}
	})
	if a.err != "" {
		ui.Text(c, a.err).FontSize(12).TextColor(widgets.Failed)
	}
	switch {
	case !a.loaded:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).FontSize(12)
			ui.Text(c, tr("Loading…")).TextColor(widgets.Muted)
		})
	case len(a.list) == 0:
		ui.Column(c).FillWidth().Padding(28).Gap(6).AlignItems(ui.Center).Border(1, widgets.Line).Radius(12).Children(func() {
			widgets.Icon(c, "clock", 22, widgets.Muted)
			ui.Text(c, tr("No automations yet")).FontWeight(600)
			ui.Text(c, tr("Click New to describe a task, or ask an agent to create one in any conversation.")).FontSize(12).TextColor(widgets.Muted)
		})
	default:
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for _, v := range a.list {
				app.automationRow(c.Key(v.ID), v)
			}
		})
	}
	ui.Row(c).FillWidth().Gap(12).Padding(14, 16, 14, 16).Border(1, widgets.Line).Radius(12).AlignItems(ui.Start).Children(func() {
		widgets.Icon(c, "message-circle", 16, widgets.Ink)
		ui.Text(c, tr("Describe a recurring task in any conversation. It appears here after confirmation. Enabled automations keep running when the window is closed.")).
			Grow(1).MinWidth(0).FontSize(12).LineHeight(1.6).TextColor(widgets.Muted)
	})
}

func (app *App) automationRow(c *ui.Context, v automation.View) {
	a := &app.Autos
	last, has := widgets.LastAutomationRun(v)
	ui.Row(c).FillWidth().Gap(14).Padding(14, 16, 14, 16).AlignItems(ui.Center).Border(1, widgets.Line).Radius(12).Children(func() {
		autoTile(c, v.Agent, 36)
		open := widgets.Quiet(c.Key("open")).Grow(1).MinWidth(0).Height(0).MinHeight(40).Padding(0).Column().AlignItems(ui.Start).Justify(ui.Center).Gap(2).Label(v.Name)
		open.Children(func() {
			name := ui.Text(c, v.Name).FontSize(14).FontWeight(600).SingleLine().Ellipsis("…")
			if !v.Enabled {
				name.TextColor(widgets.Muted)
			}
			ui.Text(c, automationSubline(v)).FontSize(12).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		})
		if open.Clicked() {
			a.Page, a.Detail, a.confirmDelete = "detail", v.ID, ""
		}
		ui.Column(c).Width(150).Shrink(0).Gap(1).Children(func() {
			ui.Text(c, automation.DescribeCron(v.Schedule)).FontWeight(500).SingleLine().Ellipsis("…")
			next := tr("Paused")
			if v.Enabled {
				next = tr("Next ") + automation.FormatTime(v.Next)
			}
			ui.Text(c, next).FontSize(12).TextColor(widgets.Muted).SingleLine()
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
			if !v.Running && widgets.ToolButton(c.Key("run"), "arrow-right", tr("Run now")).Clicked() {
				app.runAutomationNow(v.ID)
			}
			if has && last.Thread != 0 && widgets.ToolButton(c.Key("last"), "message-circle", tr("Open last conversation")).Clicked() {
				app.openAutomationThread(v.Project, last.Thread)
			}
		})
	})
}

func (app *App) toggleAutomation(id string, on bool) {
	a := &app.Autos
	for i := range a.list {
		if a.list[i].ID == id {
			a.list[i].Enabled = on
		}
	}
	app.autoCall("automationToggle", automation.Request{ID: id, Enabled: on}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		app.loadAutomations()
	})
}

func (app *App) runAutomationNow(id string) {
	a := &app.Autos
	app.autoCall("automationRunNow", automation.Request{ID: id}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		app.loadAutomations()
	})
}

// openAutomationThread opens a run's conversation, in its project.
func (app *App) openAutomationThread(project string, id int) {
	c := app.C
	if project != "" && project != c.State().Cwd {
		if err := c.ChangeWorkspace(project); err != nil {
			app.Autos.err = err.Error()
			return
		}
	}
	if t := c.ThreadWithID(id); t.ID != 0 {
		c.SelectThread(t)
	} else {
		c.State().SetActive(id)
	}
	app.Section = ""
}

func backLink(c *ui.Context, label string) bool {
	b := widgets.Quiet(c.Key("back")).AlignSelf(ui.Start).PaddingX(6).Gap(4).Label(label)
	b.Children(func() {
		widgets.Icon(c, "arrow-left", 14, widgets.Muted)
		ui.Text(c, label).TextColor(widgets.Muted)
	})
	return b.Clicked()
}

func (app *App) automationDetail(c *ui.Context) {
	a := &app.Autos
	var v automation.View
	found := false
	for _, x := range a.list {
		if x.ID == a.Detail {
			v, found = x, true
		}
	}
	if backLink(c, "Automations") || (!found && a.loaded) {
		a.Page = ""
		return
	}
	if !found {
		return
	}
	ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
		autoTile(c, v.Agent, 48)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, v.Name).FontSize(24).FontWeight(600).SingleLine().Ellipsis("…")
			ui.Text(c, filepath.Base(v.Project)+" · "+model.AgentName(v.Agent)+" · "+automation.DescribeCron(v.Schedule)).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		})
		on := v.Enabled
		if ui.Switch(c.Key("on"), &on).Shrink(0).Changed() {
			app.toggleAutomation(v.ID, on)
		}
		if ui.Button(c.Key("edit"), tr("Edit")).Clicked() {
			app.OpenAutomationForm(&v)
		}
		if ui.PrimaryButton(c.Key("run"), tr("Run now")).Disabled(v.Running).Clicked() {
			app.runAutomationNow(v.ID)
		}
		if a.confirmDelete == v.ID {
			if ui.Button(c.Key("delete-confirm"), tr("Confirm deletion")).Clicked() {
				app.deleteAutomation(v.ID)
			}
		} else if widgets.ToolButton(c.Key("delete"), "trash-2", tr("Delete")).Clicked() {
			a.confirmDelete = v.ID
		}
	})
	passed, total, rounds := 0, 0, 0
	for _, r := range v.Runs {
		if r.Result == automation.Skipped || r.Result == automation.Running {
			continue
		}
		total++
		rounds += max(r.Round, 1)
		if r.Result == automation.Passed {
			passed++
		}
	}
	ui.Row(c).FillWidth().Gap(10).Children(func() {
		next := tr("Paused")
		if v.Enabled {
			next = automation.FormatTime(v.Next)
		}
		stat(c, tr("Next run"), next)
		if total > 0 {
			stat(c, fmt.Sprintf(tr("Last %d runs"), total), fmt.Sprintf(tr("%d passed"), passed))
			stat(c, tr("Average"), fmt.Sprintf(tr("%.1f turns"), float64(rounds)/float64(total)))
		} else {
			stat(c, tr("Run history"), tr("No runs yet"))
		}
	})
	ui.Column(c).FillWidth().Gap(10).Padding(16).Border(1, widgets.Line).Radius(12).Children(func() {
		ui.Text(c, tr("Instructions")).FontSize(12).TextColor(widgets.Muted)
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
			ui.Text(c, tr("Original request: ")+v.Request).FontSize(12).TextColor(widgets.Muted).LineHeight(1.5)
		}
	})
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		carry := !v.NoCarryOver
		if ui.Switch(c.Key("carry"), &carry).Label(tr("Include previous result")).Shrink(0).Changed() {
			next := v.Automation
			next.NoCarryOver = !carry
			app.autoCall("automationSave", automation.Request{Automation: &next}, func(_ json.RawMessage, err error) {
				if err != nil {
					app.Autos.err = err.Error()
				}
				app.loadAutomations()
			})
		}
		ui.Text(c, tr("Include the previous structured result with each run so the agent can compare changes")).FontSize(12).TextColor(widgets.Muted)
	})
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		isolated := v.Worktree
		if ui.Switch(c.Key("worktree"), &isolated).Label(tr("Run in a separate worktree")).Shrink(0).Changed() {
			next := v.Automation
			next.Worktree = isolated
			app.autoCall("automationSave", automation.Request{Automation: &next}, func(_ json.RawMessage, err error) {
				if err != nil {
					app.Autos.err = err.Error()
				}
				app.loadAutomations()
			})
		}
		ui.Text(c, tr("Each run works on its own Git branch, so it cannot collide with your edits; merge or discard its changes afterwards")).FontSize(12).TextColor(widgets.Muted)
	})
	ui.Row(c).FillWidth().AlignItems(ui.End).Margin(8, 0, 0, 0).Children(func() {
		ui.Text(c, tr("Run history")).FontWeight(600).Grow(1)
		ui.Text(c, fmt.Sprintf(tr("Last %d runs"), automation.RunHistory)).FontSize(12).TextColor(widgets.Muted)
	})
	if len(v.Runs) == 0 {
		ui.Text(c, tr("No runs yet.")).TextColor(widgets.Muted)
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
	ui.Column(c).Grow(1).MinWidth(0).Gap(2).Padding(12, 14, 12, 14).Border(1, widgets.Line).Radius(12).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(widgets.Muted)
		ui.Text(c, value).FontSize(17).FontWeight(600).SingleLine().Ellipsis("…")
	})
}

func settingChip(c *ui.Context, symbol, text string) {
	ui.Row(c).Gap(5).Height(24).PaddingX(9).Radius(7).Border(1, widgets.Line).AlignItems(ui.Center).Children(func() {
		widgets.Icon(c, symbol, 12, widgets.Muted)
		ui.Text(c, text).FontSize(12).Font(widgets.MonoFont).SingleLine()
	})
}

func (app *App) runRow(c *ui.Context, v automation.View, r automation.Run, running bool) {
	color, title := ui.Color(widgets.Muted), r.Summary
	switch {
	case running:
		color, title = autoRun, fmt.Sprintf(tr("Running · Turn %d/%d"), max(r.Round, 1), max(r.Rounds, 1))
	case r.Result == automation.Passed:
		color = autoOK
	case r.Result == automation.Failed:
		color = autoBad
	}
	title = textutil.FirstNonEmpty(title, automation.ResultLabel(r.Result))
	if r.Result == automation.Skipped {
		title = tr("Skipped, ") + r.Summary
	}
	meta := r.Started.Format("Jan 2 ") + tr(automation.WeekdayNames[r.Started.Weekday()]) + r.Started.Format(" 15:04")
	if r.CatchUp {
		meta += tr(" · Catch-up run")
	}
	if r.Result != automation.Skipped {
		meta += fmt.Sprintf(tr(" · %s · Turn %d/%d"), automation.ResultLabel(r.Result), max(r.Round, 1), max(r.Rounds, 1))
		if !r.Finished.IsZero() {
			meta += fmt.Sprintf(tr(" · %d minutes"), max(int(r.Finished.Sub(r.Started).Minutes()+0.5), 1))
		}
	}
	// The caller keys each row.
	row := widgets.Quiet(c).FillWidth().Height(0).MinHeight(52).Padding(8, 12, 8, 12).Gap(12).AlignItems(ui.Center).Label(title)
	row.Children(func() {
		ui.Box(c).Size(9, 9).Radius(4.5).Shrink(0).Background(color)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			t := ui.Text(c, title).FontWeight(500).SingleLine().Ellipsis("…")
			if r.Result == automation.Skipped {
				t.TextColor(widgets.Muted)
			}
			ui.Text(c, meta).FontSize(12).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		})
		if r.Thread != 0 {
			ui.Text(c, tr("Open conversation")).FontSize(12).TextColor(widgets.Muted)
			widgets.Icon(c, "arrow-right", 12, widgets.Muted)
		}
	})
	if row.Clicked() && r.Thread != 0 {
		app.openAutomationThread(automation.Workspace(v.Project, r), r.Thread)
	}
	app.worktreeRow(c.Key("worktree"), v, r)
}

// worktreeRow shows what a run left in its worktree and, while it waits,
// merges or discards it.
func (app *App) worktreeRow(c *ui.Context, v automation.View, r automation.Run) {
	label := automation.WorktreeLabel(r)
	if label == "" {
		return
	}
	if automation.WorktreePending(r) && (r.Changes.Commits > 0 || r.Changes.Files > 0) {
		label += fmt.Sprintf(tr(" · %d commits, %d uncommitted files"), r.Changes.Commits, r.Changes.Files)
	}
	ui.Row(c).FillWidth().Gap(8).Padding(0, 12, 6, 33).AlignItems(ui.Center).Children(func() {
		widgets.Icon(c, "git-branch", 12, widgets.Muted)
		ui.Text(c, label).Grow(1).MinWidth(0).FontSize(12).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		if !automation.WorktreePending(r) {
			return
		}
		act := func(action string) {
			app.autoCall("automationWorktree", automation.Request{ID: v.ID, Started: r.Started, Action: action}, func(_ json.RawMessage, err error) {
				if err != nil {
					app.Autos.err = err.Error()
				}
				app.loadAutomations()
			})
		}
		if widgets.Quiet(c.Key("discard")).PaddingX(8).Label(tr("Discard")).Children(func() {
			ui.Text(c, tr("Discard")).FontSize(12)
		}).Clicked() {
			act(automation.WorktreeDiscard)
		}
		if ui.PrimaryButton(c.Key("merge"), tr("Merge")).Clicked() {
			act(automation.WorktreeMerge)
		}
	})
	if r.WorktreeError != "" {
		ui.Text(c, r.WorktreeError).Padding(0, 12, 6, 33).FontSize(12).TextColor(widgets.Danger)
	}
}

func (app *App) deleteAutomation(id string) {
	a := &app.Autos
	app.autoCall("automationDelete", automation.Request{ID: id}, func(_ json.RawMessage, err error) {
		if err != nil {
			a.err = err.Error()
		}
		a.Page, a.confirmDelete = "", ""
		app.loadAutomations()
	})
}

// openAutomationForm starts a new automation, or edits v.
func (app *App) OpenAutomationForm(v *automation.View) {
	a := &app.Autos
	c := app.C
	a.Page, a.editID, a.Request, a.Draft, a.DraftErr, a.Drafting, a.editing, a.editErr = "form", "", "", nil, "", false, "", ""
	a.enable, a.project, a.Agent, a.projectOpen = true, c.State().Cwd, model.Backends[0], false
	if c.State().Agent != nil {
		a.Agent = c.State().Agent()
	}
	a.gen++
	if v != nil {
		draft := *v
		a.editID, a.Request, a.Draft = v.ID, textutil.FirstNonEmpty(v.Request, v.Instructions), &draft
		a.project, a.Agent, a.enable = v.Project, v.Agent, v.Enabled
	}
}

// invalidateDraft discards settings and in-flight results after an input changes.
// Only the explicit generate button starts another request.
func (app *App) invalidateDraft() {
	a := &app.Autos
	a.gen++
	a.Draft, a.Drafting, a.DraftErr, a.editing, a.editErr = nil, false, "", "", ""
}

func (app *App) startDraft() {
	a := &app.Autos
	if strings.TrimSpace(a.Request) == "" || a.Drafting || a.Saving {
		return
	}
	app.invalidateDraft()
	g := a.gen
	a.Drafting, a.DraftErr = true, ""
	req := automation.Request{Request: a.Request, Project: a.project, Agent: a.Agent}
	app.autoCall("automationDraft", req, func(raw json.RawMessage, err error) {
		if g != a.gen {
			return
		}
		a.Drafting = false
		if err != nil {
			a.DraftErr = err.Error()
			return
		}
		var v automation.View
		if json.Unmarshal(raw, &v) != nil {
			a.DraftErr = tr("Unable to read generated settings")
			return
		}
		a.Draft, a.editing = &v, ""
	})
}

func (app *App) automationForm(c *ui.Context) {
	a := &app.Autos
	ui.Column(c).FillWidth().MaxWidth(680).AlignSelf(ui.Center).Gap(16).Children(func() {
		if backLink(c, "Automations") {
			a.Page = ""
			if a.editID != "" {
				a.Page = "detail"
			}
			return
		}
		title := tr("New automation")
		if a.editID != "" {
			title = tr("Edit automation")
		}
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(24).FontWeight(600)
			ui.Text(c, tr("Describe what to do and when, then click Generate settings to organize the schedule, completion criteria, and turn limit.")).TextColor(widgets.Muted)
		})
		app.automationComposer(c)
		app.draftStatus(c)
		switch {
		case a.Draft != nil:
			app.draftPreview(c, a.Draft)
		case a.Drafting:
			draftSkeleton(c)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Margin(4, 0, 0, 0).Children(func() {
			ui.Switch(c.Key("enable"), &a.enable).Label(tr("Enable after saving")).Shrink(0)
			ui.Text(c, tr("Enable after saving")).TextColor(widgets.Muted)
			ui.Spacer(c)
			if ui.Button(c.Key("cancel"), tr("Cancel")).Clicked() {
				a.Page = ""
				if a.editID != "" {
					a.Page = "detail"
				}
			}
			blocked := a.Draft == nil || a.Drafting || a.Saving || a.editing != ""
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

func (app *App) automationComposer(c *ui.Context) {
	a := &app.Autos
	ui.Column(c).FillWidth().Border(1, widgets.Line).Radius(16).Background(widgets.Canvas).Children(func() {
		in := ui.TextAreaBase(c.Key("request"), &a.Request).Label(tr("What to do")).
			Placeholder(tr("For example: Check new issues every weekday at 9 AM, fix what you can and pass go test, then list anything unresolved")).
			Lines(3, 8).FillWidth().Padding(16, 18, 6, 18).FontSize(15).LineHeight(1.6)
		if in.Changed() {
			app.invalidateDraft()
		}
		ui.Row(c).FillWidth().Gap(4).Padding(4, 10, 10, 10).AlignItems(ui.Center).Children(func() {
			pb := widgets.Quiet(c.Key("project")).Height(28).Gap(6).PaddingX(9).Label(tr("Projects"))
			pb.Children(func() {
				widgets.Icon(c, "folder", 14, widgets.Muted)
				ui.Text(c, filepath.Base(a.project)).FontWeight(500).SingleLine()
				widgets.Icon(c, "chevron-down", 12, widgets.Muted)
			})
			if pb.Clicked() {
				a.projectOpen = !a.projectOpen
			}
			ui.PopoverBase(c, pb, &a.projectOpen, func(panel ui.Element) {
				widgets.PopupPanel(panel, 300)
				for _, p := range app.C.State().Projects() {
					row := widgets.Quiet(c.Key("p:" + p.Path)).FillWidth().Gap(8).Label(p.Path)
					if p.Path == a.project {
						row.Background(widgets.SelectedBg)
					}
					row.Children(func() {
						widgets.Icon(c, "folder", 14, widgets.Muted)
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
			for _, ag := range model.Backends {
				if ag != a.Agent && !slices.Contains(app.C.EnabledAgents(), ag) {
					continue
				}
				b := widgets.Quiet(c.Key("agent:" + ag)).Height(28).Gap(6).PaddingX(9).Label(model.AgentName(ag))
				if a.Agent == ag {
					b.Background(widgets.SelectedBg)
				}
				b.Children(func() {
					widgets.AgentIcon(c, ag, 14)
					ui.Text(c, model.AgentName(ag)).FontWeight(500).SingleLine()
				})
				if b.Clicked() && a.Agent != ag {
					a.Agent = ag
					app.invalidateDraft()
				}
			}
			ui.Spacer(c)
			ui.Text(c, tr("Type @ to select and reference other conversations")).FontSize(12).TextColor(widgets.Muted).SingleLine()
		})
	})
}

func (app *App) draftStatus(c *ui.Context) {
	a := &app.Autos
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Margin(6, 2, 0, 2).Children(func() {
		widgets.Icon(c, "check", 14, widgets.Ink)
		ui.Text(c, tr("Generated settings")).FontWeight(600).Grow(1)
		switch {
		case a.Drafting:
			ui.Spinner(c).FontSize(11)
			ui.Text(c, tr("Generating…")).FontSize(12).TextColor(widgets.Muted)
		case a.Draft != nil && a.Draft.TimeMissing:
			ui.Text(c, tr("Schedule needed")).FontSize(12).TextColor(autoWarn)
		case a.Draft != nil:
			ui.Text(c, tr("Ready")).FontSize(12).TextColor(autoOK)
		default:
			ui.Text(c, tr("Click Generate settings when you are ready")).FontSize(12).TextColor(widgets.Muted)
		}
		label := tr("Generate settings")
		if a.DraftErr != "" {
			label = tr("Retry")
		}
		if ui.Button(c.Key("generate"), label).Disabled(strings.TrimSpace(a.Request) == "" || a.Drafting || a.Saving).Clicked() {
			app.startDraft()
		}
	})
	if a.DraftErr != "" {
		ui.Text(c, a.DraftErr).FontSize(12).TextColor(widgets.Failed).LineHeight(1.5).Selectable()
	}
}

func draftSkeleton(c *ui.Context) {
	widths := [][]float32{{140}, {200, 320}, {160}, {90}, {420, 300}}
	ui.Column(c).FillWidth().Border(1, widgets.Line).Radius(14).Children(func() {
		for i, ws := range widths {
			ui.Row(c.Key(i)).FillWidth().Gap(12).Padding(14).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Size(70, 10).Radius(5).Background(widgets.Subtle)
				ui.Column(c).Gap(8).Children(func() {
					for j, w := range ws {
						ui.Box(c.Key(j)).Size(w, 10).Radius(5).Background(widgets.Subtle)
					}
				})
			})
		}
	})
}

func (app *App) draftPreview(c *ui.Context, d *automation.View) {
	ui.Column(c).FillWidth().Border(1, widgets.Line).Radius(14).Clip().Children(func() {
		app.draftRow(c, "name", "edit", tr("Name"), d.Name, false, func() {
			ui.Text(c, d.Name).Grow(1).MinWidth(0)
		})
		app.draftRow(c, "schedule", "clock", tr("Schedule"), d.Schedule, d.TimeMissing, func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if d.TimeMissing {
						ui.Text(c, tr("No time specified. Using ")+automation.DescribeCron(d.Schedule)).TextColor(autoWarn)
					} else {
						ui.Text(c, automation.DescribeCron(d.Schedule)).FontWeight(500)
					}
					codeChip(c, d.Schedule)
				})
				ui.Row(c).Gap(6).Children(func() {
					for i, t := range d.Upcoming {
						chip := ui.Text(c.Key(i), fmt.Sprintf(tr("%d/%d %s %s"), t.Month(), t.Day(), tr(automation.WeekdayNames[t.Weekday()]), t.Format("15:04"))).
							FontSize(12).Padding(1, 8, 1, 8).Radius(6).Background(widgets.Subtle).TextColor(widgets.Muted)
						if i == 0 {
							chip.TextColor(widgets.Ink)
						}
					}
				})
			})
		})
		app.draftRow(c, "check", "terminal-2", tr("Completion check"), d.Check, false, func() {
			if d.Check == "" {
				ui.Text(c, tr("No check; finish after one turn")).TextColor(widgets.Muted).Grow(1)
				return
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					codeChip(c, d.Check)
					ui.Text(c, tr("Passed"))
				})
				ui.Text(c, tr("If the check fails, send the output back to the same conversation to continue fixing")).FontSize(12).TextColor(widgets.Muted)
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
			ui.Text(c, d.Instructions).Grow(1).MinWidth(0).TextColor(widgets.Muted).LineHeight(1.6).Selectable()
		})
	})
}

func codeChip(c *ui.Context, text string) {
	ui.Text(c, text).Font(widgets.MonoFont).FontSize(12).Padding(1, 6, 1, 6).Radius(5).Background(widgets.Subtle).SingleLine()
}

// draftRow is one setting of the draft, which the person can change.
func (app *App) draftRow(c *ui.Context, key, symbol, label, raw string, warn bool, value func()) {
	a := &app.Autos
	row := ui.Row(c.Key(key)).FillWidth().Gap(10).Padding(12, 14, 12, 14).AlignItems(ui.Start).BorderWidth(1, 0, 0, 0).BorderColor(widgets.Line)
	if key == "name" {
		row.BorderWidth(0, 0, 0, 0)
	}
	if warn {
		row.Background(autoWarnBg)
	}
	row.Children(func() {
		color := widgets.Muted
		if warn {
			color = autoWarn
		}
		widgets.Icon(c, symbol, 14, color)
		ui.Text(c, label).Width(64).Shrink(0).TextColor(color)
		if a.editing != key {
			value()
			if widgets.ToolButton(c.Key("edit"), "edit", tr("Change")+label).Clicked() {
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
					in.Font(widgets.MonoFont).FontSize(12)
				}
			}
			if key == "schedule" {
				hint := tr("5-field cron: minute hour day month weekday")
				if s := strings.TrimSpace(a.editText); s != "" {
					if _, err := automation.ParseCron(s); err == nil {
						hint = automation.DescribeCron(s)
					}
				}
				ui.Text(c, hint).FontSize(12).TextColor(widgets.Muted)
			}
			if a.editErr != "" {
				ui.Text(c, a.editErr).FontSize(12).TextColor(widgets.Failed)
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
func (app *App) applyDraftEdit(key string) {
	a := &app.Autos
	d := a.Draft
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
	if err := next.Normalize(); err != nil {
		a.editErr = err.Error()
		return
	}
	if d.ID == "" || d.ID != a.editID {
		// A new automation's id comes from the service as it is saved.
		next.ID = d.ID
	}
	v := automation.ViewOf(next, d.Runs, d.Running, time.Now())
	v.TimeMissing = d.TimeMissing && key != "schedule"
	a.Draft, a.editing, a.editErr = &v, "", ""
}

func (app *App) saveAutomationForm(runNow bool) {
	a := &app.Autos
	if a.Draft == nil || a.Drafting || a.Saving || a.editing != "" {
		return
	}
	d := a.Draft.Automation
	d.ID, d.Enabled, d.Request, d.Project, d.Agent = a.editID, a.enable, strings.TrimSpace(a.Request), a.project, a.Agent
	a.Saving = true
	app.autoCall("automationSave", automation.Request{Automation: &d}, func(raw json.RawMessage, err error) {
		a.Saving = false
		if err != nil {
			a.DraftErr = err.Error()
			return
		}
		var v automation.View
		_ = json.Unmarshal(raw, &v)
		a.Page, a.Detail = "detail", v.ID
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
func latestReportIndex(runs []automation.Run) int {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Report != nil {
			return i
		}
	}
	return -1
}

// reportView shows a run's structured result under its row; older ones
// open on a click.
func (app *App) reportView(c *ui.Context, r automation.Run, latest bool) {
	a := &app.Autos
	if a.openReports == nil {
		a.openReports = map[int64]bool{}
	}
	key := r.Started.UnixNano()
	open := latest != a.openReports[key]
	rep := r.Report
	// Indented under the run row, within the page width.
	ui.Row(c).FillWidth().Padding(0, 0, 6, 33).Children(func() {
		ui.Column(c.Key("body")).Grow(1).MinWidth(0).Gap(8).Padding(10, 14, 12, 14).Background(widgets.Subtle).Radius(10).Children(func() {
			head := widgets.Quiet(c.Key("head")).FillWidth().Height(0).MinHeight(24).Padding(0).Gap(6).Label(tr("Structured result"))
			head.Children(func() {
				label := tr("Structured result")
				if rep.Auto {
					label = tr("Automatic summary · Agent did not report a result")
				}
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(widgets.Muted)
				if rep.NeedsUser {
					ui.Text(c, tr("Needs attention")).FontSize(11).Padding(1, 8, 1, 8).Radius(8).Background(autoWarnBg).TextColor(autoWarn)
				}
				ui.Spacer(c)
				app.Chevron(c, open)
			})
			if head.Clicked() {
				a.openReports[key] = !a.openReports[key]
			}
			if !open {
				ui.Text(c, textutil.FirstLine(rep.Summary)).FontSize(12).SingleLine().Ellipsis("…")
				return
			}
			ui.Text(c, rep.Summary).FontSize(13).LineHeight(1.6).Selectable()
			if len(rep.Metrics) > 0 {
				ui.Row(c).Gap(6).Children(func() {
					for i, m := range rep.Metrics {
						ui.Column(c.Key(i)).Gap(1).Padding(6, 10, 6, 10).Radius(8).Background(widgets.Canvas).Children(func() {
							ui.Text(c, m.Name).FontSize(11).TextColor(widgets.Muted).SingleLine()
							ui.Text(c, string(m.Value)+m.Unit).FontSize(14).FontWeight(600).SingleLine()
						})
					}
				})
			}
			for i, f := range rep.Findings {
				ui.Text(c.Key(fmt.Sprintf("f%d", i)), "· "+f).FontSize(12).LineHeight(1.5).Selectable()
			}
			if len(rep.Artifacts) > 0 {
				ui.Text(c, tr("Artifacts: ")+strings.Join(rep.Artifacts, ", ")).FontSize(12).TextColor(widgets.Muted).Selectable()
			}
		})
	})
}
