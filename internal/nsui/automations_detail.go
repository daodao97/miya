package nsui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

type automationDetailState struct {
	wakeData map[string]*core.Signal[string]
	reports  map[string]*core.Signal[bool]
}

func (a *automationsState) detail(cx *core.Scope, id string) core.Node {
	state := &automationDetailState{wakeData: map[string]*core.Signal[string]{}, reports: map[string]*core.Signal[bool]{}}
	confirm := core.NewSignal(cx, false)
	return autoColumn(autoButton("automation-back", "Automations", a.back), core.Dynamic(func(cx *core.Scope) core.Node {
		var v automation.View
		found := false
		for _, item := range a.list.Get() {
			if item.ID == id {
				v = item
				found = true
				break
			}
		}
		if !found {
			return autoMuted(cx, tr("No automations yet"))
		}
		bots := []ui.Option[string]{{Value: "", Label: tr("No Bot")}}
		foundBot := false
		for _, b := range Live(a.host.Bots)() {
			if b.WorksIn(v.Project) {
				bots = append(bots, ui.Option[string]{Value: b.ID, Label: b.Name})
				foundBot = foundBot || b.ID == v.Bot
			}
		}
		if v.Bot != "" && !foundBot {
			bots = append(bots, ui.Option[string]{Value: v.Bot, Label: v.Bot})
		}
		var assignment core.Node
		if len(bots) > 1 {
			assignment = ui.FormSection{Children: []core.Node{ui.FormRow{
				Title: tr("Bot"), Detail: tr("Each run is a new conversation of the Bot, under its charter, with its agent"),
				Control: settingsSelect{TestID: "automation-bot", Label: tr("Bot"), Options: bots,
					Value: func() string { return v.Bot }, Disabled: a.pending.Get, OnChange: func(id string) {
						next := v.Automation
						next.Bot = id
						a.save(next)
					}},
			}}}
		}
		children := []core.Node{
			autoRow(autoHeading(v.Name), ui.Switch{TestID: "automation-detail-toggle", Label: tr("Enabled"), Checked: func() bool { return v.Enabled }, OnChange: func(on bool) { a.toggle(id, on) }},
				autoButton("automation-edit", "Edit", func() { a.openForm(&v) }), ui.Button{TestID: "automation-detail-run", Text: tr("Run now"), Disabled: func() bool { return a.pending.Get() || v.Running }, OnPress: func() { a.run(id) }},
				core.Show{When: confirm.Get, Then: func(cx *core.Scope) core.Node {
					return autoButton("automation-delete-confirm", "Confirm deletion", func() {
						a.mutate("automationDelete", automation.Request{ID: id}, func(_ json.RawMessage) { a.back() })
					})
				}, Else: func(cx *core.Scope) core.Node {
					return autoButton("automation-delete", "Delete", func() { confirm.Set(true) })
				}},
			),
			autoMuted(cx, filepath.Base(v.Project)+" · "+model.AgentName(v.Agent)+" · "+v.ScheduleText),
			a.stats(cx, v),
			a.reviewControls(cx, v),
			assignment,
			ui.Select[automation.Access]{TestID: "automation-access", Label: tr("Access"),
				Options: automationAccessOptions(), Value: func() automation.Access { return v.Access },
				OnChange: func(access automation.Access) { next := v.Automation; next.Access = access; a.save(next) }},
			a.switchRow("automation-recheck", "Recheck before merging", func() bool { return v.Recheck }, func(on bool) {
				next := v.Automation
				next.Recheck = on
				a.save(next)
			}),
			a.switchRow("automation-notify-every", "Notify for every run", func() bool { return v.NotifyEveryRun }, func(on bool) {
				next := v.Automation
				next.NotifyEveryRun = on
				a.save(next)
			}),
			a.switchRow("automation-notify-never", "Disable phone notifications", func() bool { return v.NotifyNever }, func(on bool) {
				next := v.Automation
				next.NotifyNever = on
				a.save(next)
			}),
			autoCard(cx, autoText(tr("Instructions")), autoText(v.Instructions), autoMuted(cx, v.Schedule), autoMuted(cx, autoCheck(v.Automation)), autoMuted(cx, tr("Original request: ")+v.Request)),
			a.triggers(cx, v, false, state),
			a.switchRow("automation-carry", "Include previous result", func() bool { return !v.NoCarryOver }, func(on bool) { next := v.Automation; next.NoCarryOver = !on; a.save(next) }),
			autoMuted(cx, tr("Include the previous structured result with each run so the agent can compare changes")),
			a.switchRow("automation-worktree", "Run in a separate worktree", func() bool { return v.Worktree }, func(on bool) { next := v.Automation; next.Worktree = on; a.save(next) }),
			autoMuted(cx, tr("Each run works on its own Git branch, so it cannot collide with your edits; merge or discard its changes afterwards")),
			autoText(tr("Run history")),
		}
		if len(v.Runs) == 0 {
			children = append(children, autoMuted(cx, tr("No runs yet.")))
		}
		latest := -1
		for i := len(v.Runs) - 1; i >= 0; i-- {
			if v.Runs[i].Report != nil {
				latest = i
				break
			}
		}
		for i := len(v.Runs) - 1; i >= 0; i-- {
			children = append(children, a.runRow(cx, v, v.Runs[i], i == latest, state))
		}
		return core.View{TestID: "automation-detail", Style: core.Style{Gap: 14}, Children: children}
	}))
}

func (a *automationsState) stats(cx *core.Scope, v automation.View) core.Node {
	total, passed, rounds := 0, 0, 0
	for _, r := range v.Runs {
		if r.Result == automation.Skipped || r.Result == automation.Running {
			continue
		}
		total++
		rounds += max(1, r.Round)
		if r.Result == automation.Passed {
			passed++
		}
	}
	children := []core.Node{autoCard(cx, autoMuted(cx, tr("Next run")), autoText(autoNext(v)))}
	if total > 0 {
		children = append(children, autoCard(cx, autoMuted(cx, fmt.Sprintf(tr("Last %d runs"), total)), autoText(fmt.Sprintf(tr("%d passed"), passed))), autoCard(cx, autoMuted(cx, tr("Average")), autoText(fmt.Sprintf(tr("%.1f turns"), float64(rounds)/float64(total)))))
	}
	return autoRow(children...)
}

func autoTriggerIcon(t automation.Trigger) string {
	switch t.Kind {
	case automation.TriggerCondition:
		return "terminal-2"
	case automation.TriggerIngress:
		return "globe"
	case automation.TriggerFile:
		return "file"
	case automation.TriggerAfter:
		return "arrow-right"
	case automation.TriggerSystem:
		return "device-desktop"
	}
	return "bulb"
}
func autoTriggerState(t automation.Trigger, st automation.TriggerState, ok bool) string {
	switch {
	case t.Disabled:
		return tr("Off")
	case !ok:
		return tr("Not checked yet")
	case st.Error != "":
		return tr("Check failed: ") + st.Error
	case t.Kind != automation.TriggerCondition:
		return tr("Watching")
	}
	text := st.Value
	if text == "" && t.Condition != nil && t.Condition.When == automation.WhenLine {
		text = tr("Listening")
	}
	if !st.LastProbe.IsZero() {
		text += " · " + automation.FormatTime(st.LastProbe)
	}
	if st.Fired {
		text += " · " + tr("Woke it; waits to recover")
	}
	return text
}
func autoTrial(t automation.Trial) string {
	text := tr("Test run: not met right now")
	switch {
	case t.When == automation.WhenLine:
		text = tr("Test run: the command stays running")
	case t.When == automation.WhenChanged:
		text = tr("Test run: the command runs")
	case t.Met:
		text = tr("Test run: met right now")
	}
	if t.Value != "" {
		text += " · " + t.Value
	}
	return text
}

func (a *automationsState) triggers(cx *core.Scope, v automation.View, draft bool, state *automationDetailState) core.Node {
	var children []core.Node
	if len(v.Triggers) > 0 || v.Policy.Gate != nil {
		children = append(children, autoText(tr("Triggers")))
	}
	for i, t := range v.Triggers {
		text := v.TriggerTexts[t.ID]
		if text == "" {
			text = automation.DescribeTrigger(t, nil)
		}
		row := []core.Node{nswidgets.Icon(autoTriggerIcon(t), 14, nswidgets.ColorsOf(cx).Muted), autoText(text)}
		if !draft {
			row = append(row, ui.Switch{TestID: "automation-trigger-" + t.ID, Label: text, Checked: func() bool { return !t.Disabled }, OnChange: func(on bool) {
				next := v.Automation
				next.Triggers = append([]automation.Trigger(nil), v.Triggers...)
				next.Triggers[i].Disabled = !on
				a.save(next)
			}})
		}
		children = append(children, autoRow(row...))
		if draft {
			if trial, ok := v.Trials[t.ID]; ok {
				children = append(children, autoMuted(cx, autoTrial(trial)))
			}
		} else {
			st, ok := v.TriggerStates[t.ID]
			status := autoTriggerState(t, st, ok)
			if t.Kind == automation.TriggerIngress && !t.Disabled {
				status = tr("Uses the shared Miya webhook")
			}
			children = append(children, autoMuted(cx, status))
			if t.Kind == automation.TriggerIngress && !t.Disabled {
				if v.WakeCommand != "" {
					children = append(children, autoCode(v.WakeCommand+" [-data text|@file|-]"))
				}
				if url := v.WakeURLs[t.ID]; url != "" {
					children = append(children, autoCode(fmt.Sprintf("curl -X POST -H 'Authorization: Bearer %s' --data @payload.json %s", t.Token, url)))
				}
				data := state.wakeData[t.ID]
				if data == nil {
					data = core.NewSignal(cx, "")
					state.wakeData[t.ID] = data
				}
				children = append(children, autoRow(ui.Input{TestID: "automation-wake-data-" + t.ID, Label: tr("What to do"), Value: data.Get, OnChange: data.Set, Placeholder: tr("What to do")},
					autoButton("automation-wake-"+t.ID, "Wake", func() { a.mutate("automationWake", automation.Request{ID: v.ID, Trigger: t.ID, Data: data.Get()}, nil) })))
			}
		}
	}
	if g := v.Policy.Gate; g != nil {
		children = append(children, autoText(tr("Runs only ")+automation.DescribeTrigger(automation.Trigger{Kind: automation.TriggerCondition, Condition: g}, nil)))
		if trial, ok := v.Trials[automation.GateTrial]; draft && ok {
			children = append(children, autoMuted(cx, autoTrial(trial)))
		}
	}
	if len(v.Triggers) > 0 || v.Policy.Gate != nil {
		children = append(children, autoMuted(cx, automation.PolicyText(v.Policy)))
	}
	if draft && (v.HasTrigger(automation.TriggerIngress) || v.HasTrigger(automation.TriggerFile)) {
		children = append(children, autoMuted(cx, tr("Can be woken from outside Miya; what comes with a wake reaches the agent as data. Consider running it in a separate worktree.")))
	}
	if !draft && len(v.Wakes) > 0 {
		children = append(children, autoText(tr("Recent wakes")))
		for i := len(v.Wakes) - 1; i >= max(0, len(v.Wakes)-10); i-- {
			w := v.Wakes[i]
			children = append(children, autoMuted(cx, automation.FormatTime(w.At)+" · "+tr(automation.SourceLabel(w.Source))+" · "+w.Reason+" · "+tr(automation.OutcomeLabel(w.Outcome))+" · "+w.Detail))
		}
	}
	if len(children) == 0 {
		return core.Group(nil)
	}
	return autoCard(cx, children...)
}

func (a *automationsState) runRow(cx *core.Scope, v automation.View, r automation.Run, latest bool, state *automationDetailState) core.Node {
	title := textutil.FirstNonEmpty(r.Summary, tr(automation.ResultLabel(r.Result)))
	if r.Result == automation.Running {
		title = fmt.Sprintf(tr("Running · Turn %d/%d"), max(1, r.Round), max(1, r.Rounds))
	}
	children := []core.Node{autoRow(ui.Badge{Text: tr(automation.ResultLabel(r.Result))}, autoText(title)), autoMuted(cx, automation.FormatTime(r.Started)+fmt.Sprintf(tr(" · %s · Turn %d/%d"), tr(automation.ResultLabel(r.Result)), max(1, r.Round), max(1, r.Rounds)))}
	if !r.Finished.IsZero() {
		children = append(children, autoMuted(cx, fmt.Sprintf(tr(" · %d minutes"), max(1, int(r.Finished.Sub(r.Started).Minutes()+0.5)))))
	}
	if r.CatchUp {
		children = append(children, autoMuted(cx, tr(" · Catch-up run")))
	}
	if r.Thread != 0 {
		children = append(children, autoButton(fmt.Sprintf("automation-conversation-%d", r.Thread), "Open conversation", func() { a.openThread(automation.Workspace(v.Project, r), r.Thread) }))
	}
	if label := automation.WorktreeLabel(r); label != "" {
		children = append(children, autoMuted(cx, label))
		if automation.WorktreePending(r) {
			act := func(action string) {
				a.mutate("automationWorktree", automation.Request{ID: v.ID, Started: r.Started, Action: action}, nil)
			}
			children = append(children, autoMuted(cx, fmt.Sprintf(tr(" · %d commits, %d uncommitted files"), r.Changes.Commits, r.Changes.Files)), autoRow(autoButton("automation-discard-"+r.Started.String(), "Discard", func() { act(automation.WorktreeDiscard) }), autoButton("automation-merge-"+r.Started.String(), "Merge", func() { act(automation.WorktreeMerge) })))
		}
		if r.WorktreeError != "" {
			children = append(children, autoText(r.WorktreeError))
		}
	}
	if rep := r.Report; rep != nil {
		key := r.Started.String()
		open := state.reports[key]
		if open == nil {
			open = core.NewSignal(cx, latest)
			state.reports[key] = open
		}
		label := tr("Structured result")
		if rep.Auto {
			label = tr("Automatic summary · Agent did not report a result")
		}
		children = append(children, autoButton("automation-report-"+r.Started.String(), label, func() { open.Update(func(b bool) bool { return !b }) }), core.Show{When: open.Get, Then: func(cx *core.Scope) core.Node {
			body := []core.Node{autoText(rep.Summary)}
			if rep.NeedsUser {
				body = append(body, ui.Badge{Text: tr("Needs attention"), Variant: ui.BadgeWarn})
			}
			for _, m := range rep.Metrics {
				body = append(body, autoText(m.Name+": "+string(m.Value)+m.Unit))
			}
			for _, f := range rep.Findings {
				body = append(body, autoText("· "+f))
			}
			if len(rep.Artifacts) > 0 {
				body = append(body, autoText(tr("Artifacts: ")+strings.Join(rep.Artifacts, ", ")))
			}
			return autoColumn(body...)
		}, Else: func(cx *core.Scope) core.Node { return autoMuted(cx, textutil.FirstLine(rep.Summary)) }})
	}
	return autoCard(cx, children...)
}

func (a *automationsState) openThread(project string, id int) {
	if project != "" && project != a.host.State().Cwd {
		if err := a.host.ChangeWorkspace(project); err != nil {
			a.err.Set(err.Error())
			return
		}
	}
	if t := a.host.ThreadWithID(id); t.ID != 0 {
		a.host.SelectThread(t)
	} else if set := a.host.State().SetActive; set != nil {
		set(id)
	}
	a.route.Set(automationRoute{"conversation", a.route.Get().id})
}
func (a *automationsState) conversation(cx *core.Scope) core.Node {
	id := a.route.Get().id
	st := a.host.State()
	return core.View{Style: ui.FillRest, Children: []core.Node{
		autoButton("automation-conversation-back", "Automations", func() {
			page := ""
			if id != "" {
				page = "detail"
			}
			a.route.Set(automationRoute{page, id})
		}),
		nschat.View{TestID: "automation-conversation", Thread: Live(a.host.Current), OnRetry: func(r appmodel.RetryRequest) {
			if err := a.host.RetryResponse(r); err != nil {
				a.err.Set(err.Error())
			}
		}, OnReload: func() { a.host.SelectThread(a.host.Current()) },
			Draft: Live(func() string {
				if st.Draft != nil {
					return st.Draft()
				}
				return ""
			}), OnDraftChange: a.host.RememberDraft, OnSend: func() { a.host.SendDraft(a.host.Current().Busy) }, OnStop: a.host.Stop},
	}}
}

func autoCheck(a automation.Automation) string {
	if a.Check == "" {
		return tr("No check; finish after one turn")
	}
	return a.Check + " · " + fmt.Sprintf(tr("Up to %d turns"), a.Rounds)
}
