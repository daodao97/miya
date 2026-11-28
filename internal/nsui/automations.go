package nsui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/textutil"
)

type automationRoute struct{ page, id string }

type automationShortcut struct {
	detail string
	duty   *bot.Bot
}

// All page state belongs to the engine. Service calls capture their inputs
// here, do the blocking work elsewhere, and post their replies back here.
type automationsState struct {
	cx                       *core.Scope
	host                     Host
	route                    *core.Signal[automationRoute]
	list                     *core.Signal[[]automation.View]
	webhook                  *core.Signal[*automation.WebhookView]
	loaded, loading, pending *core.Signal[bool]
	err                      *core.Signal[string]
	listErr                  *core.Signal[string]
	generation               *core.Signal[int]
	form                     *automationFormState
}

func automationsPage(cx *core.Scope, c Host, start ...automationShortcut) core.Node {
	a := &automationsState{cx: cx, host: c, route: core.NewSignal(cx, automationRoute{}),
		list: core.NewSignalFunc[[]automation.View](cx, nil, func(a, b []automation.View) bool { return reflect.DeepEqual(a, b) }), webhook: core.NewSignal[*automation.WebhookView](cx, nil),
		loaded: core.NewSignal(cx, false), loading: core.NewSignal(cx, false), pending: core.NewSignal(cx, false),
		err: core.NewSignal(cx, ""), listErr: core.NewSignal(cx, ""), generation: core.NewSignal(cx, 0)}
	a.form = newAutomationForm(cx)
	if len(start) != 0 {
		entry := start[0]
		if b := entry.duty; b != nil {
			a.openForm(nil)
			a.form.project.Set(b.Project)
			a.form.agent.Set(b.Agent)
			a.form.botID.Set(b.ID)
		} else if entry.detail != "" {
			a.route.Set(automationRoute{"detail", entry.detail})
		}
	}
	var refresh func()
	refresh = func() { a.load(); cx.AfterFunc(5*time.Second, refresh) }
	cx.Post(refresh)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		route := a.route.Get()
		if route.page == "conversation" {
			return a.conversation(cx)
		}
		var body core.Node
		switch route.page {
		case "form":
			body = a.formView(cx)
		case "detail":
			body = a.detail(cx, route.id)
		default:
			body = a.listView(cx)
		}
		return core.Scroll{TestID: "automations", Style: ui.FillRest, Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(920), AlignSelf: core.AlignCenter, Padding: core.All(24), Gap: 18}, Children: []core.Node{
				core.Show{When: func() bool { return a.errorText() != "" }, Then: func(cx *core.Scope) core.Node {
					return core.Text{TestID: "automations-error", ContentFunc: a.errorText, Selectable: core.SelectableOn,
						StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Danger(), FontSize: 12} }}
				}}, body,
			}},
		}}
	})
}

// call also accepts deferred local replies. A desktop mirror routes the
// same protocol through its service client, as the MyGo page does.
func (a *automationsState) call(op string, req automation.Request, done func(json.RawMessage, error)) {
	callAutomationService(a.cx, a.host, op, req, done)
}

func callAutomationService(cx *core.Scope, host Host, op string, req automation.Request, done func(json.RawMessage, error)) {
	remote := host.State().Remote
	go func() {
		var raw json.RawMessage
		var err error
		if remote != nil {
			err = remote.Call(op, req, &raw)
		} else {
			var out any
			out, err = host.AutomationOp(op, req)
			if reply, ok := out.(async.Reply); ok && err == nil {
				out, err = reply()
			}
			if err == nil && out != nil {
				raw, err = json.Marshal(out)
			}
		}
		cx.Post(func() { done(raw, err) })
	}()
}

func (a *automationsState) errorText() string {
	if text := a.err.Get(); text != "" {
		return text
	}
	return a.listErr.Get()
}

// A manual refresh supersedes older reads, including a slow webhook RPC.
func (a *automationsState) refresh() {
	if a.pending.Get() {
		return
	}
	a.err.Set("")
	a.loading.Set(false)
	a.load()
}

func (a *automationsState) load() {
	if a.loading.Get() || a.pending.Get() {
		return
	}
	a.loading.Set(true)
	a.generation.Update(func(n int) int { return n + 1 })
	gen := a.generation.Get()
	remaining := 2
	finish := func() {
		remaining--
		if remaining == 0 {
			a.loading.Set(false)
		}
	}
	a.call("automationList", automation.Request{}, func(raw json.RawMessage, err error) {
		if gen != a.generation.Get() {
			return
		}
		defer finish()
		var list []automation.View
		if err == nil {
			err = json.Unmarshal(raw, &list)
		}
		if err != nil {
			a.listErr.Set(err.Error())
			return
		}
		a.listErr.Set("")
		a.list.Set(list)
		a.loaded.Set(true)
	})
	a.call("automationWebhook", automation.Request{}, func(raw json.RawMessage, err error) {
		if gen != a.generation.Get() {
			return
		}
		defer finish()
		var v automation.WebhookView
		if err == nil {
			err = json.Unmarshal(raw, &v)
		}
		if err != nil {
			message := err.Error()
			if strings.Contains(strings.ToLower(message), "unknown operation") || strings.Contains(message, "Unknown automation operation") {
				message = tr("Waiting for running conversations to finish before the webhook update takes effect.")
			}
			v = automation.WebhookView{Enabled: true, Error: message}
		}
		a.webhook.Set(&v)
	})
}

func (a *automationsState) mutate(op string, req automation.Request, done func(json.RawMessage)) {
	if a.pending.Get() {
		return
	}
	a.pending.Set(true)
	a.err.Set("")
	a.generation.Update(func(n int) int { return n + 1 })
	a.loading.Set(false)
	a.call(op, req, func(raw json.RawMessage, err error) {
		a.pending.Set(false)
		if err != nil {
			a.err.Set(err.Error())
		} else if done != nil {
			done(raw)
		}
		a.load()
	})
}

func (a *automationsState) toggle(id string, enabled bool) {
	a.mutate("automationToggle", automation.Request{ID: id, Enabled: enabled}, nil)
}
func (a *automationsState) run(id string) {
	a.mutate("automationRunNow", automation.Request{ID: id}, nil)
}
func (a *automationsState) save(v automation.Automation) {
	copy := autoClone(v)
	a.mutate("automationSave", automation.Request{Automation: &copy}, nil)
}
func (a *automationsState) back() { a.route.Set(automationRoute{}) }

func autoColumn(children ...core.Node) core.View {
	return core.View{Style: core.Style{Gap: 10, MinWidth: core.Pt(0)}, Children: children}
}
func autoRow(children ...core.Node) core.View {
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 12, MinWidth: core.Pt(0)}, Children: children}
}
func autoText(text string) core.Text {
	return core.Text{Content: text, Selectable: core.SelectableOn, Style: core.Style{FontSize: 13, FlexShrink: 1, MinWidth: core.Pt(0)}}
}
func autoCode(text string) core.Text {
	node := autoText(text)
	node.Style.FontFamily = nswidgets.MonoFont
	node.Style.FontSize = 12
	return node
}

func autoMuted(cx *core.Scope, text string) core.Node {
	return nswidgets.MutedText(cx, func() string { return text })
}
func autoHeading(text string) core.Node {
	return core.Text{Content: text, Style: core.Style{FontSize: 24, FontWeight: ui.Semibold}}
}
func autoCard(cx *core.Scope, children ...core.Node) core.Node {
	return core.View{StyleFunc: nswidgets.CardStyle(cx), Children: children}
}
func autoButton(id, text string, press func()) ui.Button {
	return ui.Button{TestID: id, Text: tr(text), Variant: ui.ButtonSecondary, OnPress: press}
}
func (a *automationsState) switchRow(id, label string, checked func() bool, change func(bool)) core.Node {
	return autoRow(ui.Switch{TestID: id, Label: tr(label), Checked: checked, OnChange: func(on bool) {
		if !a.pending.Get() {
			change(on)
		}
	}}, autoText(tr(label)))
}
func autoNext(v automation.View) string {
	if !v.Enabled {
		return tr("Paused")
	}
	if v.Schedule == "" {
		return tr("Waiting for a trigger")
	}
	return automation.FormatTime(v.Next)
}
func autoLast(v automation.View) (automation.Run, bool) {
	for i := len(v.Runs) - 1; i >= 0; i-- {
		if v.Runs[i].Result != automation.Skipped {
			return v.Runs[i], true
		}
	}
	return automation.Run{}, false
}
func autoRunLabel(v automation.View) string {
	if v.Running {
		r, _ := autoLast(v)
		return fmt.Sprintf(tr("Running · Turn %d/%d"), max(r.Round, 1), max(r.Rounds, 1))
	}
	if r, ok := autoLast(v); ok {
		label := tr(automation.ResultLabel(r.Result))
		if r.Rounds > 1 {
			if r.Result == automation.Passed {
				label += fmt.Sprintf(tr(" · Turn %d"), r.Round)
			} else if r.Result == automation.Failed {
				label += fmt.Sprintf(tr(" · %d/%d turns"), r.Round, r.Rounds)
			}
		}
		return label
	}
	return tr("Not run yet")
}

func autoRunVariant(v automation.View) ui.BadgeVariant {
	if v.Running {
		return ui.BadgeAccent
	}
	if r, ok := autoLast(v); ok {
		switch r.Result {
		case automation.Passed:
			return ui.BadgeSuccess
		case automation.Failed:
			return ui.BadgeDanger
		case automation.Interrupted:
			return ui.BadgeWarn
		}
	}
	return ui.BadgeNeutral
}

func (a *automationsState) listView(cx *core.Scope) core.Node {
	// The webhook folds into one row; its settings and messages open in a
	// dialog, which outlives the row's rebuilds as the webhook changes.
	webhookOpen := core.NewSignal(cx, false)
	return autoColumn(
		autoRow(autoHeading(tr("Automations")), ui.Button{TestID: "automation-new", Text: tr("New"), Icon: nswidgets.IconOf("plus"), OnPress: func() { a.openForm(nil) }},
			autoButton("automations-refresh", "Refresh", a.refresh)),
		autoMuted(cx, tr("Start a new conversation in the project on schedule, then send the result to your phone.")),
		a.attention(cx), a.briefing(cx),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if v := a.webhook.Get(); v != nil {
				return a.inboxRow(cx, *v, func() { webhookOpen.Set(true) })
			}
			return core.Group(nil)
		}),
		ui.Dialog{Title: tr("Miya webhook"), TestID: "automation-webhook-dialog", Open: webhookOpen.Get, OnOpenChange: func(o bool, _ base.Reason) { webhookOpen.Set(o) },
			Style: core.Style{Width: core.Pt(560)},
			Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
				if v := a.webhook.Get(); v != nil {
					return a.inbox(cx, *v)
				}
				return core.Group(nil)
			})}},
		core.Show{When: func() bool { return !a.loaded.Get() && a.listErr.Get() == "" }, Then: func(cx *core.Scope) core.Node { return autoMuted(cx, tr("Loading…")) }},
		core.Show{When: func() bool { return a.loaded.Get() && len(a.list.Get()) == 0 }, Then: func(cx *core.Scope) core.Node {
			return autoCard(cx, autoText(tr("No automations yet")), autoMuted(cx, tr("Click New to describe a task, or ask an agent to create one in any conversation.")))
		}},
		core.Each(a.list.Get, func(v automation.View) string { return v.ID }, func(cx *core.Scope, get func() automation.View, _ func() int) core.Node {
			id := core.Untracked(cx, get).ID
			return autoCard(cx,
				autoRow(nswidgets.Icon("clock", 22, nswidgets.ColorsOf(cx).Muted),
					core.View{TestID: "automation-open-" + id, Role: core.RoleButton, LabelFunc: func() string { return get().Name },
						OnPress: func(core.PressEvent) { a.route.Set(automationRoute{"detail", id}) }, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(120), Padding: core.All(4)},
						Children: []core.Node{core.Text{ContentFunc: func() string { return get().Name }, Style: core.Style{FontSize: 15, FontWeight: ui.Semibold}},
							nswidgets.MutedText(cx, func() string {
								v := get()
								check := tr("Single turn")
								if v.Check != "" {
									check = tr("Completion criteria ") + v.Check
								}
								return filepath.Base(v.Project) + " · " + model.AgentName(v.Agent) + " · " + check
							})}},
					ui.Switch{TestID: "automation-toggle-" + id, Label: tr("Enabled"), Checked: func() bool { return get().Enabled }, OnChange: func(on bool) {
						if !a.pending.Get() {
							a.toggle(id, on)
						}
					}},
					ui.Button{TestID: "automation-run-" + id, Text: tr("Run now"), Variant: ui.ButtonGhost, Disabled: func() bool { return a.pending.Get() || get().Running }, OnPress: func() { a.run(id) }},
				),
				autoRow(nswidgets.MutedText(cx, func() string { v := get(); return v.ScheduleText + " · " + autoNext(v) }), ui.Badge{TextFunc: func() string { return autoRunLabel(get()) }, VariantFunc: func() ui.BadgeVariant { return autoRunVariant(get()) }},
					core.Show{When: func() bool { r, ok := autoLast(get()); return ok && r.Thread != 0 }, Then: func(cx *core.Scope) core.Node {
						return autoButton("automation-last-"+id, "Open last conversation", func() { v := get(); r, _ := autoLast(v); a.openThread(automation.Workspace(v.Project, r), r.Thread) })
					}}),
			)
		}),
		autoMuted(cx, tr("Describe a recurring task in any conversation. It appears here after confirmation. Enabled automations keep running when the window is closed.")),
	)
}

// inboxRow is the webhook folded to its name and state; pressing it opens
// the dialog with the rest.
func (a *automationsState) inboxRow(cx *core.Scope, v automation.WebhookView, open func()) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.View{TestID: "automation-webhook-open", Role: core.RoleButton, Label: tr("Miya webhook"), OnPress: func(core.PressEvent) { open() },
		StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{autoText(tr("Miya webhook"))}},
				autoMuted(cx, v.Status()),
				nswidgets.Icon("chevron-right", 14, colors.Faint),
			}},
		}}
}

// inbox is the webhook's switch, address and recent messages.
func (a *automationsState) inbox(cx *core.Scope, v automation.WebhookView) core.Node {
	children := []core.Node{
		autoRow(autoText(tr("Enable webhook")), ui.Switch{TestID: "automation-webhook", Label: tr("Enable webhook"), Checked: func() bool { return v.Enabled }, OnChange: func(on bool) {
			action := "disable"
			if on {
				action = "enable"
			}
			a.mutate("automationWebhook", automation.Request{Action: action}, func(raw json.RawMessage) {
				var next automation.WebhookView
				if err := json.Unmarshal(raw, &next); err != nil {
					a.err.Set(err.Error())
				} else {
					a.webhook.Set(&next)
				}
			})
		}}),
		autoMuted(cx, tr("Messages are received by Miya and routed to matching automations.")), autoMuted(cx, v.Status()),
	}
	if v.URL != "" {
		children = append(children, autoCode(v.URL), autoButton("automation-webhook-copy", "Copy webhook URL", func() { cx.SetClipboard(v.URL) }), autoMuted(cx, tr("POST text or JSON to this URL while Miya is online.")))
	}
	if len(v.Messages) > 0 {
		children = append(children, autoText(tr("Recent messages")))
	}
	for i := len(v.Messages) - 1; i >= max(0, len(v.Messages)-5); i-- {
		m := v.Messages[i]
		children = append(children, autoColumn(autoMuted(cx, automation.FormatTime(m.At)), autoText(textutil.ClipRunes(m.Data, 200)), autoMuted(cx, automation.WebhookDeliveryText(m))))
	}
	return autoColumn(children...)
}
