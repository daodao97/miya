package nsmobile

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	phonecache "miya/internal/phone/cache"
)

func (m *App) desktopsView(cx *core.Scope) core.Node {
	return scroll(text(tr("Desktops")), m.errorText(cx), core.Text{ContentFunc: live(m, func() string { return m.PersistenceError })},
		m.button("desktops-reload", tr("Reload desktops"), nil, m.ReloadDesktops), core.Each(live(m, func() []phonecache.Desktop { return m.Desktops.Items }), func(d phonecache.Desktop) string { return d.ID }, func(cx *core.Scope, d func() phonecache.Desktop, _ func() int) core.Node {
			name := core.NewSignal(cx, d().Name)
			return column(core.Text{ContentFunc: func() string { return d().Name }},
				core.Text{ContentFunc: live(m, func() string {
					if d().Link == m.Link {
						return tr("Current desktop · ") + m.ConnectionLine()
					}
					return tr("Saved")
				})}, base.Input{TestID: "desktop-name-" + d().ID, Label: tr("Desktop name"), Value: name.Get, OnChange: name.Set, Style: core.Style{MinHeight: core.Pt(44)}}, row(m.button("desktop-rename-"+d().ID, tr("Rename"), nil, func() { m.RenameDesktop(d().ID, name.Get()) }), m.button("desktop-switch-"+d().ID, tr("Connect"), nil, func() { m.closeFileBrowser(); m.SwitchDesktop(d().ID); m.pages.Open("") }), m.button("desktop-remove-"+d().ID, tr("Remove"), nil, func() { m.closeFileBrowser(); m.RemoveDesktop(d().ID) })))
		}), m.button("desktop-add", tr("Add desktop"), nil, func() { m.pages.Open("pair") }), m.button("desktops-done", tr("Done"), nil, func() { m.pages.Open("") }))
}
