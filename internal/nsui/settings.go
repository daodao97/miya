package nsui

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"miya/internal/agent"
	"miya/internal/nswidgets"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
	"miya/locales"
)

// settingsDialog is intentionally separate from the shell: menu commands and
// the sidebar share the same open signal. Closing disposes pending view work.
func settingsDialog(cx *core.Scope, c Host, open *core.Signal[bool]) core.Node {
	usage := usageFor(cx, c)
	service := newSettingsService(cx, c)
	// Apply saved device preferences before constructing the dialog, including
	// when it starts closed. The shell can use the same environment settings.
	cx.SetLocale(prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage")))
	setSettingsAppearance(cx, prefs.NormalizeAppearance(prefs.Load("appearance")))
	return ui.SettingsDialog{Open: open.Get, OnOpenChange: func(v bool, _ base.Reason) { open.Set(v) },
		Label: tr("Settings"), TestID: "settings-dialog", Content: func(_ func() bool) core.Node {
			return core.Show{When: open.Get, Then: func(cx *core.Scope) core.Node {
				return settingsContent(cx, c, usage, service, func() { open.Set(false) }, true)
			}}
		}}
}

func settingsContent(cx *core.Scope, c Host, usage *desktopUsage, service *settingsService, closeSettings func(), dialog bool) core.Node {
	category, query := usage.category, core.NewSignal(cx, "")
	c.EnsureAgentUpdates()
	drafts := map[string]*settingsDraft{}
	account := newSettingsAccount(cx, c, category)
	memory := newSettingsMemory(cx, c, category)
	memory.onSource = func() { closeSettings() }
	// Text in SettingsCategory/Item is static. Rebuild on language changes,
	// keeping navigation, drafts and asynchronous operations outside this region.
	return core.Dynamic(func(cx *core.Scope) core.Node {
		tag := prefs.NormalizeInterfaceLanguage(cx.Env().Locale())
		if tag == "" {
			tag = "en"
		}
		locales.SetCurrent(tag)
		return core.Func(func(cx *core.Scope) core.Node {
			categories := []ui.SettingsCategory{
				{ID: "general", Title: tr("General"), Icon: nswidgets.IconOf("settings"), Groups: generalSettings(cx, c, service, drafts)},
				{ID: "providers", Title: tr("Model providers"), Icon: nswidgets.IconOf("server"), Groups: []ui.SettingsGroup{{
					ID: "providers", Title: tr("Model providers"), Items: []ui.SettingsItem{{ID: "providers", Content: core.Func(func(cx *core.Scope) core.Node {
						return providerSettings(cx, c)
					})}},
				}}},
				{ID: "account", Title: tr("Account"), Icon: nswidgets.IconOf("user-circle"), Groups: account.groups()},
				{ID: "usage", Title: tr("Usage"), Icon: nswidgets.IconOf("pi"), Groups: usage.groups()},
				{ID: "phone", Title: tr("Phone"), Icon: nswidgets.IconOf("device-mobile"), Groups: append(phoneSettings(cx, c), phoneNotificationGroup(cx, c))},
				{ID: "memory", Title: tr("Memory"), Icon: nswidgets.IconOf("bulb"), Description: tr("Saved locally and shared by Codex and Claude. Current instructions and project files take priority. Forgetting removes Miya's content, but not agent history or backups."), Groups: memory.groups()},
				{ID: "webhook", Title: tr("Webhook"), Icon: nswidgets.IconOf("webhook"), Groups: []ui.SettingsGroup{webhookSettings(c)}},
				{ID: "remote-control", Title: tr("Remote control"), Icon: nswidgets.IconOf("device-desktop"), Groups: []ui.SettingsGroup{remoteControlSettings(c)}},
			}
			return desktopSettingsPage(cx, category, query, categories, closeSettings, dialog)
		})
	})
}

func openSettingsWindow(cx *core.Scope, c Host, usage *desktopUsage) bool {
	var window *core.Window
	window = cx.OpenWindow(core.WindowOptions{
		Title: tr("Settings"), Key: "miya-settings", Width: 780, Height: 700,
		Constraints: core.WindowConstraints{MinWidth: 560, MinHeight: 420},
	}, func(cx *core.Scope) core.Node {
		// The companion window resolves its own palette, including the colors
		// used by menus opened above its forms.
		cx.SetTheme(nswidgets.Theme())
		return settingsContent(cx, c, usage, newSettingsService(cx, c),
			func() {
				if window != nil {
					window.Close()
				}
			}, false)
	})
	return window != nil
}

func setSettingsAppearance(cx *core.Scope, v string) {
	a := core.AppearanceSystem
	if v == "light" {
		a = core.AppearanceLight
	}
	if v == "dark" {
		a = core.AppearanceDark
	}
	cx.SetAppearance(a)
}

func settingsColumn(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Gap: 10, MinWidth: core.Pt(0)}, Children: nodes}
}
func settingsActions(nodes ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8, AlignItems: core.AlignCenter}, Children: nodes}
}
func settingsText(cx *core.Scope, value func() string) core.Node {
	return core.Show{When: func() bool { return value() != "" }, Then: func(*core.Scope) core.Node {
		return core.Text{ContentFunc: value, Selectable: core.SelectableOn, Style: core.Style{FontSize: 12, LineHeight: 17}, StyleFunc: func() core.Style { return core.Style{Color: nswidgets.ColorsOf(cx).Muted()} }}
	}}
}
func settingsButton(id, title string, press func(), disabled func() bool) core.Node {
	return ui.Button{TestID: id, Text: title, OnPress: press, Disabled: disabled, Variant: ui.ButtonSecondary}
}
func settingsSwitch(id, title, description string, get func() bool, set func(bool), disabled func() bool) ui.SettingsItem {
	return ui.SettingsItem{ID: id, Title: tr(title), Description: tr(description), Control: settingsToggle(id, tr(title), Live(get), set, disabled)}
}
func settingsPreference(cx *core.Scope, key string) (func() string, func(string)) {
	changed := core.NewSignal(cx, 0)
	return func() string { changed.Get(); return prefs.Load(key) }, func(v string) {
		prefs.Save(key, v)
		changed.Update(func(n int) int { return n + 1 })
		uiruntime.Invalidate()
	}
}

func generalSettings(cx *core.Scope, c Host, service *settingsService, drafts map[string]*settingsDraft) []ui.SettingsGroup {
	interfaceLanguage, saveLanguage := settingsPreference(cx, "interfaceLanguage")
	appearance, saveAppearance := settingsPreference(cx, "appearance")
	general := ui.SettingsGroup{ID: "general", Title: tr("General"), Items: []ui.SettingsItem{
		{ID: "language", Title: tr("Interface language"), Description: tr("Changes apply immediately on this device."), Control: settingsSelect{TestID: "interface-language", Label: tr("Interface language"), Options: []ui.Option[string]{{Value: "", Label: tr("Follow system")}, {Value: "zh-Hans", Label: display.Self.Name(language.SimplifiedChinese)}, {Value: "en", Label: display.Self.Name(language.English)}}, Value: func() string { return prefs.NormalizeInterfaceLanguage(interfaceLanguage()) }, OnChange: func(v string) {
			saved := v
			if saved == "" {
				saved = "system"
			}
			saveLanguage(saved)
			cx.SetLocale(v)
		}}},
		{ID: "appearance", Title: tr("Appearance"), Control: settingsSelect{TestID: "appearance", Label: tr("Appearance"), Options: []ui.Option[string]{{Value: "", Label: tr("System")}, {Value: "light", Label: tr("Light")}, {Value: "dark", Label: tr("Dark")}}, Value: func() string { return prefs.NormalizeAppearance(appearance()) }, OnChange: func(v string) { saveAppearance(v); setSettingsAppearance(cx, v) }}},
	}}
	notifications := ui.SettingsGroup{ID: "notifications", Title: tr("Notifications")}
	for _, o := range []struct{ key, title, help string }{{"completion", "Task finished", "Notify when a conversation finishes its turn."}, {"waiting", "Waiting for approval", "Notify when an action needs your approval."}} {
		get, set := settingsPreference(cx, "notifications-"+o.key)
		notifications.Items = append(notifications.Items, settingsSwitch("notification-"+o.key, o.title, o.help, func() bool { return get() != "off" }, func(on bool) {
			v := "off"
			if on {
				v = "on"
			}
			set(v)
		}, nil))
	}
	notifications.Items = append(notifications.Items, ui.SettingsItem{ID: "phone-notifications", Description: tr("Phone notifications in the background need Miya's APNs key on the sending side and permission on the phone.")})
	permissions := ui.SettingsGroup{ID: "permissions", Title: tr("Default permissions for new conversations"), Items: []ui.SettingsItem{{ID: "permission-note", Description: tr("Change them for the current conversation from the composer.")}}}
	for _, p := range []struct{ value, title, help, icon string }{{"on-request", "Ask when needed", "Always ask before editing external files or accessing the internet", "hand-stop"}, {"on-failure", "Automatic approval", "Request approval only for detected risky actions", "shield-check"}, {"never", "Full access", "Unrestricted access to the internet and any file on your computer", "shield-exclamation"}} {
		permissions.Items = append(permissions.Items, ui.SettingsItem{ID: "permission-" + p.value, Title: tr(p.title), Description: tr(p.help), Leading: ui.Icon(nswidgets.IconOf(p.icon), 20, nswidgets.ColorsOf(cx).Muted), Control: ui.Radio{TestID: "permission-" + p.value, Label: tr(p.title), Checked: Live(func() bool { return c.SelectedPermissionPolicy() == p.value }), OnChange: func(bool) {
			if set := c.State().SetPermissionPolicy; set != nil {
				set(p.value)
			}
		}}})
	}
	groups := []ui.SettingsGroup{general, notifications, permissions, {ID: "agents", Title: tr("Agents"), Actions: []core.Node{settingsButton("recheck-agents", tr("Check again"), c.RecheckAgents, Live(c.AgentUpdateBusy))}, Items: []ui.SettingsItem{{ID: "agents", Title: tr("Agents"), Content: settingsColumn(settingsText(cx, func() string {
		return tr("Turn agents on or off for new conversations. Existing conversations keep their agent.")
	}), agentEnvironmentSettings(cx, c), agentSettings(cx, c, drafts))}}}}
	return append(groups, service.groups()...)
}

func agentSettings(cx *core.Scope, c Host, drafts map[string]*settingsDraft) core.Node {
	// Only membership changes replace controls. Status/selection bindings update
	// in place so controller invalidations never erase an input draft.
	key := core.NewMemo(cx, Live(func() string {
		var ids []string
		for _, a := range c.Agents() {
			ids = append(ids, a.ID)
		}
		for _, s := range c.AgentSettings() {
			ids = append(ids, s.ID)
		}
		return strings.Join(ids, "\x00")
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		key.Get()
		return core.Func(func(cx *core.Scope) core.Node {
			var rows []core.Node
			for _, a := range c.Agents() {
				id := a.ID
				get := func() agent.Availability {
					for _, a := range c.Agents() {
						if a.ID == id {
							return a
						}
					}
					return agent.Availability{}
				}
				info := settingsText(cx, Live(func() string {
					a := get()
					u := c.AgentUpdates()[id]
					parts := []string{a.Path}
					if !a.Installed() {
						parts = append(parts, firstSettingsText(a.Problem, tr("Not installed")))
					}
					parts = append(parts, u.Version)
					switch {
					case u.Checking:
						parts = append(parts, tr("Checking for updates…"))
					case u.Error != "":
						parts = append(parts, u.Error)
					case u.Version != "" && u.Via == "":
						parts = append(parts, tr("Update this CLI using its original installer."))
					case u.Latest != "" && !u.Update:
						parts = append(parts, tr("Up to date"))
					}
					if u.Previous != "" && u.Previous != u.Version {
						parts = append(parts, u.Previous+" → "+u.Version, tr("New agent processes will use the updated CLI. Existing sessions keep running."))
					}
					if u.Command != "" && (u.Update || u.Error != "") {
						parts = append(parts, fmt.Sprintf(tr("Update command: %s"), u.Command))
					}
					return strings.TrimSpace(strings.Join(parts, "\n"))
				}))
				upgrade := core.Dynamic(func(cx *core.Scope) core.Node {
					u := Live(c.AgentUpdates)()[id]
					if !u.Update && !u.Updating {
						return core.Group(nil)
					}
					label := fmt.Sprintf(tr("Update to %s"), u.Latest)
					if u.Updating {
						label = tr("Updating…")
					}
					return settingsButton("upgrade-agent-"+id, label, func() { c.UpgradeAgent(id) }, Live(func() bool { return c.AgentUpdateBusy() || c.AgentUpdates()[id].Checking }))
				})
				rows = append(rows, ui.FormSection{Children: []core.Node{ui.FormRow{
					Title: a.Name, Below: settingsColumn(info, settingsActions(upgrade)),
					Control: settingsToggle("agent-"+id, tr("Enable ")+a.Name, Live(func() bool { return get().Enabled }), func(on bool) { c.SetAgentEnabled(id, on) }, Live(func() bool {
						n := 0
						for _, a := range c.Agents() {
							if a.Enabled {
								n++
							}
						}
						return get().Enabled && n <= 1
					}))}}})
			}
			for _, s := range c.AgentSettings() {
				rows = append(rows, agentSetting(cx, c, s.ID, drafts))
			}
			return settingsColumn(rows...)
		})
	})
}
func firstSettingsText(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type settingsDraft struct {
	value *core.Signal[string]
	dirty *core.Signal[bool]
}

func agentSetting(cx *core.Scope, c Host, id string, drafts map[string]*settingsDraft) core.Node {
	get := func() agent.Setting {
		for _, s := range c.AgentSettings() {
			if s.ID == id {
				return s
			}
		}
		return agent.Setting{}
	}
	s := get()
	d := drafts[id]
	if d == nil {
		d = &settingsDraft{core.NewSignal(cx, ""), core.NewSignal(cx, false)}
		drafts[id] = d
	}
	draft, dirty := d.value, d.dirty
	value := Live(func() string {
		if dirty.Get() {
			return draft.Get()
		}
		s := get()
		if s.Value != nil {
			return s.Value()
		}
		return ""
	})
	changed := Live(func() bool { s := get(); return s.Value != nil && strings.TrimSpace(value()) != s.Value() })
	save := func() {
		if changed() {
			s := get()
			if s.Save != nil {
				s.Save(value())
				dirty.Set(false)
				uiruntime.Invalidate()
			}
		}
	}
	return settingsColumn(settingsText(cx, func() string { return s.Title + "\n" + s.Help }), settingsActions(ui.Input{TestID: "agent-setting-" + id, Label: s.Title, Placeholder: s.Placeholder, Value: value, OnChange: func(v string) { draft.Set(v); dirty.Set(true) }, OnSubmit: func(string) { save() }, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(180)}}, settingsButton("save-setting-"+id, tr("Save"), save, func() bool { return !changed() })), core.Text{ContentFunc: Live(func() string {
		s := get()
		if s.Status == nil {
			return ""
		}
		msg, _ := s.Status()
		var lines []string
		for _, line := range strings.Split(msg, "\n") {
			if t := strings.TrimSpace(line); t != "" && t != "→" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}), Selectable: core.SelectableOn, Style: core.Style{FontSize: 11}, StyleFunc: Live(func() core.Style {
		s := get()
		bad := false
		if s.Status != nil {
			_, bad = s.Status()
		}
		colors := nswidgets.ColorsOf(cx)
		color := colors.Muted()
		if bad {
			color = colors.Danger()
		}
		return core.Style{Color: color}
	})})
}

// ui.Switch does not expose Disabled yet. Share its look with base.Switch so
// the last enabled agent and dependent memory settings are actually disabled.
func settingsToggle(id, label string, get func() bool, set func(bool), disabled func() bool) core.Node {
	return core.Func(func(cx *core.Scope) core.Node {
		look := ui.NewSwitchLook(cx, get, ui.SizeDefault)
		return base.Switch{TestID: id, Label: label, Checked: get, OnChange: set, Disabled: disabled, TrackStyleFunc: look.Track, ThumbStyleFunc: look.Thumb, StyleFunc: func() core.Style {
			opacity := float32(1)
			if disabled != nil && disabled() {
				opacity = .45
			}
			return core.Style{Opacity: core.Alpha(opacity)}
		}}
	})
}
