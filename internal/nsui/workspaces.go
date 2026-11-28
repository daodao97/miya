package nsui

import (
	"context"
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/service/workspaces"
)

// Each workspace owns its controller and whole window state, as on MyGo:
// keeping them alive keeps drafts, terminals and reconnects bound to the
// right host when two servers reuse conversation numbers or paths. The hub
// lives in the app's scope and changes only on the engine goroutine.
type workspaceHub struct {
	cx                 *core.Scope
	root               Host
	active             *core.Signal[Host]
	store              *core.Signal[workspaces.Store]
	sessions           map[string]Host
	loadErr            error
	err, pending       *core.Signal[string]
	busy, dialog       *core.Signal[bool]
	editID, name, link *core.Signal[string]
	generation         uint64
	cancel             context.CancelFunc
	closed             bool
	usage              *desktopUsage
	usages             map[Host]*desktopUsage
	connect            func(context.Context, workspaces.Workspace) (*svcclient.Client, svcproto.State, error)
}

func newWorkspaceHub(cx *core.Scope, root Host, store workspaces.Store, loadErr error) *workspaceHub {
	m := &workspaceHub{cx: cx, root: root, sessions: map[string]Host{store.Active: root}, loadErr: loadErr, connect: connectWorkspace,
		active: core.NewSignalFunc(cx, root, func(a, b Host) bool { return a == b }),
		store:  core.NewSignalFunc(cx, store, func(a, b workspaces.Store) bool { return reflect.DeepEqual(a, b) }),
		err:    core.NewSignal(cx, ""), pending: core.NewSignal(cx, ""),
		busy: core.NewSignal(cx, false), dialog: core.NewSignal(cx, false),
		editID: core.NewSignal(cx, ""), name: core.NewSignal(cx, ""), link: core.NewSignal(cx, "")}
	if loadErr != nil {
		m.err.Set(loadErr.Error())
	}
	m.bind(root)
	m.usage = newDesktopUsage(cx, root)
	m.usages = map[Host]*desktopUsage{root: m.usage}
	m.usage.start()
	cx.Cleanup(m.close)
	return m
}

func connectWorkspace(ctx context.Context, item workspaces.Workspace) (*svcclient.Client, svcproto.State, error) {
	if item.ID == workspaces.Local {
		return environment.LocalConnection()
	}
	return workspaces.ConnectRemoteServiceContext(ctx, item.Link)
}

// bind lets a workspace's controller know whether it shows, and bring
// itself forward, as a notification for it does.
func (m *workspaceHub) bind(h Host) {
	st := h.State()
	st.WorkspaceVisible = func() bool { return m.active.GetUntracked() == h }
	st.RevealWorkspace = func() {
		for id, s := range m.sessions {
			if s == h {
				m.selectWorkspace(id)
				return
			}
		}
	}
}

func (m *workspaceHub) cancelConnection() {
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy.Set(false)
}

func (m *workspaceHub) selectWorkspace(id string) {
	item, ok := m.store.GetUntracked().Find(id)
	if !ok || m.closed {
		return
	}
	m.connectTo(item)
}

// save makes next the store, on disk first.
func (m *workspaceHub) save(next workspaces.Store) bool {
	if err := next.Save(); err != nil {
		m.err.Set(err.Error())
		return false
	}
	m.store.Set(next)
	return true
}

func (m *workspaceHub) connectTo(item workspaces.Workspace) {
	if m.loadErr != nil {
		m.err.Set(m.loadErr.Error())
		return
	}
	m.cancelConnection()
	m.err.Set("")
	if session := m.sessions[item.ID]; session != nil {
		next := m.store.GetUntracked().Clone()
		if item.ID != workspaces.Local {
			if _, err := next.Remember(item.Link, item.Name); err != nil {
				m.err.Set(err.Error())
				return
			}
		}
		next.Active = item.ID
		if m.save(next) {
			m.dialog.Set(false)
			m.activate(session)
		}
		return
	}
	if item.ID == workspaces.Local && (runtime.GOOS == "windows" || os.Getenv("MIYA_SERVICE") == "0") {
		next := m.store.GetUntracked().Clone()
		next.Active = item.ID
		if m.save(next) {
			h := environment.NewLocal()
			m.sessions[item.ID] = h
			m.bind(h)
			m.dialog.Set(false)
			m.activate(h)
		}
		return
	}
	m.pending.Set(item.Name)
	m.busy.Set(true)
	ctx, cancel := context.WithCancel(m.root.State().Ctx)
	m.cancel = cancel
	gen, connect := m.generation, m.connect
	go func() {
		cl, state, err := connect(ctx, item)
		m.cx.Post(func() { m.finishConnection(gen, item, cl, state, err) })
	}()
}

func (m *workspaceHub) finishConnection(gen uint64, item workspaces.Workspace, cl *svcclient.Client, state svcproto.State, err error) {
	if m.closed || gen != m.generation {
		if cl != nil {
			cl.Close()
		}
		return
	}
	m.busy.Set(false)
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if err == nil && cl == nil {
		err = svcproto.ErrClosed
	}
	if err != nil {
		if cl != nil {
			cl.Close()
		}
		m.err.Set(err.Error())
		return
	}
	next := m.store.GetUntracked().Clone()
	if item.ID != workspaces.Local {
		item, err = next.Remember(item.Link, item.Name)
	}
	if err == nil {
		next.Active = item.ID
		err = next.Save()
	}
	if err != nil {
		cl.Close()
		m.err.Set(err.Error())
		return
	}
	c := environment.NewMirror()
	c.ConfigureMirror(item.ID != workspaces.Local, func() (*svcclient.Client, svcproto.State, error) { return m.connect(c.State().Ctx, item) })
	c.AttachService(cl, state)
	m.store.Set(next)
	m.sessions[item.ID] = c
	m.bind(c)
	m.dialog.Set(false)
	m.activate(c)
}

func (m *workspaceHub) activate(h Host) {
	if m.active.GetUntracked() == h {
		return
	}
	m.usage.stop()
	m.usage = m.usages[h]
	if m.usage == nil {
		m.cx.Run(func() { m.usage = newDesktopUsage(m.cx, h) })
		m.usages[h] = m.usage
	}
	m.usage.start()
	m.active.Set(h)
	h.EnsurePhoneHost()
}

func (m *workspaceHub) close() {
	if m.closed {
		return
	}
	m.closed = true
	m.usage.stop()
	m.cancelConnection()
	for _, h := range m.sessions {
		if h == m.root {
			continue
		}
		st := h.State()
		if st.Cancel != nil {
			st.Cancel()
		}
		if st.Remote != nil {
			st.Remote.Close()
		}
	}
}

func (m *workspaceHub) openAdd() {
	m.cancelConnection()
	m.name.Set("")
	m.link.Set("")
	m.editID.Set("")
	m.err.Set("")
	m.dialog.Set(true)
}

func (m *workspaceHub) openRename(item workspaces.Workspace) {
	m.cancelConnection()
	m.name.Set(item.Name)
	m.editID.Set(item.ID)
	m.err.Set("")
	m.dialog.Set(true)
}

func (m *workspaceHub) rename() {
	next := m.store.GetUntracked().Clone()
	id := m.editID.GetUntracked()
	for i := range next.Items {
		if next.Items[i].ID == id {
			next.Items[i].Name = strings.TrimSpace(m.name.GetUntracked())
		}
	}
	if m.save(next) {
		m.err.Set("")
		m.dialog.Set(false)
	}
}

func (m *workspaceHub) add() {
	next := m.store.GetUntracked().Clone()
	item, err := next.Remember(m.link.GetUntracked(), m.name.GetUntracked())
	if err != nil {
		m.err.Set(err.Error())
		return
	}
	m.connectTo(item)
}

func workspaceLabel(w workspaces.Workspace) string {
	if w.ID == workspaces.Local {
		return tr("Local")
	}
	return w.Name
}

// workspaceSelector is the sidebar's workspace menu, the connection's
// progress or failure under it, and the dialog adding or renaming one.
func workspaceSelector(cx *core.Scope, m *workspaceHub) core.Node {
	colors := nswidgets.ColorsOf(cx)
	open := core.NewSignal(cx, false)
	menu := core.Dynamic(func(cx *core.Scope) core.Node {
		store := m.store.Get()
		current := store.Current()
		symbol := "device-desktop"
		if current.ID != workspaces.Local {
			symbol = "server"
		}
		face := []core.Node{nswidgets.Icon(symbol, 15, colors.Muted)}
		// The local workspace is known by its icon; the menu still names it.
		if current.ID != workspaces.Local {
			face = append(face, core.Text{Content: current.Name, MaxLines: 1, Style: core.Style{FontSize: 12, FlexShrink: 1, MinWidth: core.Pt(0)}})
		}
		face = append(face, nswidgets.Icon("chevron-down", 11, colors.Faint))
		var items []core.Node
		for _, item := range store.Items {
			id := item.ID
			items = append(items, ui.MenuItem{Text: workspaceLabel(item), TestID: "workspace-" + id,
				Checked:  func() bool { return m.store.Get().Active == id },
				OnSelect: func() { m.selectWorkspace(id) }})
		}
		if current.ID != workspaces.Local {
			items = append(items, ui.MenuSeparator{}, ui.MenuItem{Text: tr("Remote desktop"), TestID: "workspace-remote-desktop",
				Disabled: Live(func() bool {
					cl := m.active.Get().State().Remote
					return cl == nil || !cl.Connected() || cl.Wire() == nil
				}),
				OnSelect: m.openPeerDesktop})
		}
		items = append(items, ui.MenuSeparator{},
			ui.MenuItem{Text: tr("Add remote workspace…"), TestID: "add-workspace", Disabled: func() bool { return m.loadErr != nil }, OnSelect: m.openAdd})
		// Any saved remote workspace can be renamed without switching to it.
		for _, item := range store.Items {
			if item.ID == workspaces.Local {
				continue
			}
			item := item
			items = append(items, ui.MenuItem{Text: tr("Rename workspace") + " · " + item.Name, TestID: "rename-workspace-" + item.ID,
				OnSelect: func() { m.openRename(item) }})
		}
		return ui.Menu{Open: open.Get, OnOpenChange: func(o bool, _ base.Reason) { open.Set(o) },
			Label: tr("Switch workspace"), TestID: "workspace-switcher", Placement: core.PlaceTop,
			Face: core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(30), Padding: core.Symmetric(0, 8), MinWidth: core.Pt(0)},
				Children: face},
			Children: items}
	})
	status := core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
		core.Show{When: func() bool { return m.busy.Get() && !m.dialog.Get() }, Then: func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				ui.Spinner{Size: 12},
				core.Text{TestID: "workspace-connecting", MaxLines: 1, ContentFunc: func() string { return tr("Connecting to ") + m.pending.Get() + "…" },
					StyleFunc: func() core.Style {
						return core.Style{FontSize: 11, Color: colors.Muted(), FlexGrow: 1, MinWidth: core.Pt(0)}
					}},
				ui.IconButton{Icon: nswidgets.IconOf("x"), Label: tr("Cancel connection"), Size: 22, IconSize: 12, OnPress: m.cancelConnection},
			}}
		}},
		core.Show{When: func() bool { return m.err.Get() != "" && !m.dialog.Get() }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "workspace-error", MaxLines: 3, ContentFunc: m.err.Get,
				StyleFunc: func() core.Style { return core.Style{FontSize: 11, Color: colors.Danger()} }}
		}},
	}}
	return core.View{Style: core.Style{Gap: 4}, Children: []core.Node{menu, status, workspaceDialog(cx, m)}}
}

func workspaceDialog(cx *core.Scope, m *workspaceHub) core.Node {
	colors := nswidgets.ColorsOf(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		editing := m.editID.Get() != ""
		title := tr("Add remote workspace")
		if editing {
			title = tr("Rename workspace")
		}
		var body []core.Node
		if !editing {
			body = append(body, nswidgets.MutedText(cx, func() string {
				return tr("Once connected, this server's projects and conversations appear in the sidebar.")
			}))
		}
		body = append(body, ui.Input{Label: tr("Workspace name"), Placeholder: tr("For example, prod-us"), Value: m.name.Get, OnChange: m.name.Set})
		if !editing {
			body = append(body, ui.Input{Label: tr("Connection code"), Placeholder: "miya://connect?…", Value: m.link.Get, OnChange: m.link.Set})
		}
		body = append(body, core.Show{When: func() bool { return m.err.Get() != "" }, Then: func(cx *core.Scope) core.Node {
			return core.Text{TestID: "workspace-dialog-error", ContentFunc: m.err.Get, Selectable: core.SelectableOn,
				StyleFunc: func() core.Style { return core.Style{FontSize: 12, Color: colors.Danger()} }}
		}})
		footer := []core.Node{ui.Button{Text: tr("Cancel"), Variant: ui.ButtonSecondary, TestID: "workspace-cancel", OnPress: func() {
			m.cancelConnection()
			m.dialog.Set(false)
		}}}
		if editing {
			footer = append(footer, ui.Button{Text: tr("Save"), TestID: "workspace-rename",
				Disabled: func() bool { return strings.TrimSpace(m.name.Get()) == "" }, OnPress: m.rename})
		} else {
			footer = append(footer, core.Dynamic(func(cx *core.Scope) core.Node {
				label := tr("Add and connect")
				if m.busy.Get() {
					label = tr("Connecting…")
				}
				return ui.Button{Text: label, TestID: "workspace-connect",
					Disabled: func() bool { return m.busy.Get() || strings.TrimSpace(m.link.Get()) == "" }, OnPress: m.add}
			}))
		}
		return ui.Dialog{Open: m.dialog.Get, TestID: "workspace-dialog", Title: title, Children: body, Footer: footer,
			OnOpenChange: func(o bool, _ base.Reason) {
				if !o {
					m.cancelConnection()
				}
				m.dialog.Set(o)
			}}
	})
}
