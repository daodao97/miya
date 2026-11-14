package nsmobile

import (
	"fmt"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/desktop"
	"miya/internal/interaction"
	"miya/internal/nswidgets"
	phoneclient "miya/internal/phone/client"
	"miya/internal/phone/desktopview"
	"miya/internal/textutil"
)

// Only a phase or lease change replaces a destination. Frame arrivals change
// SourceFunc on the existing image; neither the tree nor the editor rebuilds.
func (m *App) desktopPage(cx *core.Scope) core.Node {
	type destination struct {
		page  *phoneclient.MobileDesktopPage
		phase string
	}
	key := core.NewMemo(cx, live(m, func() destination {
		p := m.DesktopPage
		if p == nil {
			return destination{}
		}
		return destination{p, p.Phase}
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		d := key.Get()
		p := d.page
		if p == nil {
			return core.View{}
		}
		nodes := []core.Node{row(m.button("desktop-back", tr("Back"), nil, func() { m.CloseDesktopPage(true) }), core.Text{ContentFunc: live(m, func() string { return textutil.FirstNonEmpty(p.Window.Title, p.Display.Name, tr("Remote desktop")) }), Style: core.Style{FlexGrow: 1, FontSize: 18, FontWeight: 600}})}
		switch d.phase {
		case "live":
			nodes = append(nodes, m.desktopLive(cx, p))
		case "picker":
			nodes = append(nodes, m.desktopPicker(cx, p))
		default:
			nodes = append(nodes, scroll(core.Show{When: live(m, func() bool { return p.Busy }), Then: func(*core.Scope) core.Node { return ui.Spinner{} }}, core.Text{TestID: "desktop-phase-title", ContentFunc: live(m, func() string { return p.Title })}, core.Text{ContentFunc: live(m, func() string { return p.Detail })}, core.Show{When: live(m, func() bool { return !p.Busy && (p.Phase == "error" || p.Phase == "paused") }), Then: func(*core.Scope) core.Node {
				return m.button("desktop-retry", tr("Reconnect"), nil, func() { m.DesktopRetry(p) })
			}}, m.button("desktop-return", tr("Back to conversation"), nil, func() { m.CloseDesktopPage(true) })))
		}
		return core.View{TestID: "mobile-desktop", Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0), Gap: 6}, Children: nodes}
	})
}
func desktopSource(f *desktopview.Frame) core.ImageSource {
	if f == nil {
		return core.ImageSource{}
	}
	return core.ImageSource{Data: f.Data, Size: core.Size{Width: core.Points(f.Width), Height: core.Points(f.Height)}}
}
func (m *App) desktopLive(cx *core.Scope, p *phoneclient.MobileDesktopPage) core.Node {
	colors := nswidgets.ColorsOf(cx)
	input := func(ev desktopview.InputEvent) { m.DesktopPointerInput(p, ev); m.change() }
	touches := &desktopview.TouchMapper{Mapper: p.Mapper(), Input: input, Scroll: func(dx, dy float32) { m.DesktopScroll(p, dx, dy) }, RightClick: func() { m.DesktopAtCursor(p, "rightClick") }}
	// Native mouse presses and raw finger events are exclusive for one gesture.
	// Raw touches retain finger IDs so a second finger cancels the pending click.
	mouse := func(kind desktopview.InputKind, e core.PressEvent) {
		if !touches.Active() {
			input(desktopview.InputEvent{Kind: kind, X: float32(e.Position.X), Y: float32(e.Position.Y)})
		}
	}
	pinchStart := float32(1)
	viewport := core.View{TestID: "desktop-viewport", Label: tr("Live Mac screen. Tap, swipe, or pinch to zoom."), Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(44), Overflow: core.Hidden, Background: colors.Canvas()},
		OnLayout: func(b core.Bounds) {
			v := desktopview.Rect{W: float32(b.Size.Width), H: float32(b.Size.Height)}
			if p.View != v {
				p.View = v
				m.change()
			}
		},
		OnTouch: func(e core.TouchEvent) {
			p.Mapper()
			events := make([]desktopview.Touch, len(e.Touches))
			for i, t := range e.Touches {
				events[i] = desktopview.Touch{ID: t.ID, X: float32(t.Position.X), Y: float32(t.Position.Y), Changed: t.Changed}
			}
			touches.Update(int(e.Phase), events)
			p.Mapped()
			m.change()
			e.StopPropagation()
		},
		Responder:   &core.Responder{Claims: []core.TouchClaim{{}}, Keep: true},
		OnPressDown: func(e core.PressEvent) { mouse(desktopview.InputPointerDown, e) }, OnPressUp: func(e core.PressEvent) { mouse(desktopview.InputPointerUp, e) },
		OnMouseMove: func(e core.MouseEvent) {
			if e.PressedButton == core.MouseButtonLeft {
				input(desktopview.InputEvent{Kind: desktopview.InputPointerMove, X: float32(e.Position.X), Y: float32(e.Position.Y)})
			}
		},
		OnContextMenu: func(core.ContextMenuEvent) { m.DesktopAtCursor(p, "rightClick"); m.change() },
		OnPinch: func(e core.GestureEvent) {
			if touches.Active() {
				return
			}
			if e.Phase == core.GestureBegan {
				pinchStart = p.Zoom
			}
			p.SetZoom(pinchStart * e.Value)
			m.change()
		},
		OnWheel: func(e core.WheelEvent) {
			input(desktopview.InputEvent{Kind: desktopview.InputScroll, X: float32(e.Position.X), Y: float32(e.Position.Y), DX: -float32(e.Delta.X), DY: -float32(e.Delta.Y)})
		},
		Children: []core.Node{
			core.Image{TestID: "desktop-frame", SourceFunc: live(m, func() core.ImageSource { return desktopSource(p.Image) }), ResizeMode: core.Contain, StyleFunc: live(m, func() core.Style {
				r, ok := p.ImageRect()
				s := core.Style{Position: core.Absolute, Left: core.Pt(core.Points(r.X)), Top: core.Pt(core.Points(r.Y)), Width: core.Pt(core.Points(r.W)), Height: core.Pt(core.Points(r.H))}
				if !ok {
					s.Width, s.Height = core.Pt(0), core.Pt(0)
				}
				return s
			})},
			core.View{PointerEvents: core.PointerEventsNone, Style: core.Style{Position: core.Absolute, Width: core.Pct(100), Height: core.Pct(100)}, Children: []core.Node{core.Canvas{TestID: "desktop-cursor", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100)}, Draw: func(d *core.Drawing, _ core.Size) {
				m.revision.Get()
				r, ok := p.ImageRect()
				if ok && p.Trackpad {
					d.BeginPath().Ellipse(r.X+float32(p.Cursor.X)*r.W, r.Y+float32(p.Cursor.Y)*r.H, 7, 7).SetFillColor(core.RGBA(0x0a84ff, 0.8)).Fill()
				}
			}}}},
		}}
	// Scope-owned shortcuts stop when the live surface unmounts. Native text
	// input owns composition; named Mac keys also remain available as buttons.
	for _, key := range []string{"escape", "tab", "enter", "backspace", "left", "right", "up", "down"} {
		cx.OnKey(key, m.act(func() { m.DesktopKey(p, key) }))
	}
	return core.View{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0), Gap: 6}, Children: []core.Node{
		core.Text{TestID: "desktop-status", ContentFunc: live(m, func() string { return p.Status }), MaxLines: 2}, viewport,
		row(m.desktopIconButton("desktop-keyboard", tr("Keyboard"), "keyboard", func() { p.Keyboard = !p.Keyboard }),
			core.Text{TestID: "desktop-pointer-mode", ContentFunc: live(m, func() string {
				if p.Trackpad {
					return tr("Trackpad")
				}
				return tr("Touch")
			})},
			m.desktopIconButton("desktop-pointer", tr("Switch control mode"), "pointer", func() { m.DesktopTogglePointer(p) }), m.desktopIconButton("desktop-more", tr("More options"), "dots", func() { p.More = !p.More }), m.desktopIconButton("desktop-minimize", tr("Show as floating window"), "picture-in-picture", func() { m.DesktopMinimize(p) })),
		core.Show{When: live(m, func() bool { return p.More }), Then: func(cx *core.Scope) core.Node { return m.desktopOptions(cx, p) }},
		m.desktopUnlock(cx, p),
		core.Show{When: live(m, func() bool { return p.Keyboard }), Then: func(cx *core.Scope) core.Node { return m.desktopKeyboard(cx, p) }},
		core.Each(live(m, func() []desktop.Action { return p.Actions.Actions }), func(a desktop.Action) string { return a.ID }, func(cx *core.Scope, a func() desktop.Action, _ func() int) core.Node {
			return m.desktopButton("desktop-action-"+a().ID, func() string { return a().Label }, live(m, func() bool { return !a().Enabled || !p.ActionsFresh || p.ActionPending }), func() { m.DesktopPressAction(p, a()) })
		}),
		core.Text{ContentFunc: live(m, func() string { return p.ActionStatus })},
	}}
}
func (m *App) desktopKeyboard(cx *core.Scope, p *phoneclient.MobileDesktopPage) core.Node {
	field := func(secret bool) core.Node {
		return base.Input{TestID: "desktop-text", Label: tr("Text to send to Mac"), Value: live(m, func() string { return p.Text }), OnChange: func(s string) { p.Text = s; m.change() }, Masked: secret, AutoFocus: true, NoAutoCorrect: true, AutoCapitalize: core.AutoCapitalizeNone, Style: core.Style{MinHeight: core.Pt(44), FontSize: 17}, OnSubmit: func(string) { m.act(func() { m.DesktopSendText(p) })() }}
	}
	secret := core.NewMemo(cx, live(m, func() bool { return p.Secret }))
	keys := []core.Node{}
	for _, k := range []struct{ key, label string }{{"enter", tr("Return")}, {"escape", tr("Esc")}, {"tab", tr("Tab")}, {"backspace", tr("Backspace")}, {"left", "←"}, {"up", "↑"}, {"down", "↓"}, {"right", "→"}} {
		keys = append(keys, m.button("desktop-key-"+k.key, k.label, nil, func() { m.DesktopKey(p, k.key) }))
	}
	for _, k := range []struct{ key, label string }{{"command", "⌘"}, {"control", "⌃"}, {"option", "⌥"}, {"shift", "⇧"}} {
		keys = append(keys, m.button("desktop-mod-"+k.key, k.label, nil, func() {
			if p.Modifier == k.key {
				p.Modifier = ""
			} else {
				p.Modifier = k.key
			}
		}))
	}
	for _, key := range []string{"a", "c", "v", "z", "l"} {
		keys = append(keys, m.button("desktop-key-"+key, strings.ToUpper(key), nil, func() { m.DesktopKey(p, key) }))
	}
	return core.Scroll{Style: core.Style{MaxHeight: core.Pt(200), FlexShrink: 1}, Children: []core.Node{column(core.Dynamic(func(*core.Scope) core.Node { return field(secret.Get()) }), row(m.button("desktop-send", tr("Send"), live(m, func() bool { return p.Text == "" || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy }), func() { m.DesktopSendText(p) }), m.button("desktop-secret", tr("Password input"), nil, func() { p.Secret = !p.Secret }), m.button("desktop-hide-keyboard", tr("Hide keyboard"), nil, func() { p.Keyboard = false; cx.DismissKeyboard() })), core.Text{ContentFunc: live(m, func() string { return p.Modifier })}, row(keys...))}}
}
func (m *App) desktopOptions(cx *core.Scope, p *phoneclient.MobileDesktopPage) core.Node {
	nodes := []core.Node{row(m.button("desktop-zoom-in", tr("Zoom in"), nil, func() { p.SetZoom(p.Zoom + .5) }), m.button("desktop-zoom-out", tr("Zoom out"), nil, func() { p.SetZoom(p.Zoom - .5) }), m.button("desktop-right-click", tr("Right click"), nil, func() { m.DesktopAtCursor(p, "rightClick") }))}
	nodes = append(nodes, core.Show{When: live(m, func() bool { return !p.Trackpad }), Then: func(*core.Scope) core.Node {
		var n []core.Node
		for i, label := range []string{tr("Click / Scroll"), tr("Drag"), tr("Move screen")} {
			n = append(n, m.button(fmt.Sprintf("desktop-touch-%d", i), label, nil, func() { p.TouchMode = i; p.Mapping.Reset() }))
		}
		return row(n...)
	}})
	if p.WindowOnly || p.Pickable {
		nodes = append(nodes, m.button("desktop-windows", tr("Switch window"), nil, func() { m.DesktopChooseWindows(p) }))
	}
	if p.Pickable && p.AppsAvailable {
		nodes = append(nodes, m.button("desktop-apps", tr("Open app"), nil, func() { m.DesktopChooseApps(p) }))
	}
	if p.DisplaysAvailable {
		nodes = append(nodes, m.button("desktop-displays", tr("Switch desktop"), nil, func() { m.DesktopChooseDisplays(p) }))
	}
	if p.Privacy.Available {
		nodes = append(nodes, text(tr("Black out your Mac displays while you keep working here. Press Control–Option–Command–P on the Mac to restore them.")), core.Text{ContentFunc: live(m, func() string {
			if !p.Privacy.Known {
				return tr("Checking…")
			}
			if p.Privacy.Enabled {
				return tr("On")
			}
			return tr("Off")
		})}, m.button("desktop-privacy-enable", tr("Enable privacy mode"), live(m, func() bool {
			return p.Privacy.Busy || !p.Privacy.Known || p.Privacy.Enabled || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy
		}), func() { m.DesktopSetPrivacy(p) }), m.button("desktop-privacy-restore", tr("Restore Mac displays"), live(m, func() bool { return p.Privacy.Busy }), func() { m.DesktopRestorePrivacy(p) }), core.Text{ContentFunc: live(m, func() string { return p.Privacy.Message })})
	}
	nodes = append(nodes, core.Show{When: live(m, func() bool { return p.Unlock.Saved }), Then: func(*core.Scope) core.Node {
		return column(text(tr("Password saved for this Mac")), m.button("desktop-forget-password", tr("Forget password"), live(m, func() bool { return p.Unlock.Busy }), func() { m.DesktopForgetUnlock(p) }))
	}})
	return core.Scroll{Style: core.Style{MaxHeight: core.Pt(220)}, Children: []core.Node{column(nodes...)}}
}
func (m *App) desktopUnlock(cx *core.Scope, p *phoneclient.MobileDesktopPage) core.Node {
	return column(core.Show{When: live(m, func() bool { return p.CanUnlock() && p.Unlock.Locked }), Then: func(*core.Scope) core.Node {
		return column(text(tr("Mac is locked")), base.Input{TestID: "desktop-unlock-password", Label: tr("Mac login password"), Masked: true, Value: live(m, func() string { return p.Unlock.Text }), OnChange: func(s string) { p.Unlock.Text = s; m.change() }, Style: core.Style{MinHeight: core.Pt(44)}}, m.button("desktop-unlock", tr("Unlock"), live(m, func() bool { return p.Unlock.Busy || p.Unlock.Text == "" }), func() { m.DesktopUnlock(p) }), core.Show{When: func() bool { return m.DesktopAuthenticate == nil }, Then: func(*core.Scope) core.Node {
			return text(tr("Password saving requires device authentication, which is unavailable in this NSGo build."))
		}},
			core.Show{When: live(m, func() bool { return p.Unlock.Saved && m.DesktopAuthenticate != nil }), Then: func(*core.Scope) core.Node {
				return m.button("desktop-unlock-saved", tr("Verify and unlock"), live(m, func() bool { return p.Unlock.Busy }), func() { m.DesktopUnlockSaved(p) })
			}},
			core.Show{When: func() bool { return m.DesktopAuthenticate != nil && m.Store != nil }, Then: func(*core.Scope) core.Node {
				return ui.Checkbox{TestID: "desktop-unlock-save", Text: tr("Save password for automatic unlock after identity verification"), Checked: live(m, func() bool { return p.Unlock.Remember }), OnChange: func(v bool) { p.Unlock.Remember = v; m.change() }, Disabled: live(m, func() bool { return p.Unlock.Busy }), Style: core.Style{MinHeight: core.Pt(44)}}
			}})
	}}, core.Text{ContentFunc: live(m, func() string { return p.Unlock.Status })})
}
func (m *App) desktopPicker(cx *core.Scope, p *phoneclient.MobileDesktopPage) core.Node {
	tab := core.NewMemo(cx, live(m, func() int { return p.PickerTab }))
	return scroll(row(m.button("desktop-tab-windows", tr("Windows"), live(m, func() bool { return p.Busy }), func() { m.DesktopChooseWindows(p) }), core.Show{When: live(m, func() bool { return p.Pickable && p.AppsAvailable }), Then: func(*core.Scope) core.Node {
		return m.button("desktop-tab-apps", tr("Apps"), live(m, func() bool { return p.Busy }), func() { m.DesktopChooseApps(p) })
	}}, core.Show{When: live(m, func() bool { return p.DisplaysAvailable }), Then: func(*core.Scope) core.Node {
		return m.button("desktop-tab-displays", tr("Desktops"), live(m, func() bool { return p.Busy }), func() { m.DesktopChooseDisplays(p) })
	}}),
		core.Text{ContentFunc: live(m, func() string { return p.AppStatus })}, core.Show{When: live(m, func() bool { return p.Busy }), Then: func(*core.Scope) core.Node { return ui.Spinner{} }},
		core.Dynamic(func(cx *core.Scope) core.Node {
			switch tab.Get() {
			case 1:
				return column(base.Input{TestID: "desktop-app-search", Label: tr("Search apps"), Placeholder: tr("Search apps"), Value: live(m, func() string { return p.AppSearch }), OnChange: func(s string) { p.AppSearch = s; m.change() }, Style: core.Style{MinHeight: core.Pt(44)}}, core.Each(live(m, func() []desktop.Application {
					var apps []desktop.Application
					for _, a := range p.Apps {
						if strings.Contains(strings.ToLower(a.Name+" "+a.BundleID), strings.ToLower(strings.TrimSpace(p.AppSearch))) {
							apps = append(apps, a)
						}
					}
					return apps
				}), func(a desktop.Application) string { return a.ID }, func(cx *core.Scope, a func() desktop.Application, _ func() int) core.Node {
					return row(core.Image{SourceFunc: live(m, func() core.ImageSource { return desktopSource(p.AppIcons[a().ID]) }), Style: core.Style{Width: core.Pt(40), Height: core.Pt(40)}}, m.button("desktop-app-"+a().ID, fmt.Sprintf(tr("Open %s"), a().Name), live(m, func() bool { return p.Busy }), func() { m.DesktopLaunchApp(p, a()) }))
				}))
			case 2:
				return core.Each(live(m, func() []desktop.Display { return p.Displays }), func(d desktop.Display) string { return d.UUID }, func(cx *core.Scope, d func() desktop.Display, i func() int) core.Node {
					return m.button("desktop-display-"+d().UUID, phoneclient.DesktopDisplayTitle(d(), i()+1), live(m, func() bool { return p.Busy }), func() { m.DesktopSelectDisplay(p, d()) })
				})
			default:
				return column(core.Show{When: live(m, func() bool { return p.Pickable && !p.DisplaysAvailable }), Then: func(*core.Scope) core.Node {
					return m.button("desktop-full", tr("Full desktop"), nil, func() { m.DesktopSelectFull(p) })
				}}, text(tr("Only the selected window is shared. Complete Touch ID or local confirmation on your Mac when required.")), core.Each(live(m, func() []desktop.Window { return p.Windows }), phoneclient.DesktopWindowKey, func(cx *core.Scope, w func() desktop.Window, _ func() int) core.Node {
					k := phoneclient.DesktopWindowKey(w())
					return column(core.Image{SourceFunc: live(m, func() core.ImageSource { return desktopSource(p.Thumbs[k]) }), Style: core.Style{Height: core.Pt(100), Width: core.Pct(100)}, ResizeMode: core.Contain}, m.button("desktop-window-"+k, textutil.FirstNonEmpty(w().Title, w().App, tr("Untitled window")), live(m, func() bool { return p.Busy }), func() { m.DesktopSelectWindow(p, w()) }))
				}))
			}
		}), m.button("desktop-refresh", tr("Refresh list"), live(m, func() bool { return p.Busy }), func() {
			switch p.PickerTab {
			case 1:
				m.DesktopChooseApps(p)
			case 2:
				m.DesktopChooseDisplays(p)
			default:
				m.DesktopChooseWindows(p)
			}
		}))
}
func (m *App) desktopPreviews(cx *core.Scope) core.Node {
	capability := core.NewMemo(cx, live(m, func() int {
		if !m.State.DesktopControl {
			return 0
		}
		if m.State.DesktopWindows {
			return 2
		}
		return 1
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch capability.Get() {
		case 0:
			return core.View{}
		case 2:
			return m.desktopPreviewCards(cx)
		default:
			return core.Show{When: live(m, func() bool { return len(m.State.Approvals) > 0 }), Then: func(*core.Scope) core.Node {
				return m.button("desktop-preview-open", tr("Open remote desktop"), live(m, func() bool { return !m.Connected || m.DesktopRequest != "" }), m.OpenRemoteDesktop)
			}}
		}
	})
}
func (m *App) desktopPreviewCards(cx *core.Scope) core.Node {
	return core.Each(live(m, func() []interaction.Request {
		var requests []interaction.Request
		for _, a := range m.State.Approvals {
			if a.Kind() == interaction.DesktopControl {
				requests = append(requests, a)
			}
		}
		return requests
	}), func(a interaction.Request) string { return string(a.ID) }, func(cx *core.Scope, a func() interaction.Request, _ func() int) core.Node {
		id := string(a().ID)
		// Request only when identity changes, not on every frame/revision.
		version := ""
		cx.Effect(func() {
			m.revision.Get()
			v := phoneclient.DesktopApprovalVersion(a())
			if !m.Connected {
				version = ""
				return
			}
			if v != version || m.DesktopPreviews[id] == nil {
				version = v
				cx.Post(m.act(func() { m.RequestWindowPreview(a()) }))
			}
		})
		preview := func() *phoneclient.MobileWindowPreview { m.revision.Get(); return m.DesktopPreviews[id] }
		return column(core.Text{ContentFunc: func() string {
			if p := preview(); p != nil {
				return textutil.FirstNonEmpty(p.Dialog.Title, a().Reason)
			}
			return a().Reason
		}}, core.Text{ContentFunc: func() string {
			if p := preview(); p != nil {
				return p.Dialog.Message
			}
			return ""
		}, MaxLines: 6}, core.Image{TestID: "desktop-preview-" + id, SourceFunc: func() core.ImageSource {
			if p := preview(); p != nil {
				return desktopSource(p.Image)
			}
			return core.ImageSource{}
		}, Style: core.Style{Height: core.Pt(140), Width: core.Pct(100)}, ResizeMode: core.Contain}, core.Text{ContentFunc: func() string {
			if p := preview(); p != nil {
				return p.Message
			}
			return ""
		}}, m.button("desktop-takeover-"+id, tr("Control permission window"), live(m, func() bool {
			return !m.Connected || m.DesktopRequest != "" || m.DesktopPage != nil && !m.DesktopPage.Floating
		}), func() {
			if p := preview(); p != nil {
				req := a()
				m.OpenDesktopWindow(&req, p.Window)
			}
		}), m.button("desktop-preview-retry-"+id, tr("Retry preview"), live(m, func() bool { p := preview(); return !m.Connected || p != nil && p.Loading }), func() { m.RequestWindowPreview(a()) }), core.Each(func() []desktop.Action {
			if p := preview(); p != nil {
				return p.Dialog.Actions
			}
			return nil
		}, func(v desktop.Action) string { return v.ID }, func(cx *core.Scope, v func() desktop.Action, _ func() int) core.Node {
			return m.desktopButton("desktop-preview-action-"+v().ID, func() string { return v().Label }, live(m, func() bool {
				p := preview()
				return p == nil || p.Stale || p.ActionPending || !v().Enabled || !m.Connected
			}), func() {
				if p := preview(); p != nil {
					m.PressWindowAction(a(), p, v())
				}
			})
		}))
	})
}
func (m *App) desktopFloat(cx *core.Scope) core.Node {
	key := core.NewMemo(cx, live(m, func() *phoneclient.MobileDesktopPage {
		if m.DesktopPage != nil && m.DesktopPage.Floating && m.Path != "/desktop" {
			return m.DesktopPage
		}
		return nil
	}))
	return core.Dynamic(func(cx *core.Scope) core.Node {
		p := key.Get()
		if p == nil {
			return core.View{}
		}
		startX, startY := float32(0), float32(0)
		return core.View{TestID: "desktop-float", Label: tr("Restore remote desktop"), Role: core.RoleButton, StyleFunc: live(m, func() core.Style {
			return core.Style{Position: core.Absolute, Right: core.Pt(core.Points(12 - p.FloatX)), Bottom: core.Pt(core.Points(12 - p.FloatY)), Width: core.Pt(160), Padding: core.All(6), Background: nswidgets.ColorsOf(cx).Bubble(), CornerRadius: 12}
		}), OnPress: func(core.PressEvent) { m.act(func() { m.DesktopRestore(p) })() }, OnPan: func(e core.PanEvent) {
			if e.Phase == core.GestureBegan {
				startX, startY = p.FloatX, p.FloatY
			}
			p.FloatX = max(-190, min(0, startX+float32(e.Translation.X)))
			p.FloatY = max(-500, min(0, startY+float32(e.Translation.Y)))
			m.change()
		}, Children: []core.Node{core.Image{TestID: "desktop-float-frame", SourceFunc: live(m, func() core.ImageSource { return desktopSource(p.Image) }), Style: core.Style{Width: core.Pct(100), Height: core.Pt(100)}, ResizeMode: core.Contain}, core.Text{ContentFunc: live(m, func() string { return textutil.FirstNonEmpty(p.Title, p.Status, tr("Remote desktop")) }), MaxLines: 1}, m.button("desktop-float-close", tr("End control"), nil, func() { m.CloseDesktopPage(false) })}}
	})
}

func (m *App) desktopIconButton(id, label, icon string, press func()) core.Node {
	return core.View{TestID: id, Role: core.RoleButton, Label: label, Style: core.Style{Width: core.Pt(44), Height: core.Pt(44), FlexShrink: 0, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, OnPress: func(core.PressEvent) { m.act(press)() }, Children: []core.Node{nswidgets.Icon(icon, 22, nil)}}
}

func (m *App) desktopButton(id string, label func() string, disabled func() bool, press func()) core.Node {
	return core.View{TestID: id, Role: core.RoleButton, LabelFunc: label, Disabled: disabled, Style: core.Style{MinHeight: core.Pt(44), Padding: core.Symmetric(10, 12), CornerRadius: 10}, OnPress: func(core.PressEvent) {
		if disabled == nil || !disabled() {
			m.act(press)()
		}
	}, Children: []core.Node{core.Text{ContentFunc: label}}}
}
