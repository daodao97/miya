package nsmobile

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	phonecache "miya/internal/phone/cache"
)

func (m *App) desktopsView(cx *core.Scope) core.Node {
	err := live(m, func() string { return strings.TrimSpace(m.Error + "\n" + m.PersistenceError) })
	return m.groupedPage(cx,
		groupedState(cx, "alert-circle", err, nil, func() bool { return true }),
		groupedState(cx, "device-desktop", live(m, func() string {
			if len(m.Desktops.Items) == 0 {
				return tr("No saved desktops yet")
			}
			return ""
		}), nil, nil),
		core.Show{When: live(m, func() bool { return len(m.Desktops.Items) > 0 }), Then: func(cx *core.Scope) core.Node {
			return groupedSection(cx, "", nil,
				core.Each(live(m, func() []phonecache.Desktop { return m.Desktops.Items }),
					func(d phonecache.Desktop) string { return d.ID },
					func(cx *core.Scope, desktop func() phonecache.Desktop, index func() int) core.Node {
						d := core.Untracked(cx, desktop)
						return core.Group{
							core.Show{When: func() bool { return index() > 0 }, Then: groupedDivider},
							m.groupedRow(cx, gRow{ID: "desktop-entry-" + d.ID, LabelFunc: func() string { return desktop().Name }, Icon: "device-desktop",
								DetailFunc: live(m, func() string {
									if desktop().Link == m.Link {
										return m.ConnectionLine()
									}
									return tr("Saved")
								}),
								Chevron: true, Press: func() { m.pages.Open("desktop-edit:" + d.ID) }}),
						}
					}),
			)
		}},
		m.group(cx, "", nil, gRow{ID: "desktops-reload", Label: tr("Reload desktops"), Icon: "refresh", Action: true, Press: m.ReloadDesktops}),
	)
}

func (m *App) desktopEdit(cx *core.Scope, id string) core.Node {
	var desktop phonecache.Desktop
	for _, d := range m.Desktops.Items {
		if d.ID == id {
			desktop = d
			break
		}
	}
	if desktop.ID == "" {
		return m.groupedPage(cx, groupedState(cx, "device-desktop", func() string { return tr("No saved desktops yet") }, nil, nil))
	}
	name := core.NewSignal(cx, desktop.Name)
	disabled := func() bool { s := strings.TrimSpace(name.Get()); return s == "" || s == desktop.Name }
	save := func() {
		m.RenameDesktop(id, strings.TrimSpace(name.Get()))
		if m.PersistenceError == "" {
			m.pages.Pop()
		}
	}
	remove := func() {
		label := tr("Remove")
		base.ShowAlertDialog(cx, base.AlertDialog{Title: tr("Forget desktop"), Description: desktop.Name,
			Actions: []core.AlertAction{{Label: tr("Cancel"), Role: core.AlertCancel}, {Label: label, Role: core.AlertDestructive}}}, func(chosen string) {
			if chosen == label {
				m.act(func() {
					wasConnected := desktop.Link == m.Link
					if wasConnected {
						m.closeFileBrowser()
					}
					m.RemoveDesktop(id)
					if wasConnected {
						m.pages.Open("")
					} else {
						m.pages.Pop()
					}
				})()
			}
		})
	}
	return m.groupedPage(cx,
		groupedSection(cx, "", nil,
			base.Input{TestID: "desktop-name-" + id, Label: tr("Desktop name"), Value: name.Get, OnChange: name.Set,
				OnSubmit: func(string) {
					if !disabled() {
						m.act(save)()
					}
				},
				Style: core.Style{MinHeight: core.Pt(52), Padding: core.Symmetric(12, 16), FontSize: 17}},
			groupedDivider(cx),
			m.groupedRow(cx, gRow{ID: "desktop-rename-" + id, Label: tr("Save"), Action: true, Disabled: disabled, Press: save})),
		m.group(cx, "", nil,
			gRow{ID: "desktop-switch-" + id, Label: tr("Connect"), Icon: "device-desktop", Action: true,
				Press: func() { m.closeFileBrowser(); m.SwitchDesktop(id); m.pages.Open("") }}),
		m.group(cx, "", nil, gRow{ID: "desktop-remove-" + id, Label: tr("Forget desktop"), Destructive: true, Press: remove}),
	)
}
