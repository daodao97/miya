package nsui

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/composer"
	"miya/internal/chat/model"
	"miya/internal/nschat"
	"miya/internal/nswidgets"
)

// desktopComposer owns only window UI state. Controller state stays behind Live.
type desktopComposer struct {
	cx                 *core.Scope
	host               Host
	notice             *core.Signal[string]
	preparing          *core.Signal[int]
	menu               *core.Signal[string]
	complete           *composerCompletion
	active             func() bool
	diffOpen           *core.Signal[bool]
	collaborationPanes map[string]*CollaborationPanel
}

func newDesktopComposer(cx *core.Scope, c Host) *desktopComposer {
	d := &desktopComposer{cx: cx, host: c, notice: core.NewSignal(cx, ""), preparing: core.NewSignal(cx, 0), menu: core.NewSignal(cx, ""), diffOpen: core.NewSignal(cx, false)}
	d.collaborationPanes = map[string]*CollaborationPanel{}
	d.complete = newComposerCompletion(cx, d)
	cx.OnKey("mod+shift+o", func() {
		t := c.Current()
		if (d.active == nil || d.active()) && !t.ReadOnly && !t.Loading {
			d.pickFiles()
		}
	})
	return d
}
func (d *desktopComposer) draft() string {
	if f := d.host.State().Draft; f != nil {
		return f()
	}
	return ""
}
func (d *desktopComposer) attachments() []string {
	if f := d.host.State().Attachments; f != nil {
		return f()
	}
	return nil
}
func (d *desktopComposer) disabled() bool {
	t := d.host.Current()
	return t.ReadOnly || t.TaskPending || !d.host.BackendReady() || !t.Busy && t.Loading || d.preparing.Get() > 0
}
func (d *desktopComposer) wire(v *nschat.View) {
	c, cx := d.host, d.cx
	v.Draft = Live(d.draft)
	v.OnDraftChange = d.complete.edit
	v.OnSend = func() { d.send(false) }
	v.OnSteer = func() {
		if !d.complete.visible() {
			d.send(true)
		}
	}
	v.CaptureKeys = Live(d.complete.captureKeys)
	v.OnComposerKey = d.complete.onKey
	v.FileReferences = d.complete.files.Get
	v.OnDraftEdit = d.complete.editAt
	v.OnComposerMove = d.complete.close
	v.ComposerOwner = Live(func() string {
		return fmt.Sprintf("%s:%s:%d", c.State().ReferenceSource, c.State().Cwd, c.Current().ID)
	})
	v.Commands = Live(func() []model.ComposerChoice {
		return c.CurrentAgentControls().Commands(c.State().Cwd, true).Filter("")
	})
	v.OnInvalidReference = func() { d.notice.Set(tr("No valid session link in the clipboard")) }
	v.SendDisabled = Live(d.disabled)
	v.EditorDisabled = Live(func() bool { t := c.Current(); return t.ReadOnly || t.Loading })
	v.ComposerVisible = Live(func() bool { return !c.Current().ReadOnly })
	v.Above = settingsColumn(d.suggestions(), d.problem(), queueView(cx, c), taskStatus(cx, c, d.notice), collaborationStatus(cx, c, d.notice, d.collaborationPanes), d.readOnly(), d.diffDialog(), d.complete.view(), d.commandHint(), nswidgets.MutedText(cx, d.notice.Get))
	v.Accessory = settingsActions(ui.IconButton{Icon: nswidgets.IconOf("plus"), Label: tr("Add context"), TestID: "attach-files", OnPress: d.pickFiles, Disabled: Live(func() bool { return c.Current().Loading })}, d.agentPicker())
	v.Trailing = settingsActions(d.picker("permissions", tr("Permissions"), Live(func() string { return permissionTitle(c.CurrentPermissionPolicy()) }), func() []model.ComposerChoice {
		return []model.ComposerChoice{
			{Name: "on-request", Detail: tr("Ask when needed") + " · " + tr("Always ask before editing external files or accessing the internet")},
			{Name: "on-failure", Detail: tr("Automatic approval") + " · " + tr("Request approval only for detected risky actions")},
			{Name: "never", Detail: tr("Full access") + " · " + tr("Unrestricted access to the internet and any file on your computer")},
		}
	}, c.SetCurrentPermissionPolicy),
		core.Show{When: Live(func() bool { return c.CurrentAgentControls().Caps().Models }), Then: func(cx *core.Scope) core.Node {
			return d.picker("model", tr("Model"), Live(func() string { b := c.CurrentAgentControls(); return b.ModelLabel(b.Model()) }), func() []model.ComposerChoice {
				b := c.CurrentAgentControls()
				var out []model.ComposerChoice
				for _, m := range b.ModelChoices() {
					out = append(out, model.ComposerChoice{Name: m, Detail: b.ModelLabel(m)})
				}
				return out
			}, func(v string) { c.CurrentAgentControls().SetModel(v) })
		}},
		core.Show{When: Live(func() bool { return c.CurrentAgentControls().Caps().Effort }), Then: func(cx *core.Scope) core.Node {
			return d.picker("effort", tr("Thinking"), Live(func() string { return firstSettingsText(c.CurrentAgentControls().Effort(), tr("Default effort")) }), func() []model.ComposerChoice {
				var out []model.ComposerChoice
				for _, e := range c.CurrentAgentControls().EffortChoices() {
					out = append(out, model.ComposerChoice{Name: e, Detail: tr(firstSettingsText(e, "Default"))})
				}
				return out
			}, func(v string) { c.CurrentAgentControls().SetEffort(v) })
		}},
		core.Show{When: Live(func() bool {
			return c.Current().Busy && (strings.TrimSpace(d.draft()) != "" || len(d.attachments()) > 0)
		}), Then: func(cx *core.Scope) core.Node {
			return ui.Button{Text: tr("Steer now"), TestID: "steer-draft", Variant: ui.ButtonGhost, Size: ui.SizeSmall, Disabled: Live(d.disabled), OnPress: func() { d.send(true) }}
		}})
	v.Attachments = d.attachmentChips()
	v.HasAttachments = Live(func() bool { return len(d.attachments()) > 0 })
	v.OnPaste = d.paste
	v.OnDropFiles = func(uris []string) {
		if !c.Current().ReadOnly && !c.Current().Loading {
			d.addPaths(localPaths(uris))
		}
	}
	v.OnHandoff = d.handoff
	v.ReferenceTitle = d.referenceTitle
	v.OnOpenReference = func(ref model.ThreadRef) { openCollaborationThread(c, ref.URL(), d.notice) }
	v.UserContent = d.userContent
	v.ImageSource = func(cx *core.Scope, src string) func() string { return remoteImageSource(cx, c, src) }
}
func permissionTitle(p string) string {
	switch p {
	case "never":
		return tr("Full access")
	case "on-failure":
		return tr("Automatic approval")
	}
	return tr("Ask when needed")
}

func (d *desktopComposer) suggestions() core.Node {
	return core.Show{When: Live(func() bool { return d.host.Current().ID == 0 }), Then: func(cx *core.Scope) core.Node {
		rows := []core.Node{nswidgets.MutedText(cx, func() string { return tr("A few ideas to get started") })}
		var buttons []core.Node
		for _, s := range []struct{ id, icon, title, prompt string }{
			{"snake", "device-gamepad-2", "Create a classic snake game", "Create a classic snake game in this project."},
			{"bug", "bug", "Find and fix a bug in my code", "Find and fix a bug in my code. Explain the root cause and verify the fix."},
			{"pdf", "file-text", "Summarize this app in a PDF", "Summarize this app in a one-page PDF."},
		} {
			buttons = append(buttons, ui.Button{TestID: "suggestion-" + s.id, Text: tr(s.title), Icon: nswidgets.IconOf(s.icon), Variant: ui.ButtonSecondary, OnPress: func() { d.host.RememberDraft(tr(s.prompt)) }, Flex: 1})
		}
		rows = append(rows, settingsActions(buttons...))
		return settingsColumn(rows...)
	}}
}
func (d *desktopComposer) problem() core.Node {
	return core.Show{When: Live(func() bool { s, _ := d.host.CurrentAgentControls().Problem(); return s != "" }), Then: func(cx *core.Scope) core.Node {
		return settingsActions(core.Text{ContentFunc: Live(func() string { s, _ := d.host.CurrentAgentControls().Problem(); return s }), Style: core.Style{FlexGrow: 1}}, core.Show{When: Live(func() bool { _, r := d.host.CurrentAgentControls().Problem(); return r }), Then: func(cx *core.Scope) core.Node {
			return ui.Button{Text: tr("Reconnect"), TestID: "reconnect-agent", Variant: ui.ButtonGhost, OnPress: func() { d.host.CurrentAgentControls().Reconnect() }}
		}})
	}}
}
func (d *desktopComposer) readOnly() core.Node {
	return core.Show{When: Live(func() bool { return d.host.Current().ReadOnly }), Then: func(cx *core.Scope) core.Node {
		return core.View{TestID: "readonly-composer", StyleFunc: nswidgets.CardStyle(cx), Children: []core.Node{
			core.Text{Content: tr("Open in another app")}, nswidgets.MutedText(cx, func() string { return tr("Close the conversation there before continuing here.") }), ui.Button{Text: tr("Retry"), Disabled: Live(func() bool { return d.host.Current().Loading }), OnPress: func() { d.host.Resume(d.host.Current(), nil) }},
		}}
	}}
}
func (d *desktopComposer) picker(id, label string, value func() string, choices func() []model.ComposerChoice, pick func(string)) core.Node {
	return core.Func(func(cx *core.Scope) core.Node {
		caption := core.NewMemo(cx, value)
		return core.Dynamic(func(cx *core.Scope) core.Node {
			title := caption.Get()
			return ui.Popover{TestID: "picker-" + id, Label: label, Open: func() bool { return d.menu.Get() == id }, OnOpenChange: func(v bool, _ base.Reason) {
				if v {
					d.menu.Set(id)
				} else {
					d.menu.Set("")
				}
			}, Placement: core.PlaceTop,
				Trigger: ui.Button{Text: title, Variant: ui.ButtonGhost, Size: ui.SizeSmall},
				Children: []core.Node{core.Text{Content: label}, core.Each(Live(choices), func(c model.ComposerChoice) string { return c.Name }, func(cx *core.Scope, get func() model.ComposerChoice, _ func() int) core.Node {
					name := core.Untracked(cx, func() string { return get().Name })
					return composerButton("pick-"+id+"-"+name, func() string { return firstSettingsText(get().Detail, get().Name) }, func() { pick(name); d.menu.Set("") })
				})}}
		})
	})
}
func (d *desktopComposer) agentPicker() core.Node {
	// The disabled wrapper also blocks popover triggers, whose own Disabled is ignored.
	return core.View{Disabled: Live(func() bool { return d.host.Current().ID != 0 }), Children: []core.Node{d.picker("agent", tr("Agents"), Live(func() string {
		id := d.host.CurrentAgent()
		return model.AgentName(id) + " " + d.host.AgentSpeedBadge(id)
	}), func() []model.ComposerChoice {
		var out []model.ComposerChoice
		for _, id := range d.host.EnabledAgents() {
			out = append(out, model.ComposerChoice{Name: id, Detail: model.AgentName(id) + " " + d.host.AgentSpeedBadge(id)})
		}
		return out
	}, func(v string) {
		if d.host.Current().ID == 0 && d.host.State().SetAgent != nil {
			d.host.State().SetAgent(v)
		}
	})}}
}
func (d *desktopComposer) handoff() {
	c := d.host
	t := c.Current()
	ref := c.ThreadRef(t)
	if !ref.Valid() {
		d.notice.Set(tr("Available once the session is created"))
		return
	}
	c.NewThread()
	c.RememberDraft(model.ReferenceText(ref, appmodel.ThreadRefTitle(t)) + " ")
	c.PrefetchReference(ref)
	d.notice.Set(tr("Referenced conversation \"") + appmodel.ThreadRefTitle(t) + tr("\": choose an agent, describe the task, and send"))
}
func (d *desktopComposer) send(steer bool) {
	c := d.host
	if d.disabled() {
		return
	}
	d.complete.close()
	d.notice.Set("")
	if command, ok := composer.SlashCommand(d.draft()); ok && !c.CurrentAgentControls().Commands(c.State().Cwd, false).TakesCommand(command) {
		clear := func() { c.RememberDraft("") }
		switch command {
		case "new":
			c.NewThread()
		case "model":
			clear()
			d.menu.Set("model")
		case "permissions":
			clear()
			d.menu.Set("permissions")
		case "mention":
			d.complete.edit("@")
		case "diff":
			clear()
			d.diffOpen.Set(true)
		case "status":
			clear()
			b := c.CurrentAgentControls()
			d.notice.Set(fmt.Sprintf(tr("Model: %s · Thinking: %s · Permissions: %s · Tokens: %d"), b.Model(), b.Effort(), permissionTitle(c.CurrentPermissionPolicy()), c.Current().Tokens))
		default:
			if name, _, _ := strings.Cut(command, " "); name == "goal" || name == "fast" || name == "compact" || name == "review" {
				d.notice.Set(c.RunThreadCommand(command))
			} else {
				d.notice.Set(tr("Unknown command. Type / to choose a supported command."))
			}
		}
		return
	}
	c.SendDraft(steer)
	d.notice.Set(c.State().LastSubmitError)
}

func composerButton(id string, text func() string, press func()) core.Node {
	return core.View{TestID: id, Role: core.RoleButton, LabelFunc: text, OnPress: func(core.PressEvent) { press() }, Style: core.Style{Padding: core.All(6), CornerRadius: 6}, Children: []core.Node{core.Text{ContentFunc: text, Style: core.Style{FontSize: 12}}}}
}

func (d *desktopComposer) referenceTitle(ref model.ThreadRef) string {
	c := d.host
	if ref.Source == c.State().ReferenceSource {
		if title, ok := c.SavedReferenceTitle(ref); ok {
			return title
		}
		for _, p := range c.ThreadRefProjects() {
			for _, t := range p.Threads {
				if t.ServerID == ref.SessionID && model.ThreadAgent(t) == ref.Backend {
					return appmodel.ThreadRefTitle(t)
				}
			}
		}
	}
	return tr("Session unavailable")
}

func (d *desktopComposer) commandHint() core.Node {
	return nswidgets.MutedText(d.cx, Live(func() string {
		text := strings.TrimPrefix(d.draft(), "/")
		if text == d.draft() {
			return ""
		}
		name, _, hasArgs := strings.Cut(text, " ")
		if !hasArgs {
			return ""
		}
		for _, choice := range d.host.CurrentAgentControls().Commands(d.host.State().Cwd, false).Choices {
			if choice.Name == name {
				return tr(choice.ArgumentHint)
			}
		}
		return ""
	}))
}
