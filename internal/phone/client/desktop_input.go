package client

import (
	"context"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/phone/desktopview"
)

const DesktopFloatPollInterval = time.Second

func (p *MobileDesktopPage) Mapper() *desktopview.Mapper {
	q := &p.Mapping
	q.Active, q.Blocked = p.Active, p.Unlock.Locked || p.Unlock.Busy || p.Floating
	q.Width, q.Height = desktopview.ImageSize(p.Image)
	q.View = p.View
	q.Zoom, q.PanX = p.Zoom, p.PanX
	q.SetPanY(p.panY)
	q.Trackpad, q.TouchMode, q.Cursor = p.Trackpad, p.TouchMode, p.Cursor
	return q
}
func (p *MobileDesktopPage) Mapped() {
	p.Zoom, p.PanX, p.panY, p.Cursor = p.Mapping.Zoom, p.Mapping.PanX, p.Mapping.PanY(), p.Mapping.Cursor
}
func (p *MobileDesktopPage) ImageRect() (desktopview.Rect, bool) {
	r, ok := p.Mapper().ImageRect()
	p.Mapped()
	return r, ok
}
func (p *MobileDesktopPage) SetZoom(z float32) { p.Mapper().SetZoom(z); p.Mapped() }
func (m *Client) DesktopPointerInput(p *MobileDesktopPage, ev desktopview.InputEvent) bool {
	q := p.Mapper()
	q.Send = func(in desktop.Input) { m.DesktopSend(p, in) }
	q.After = func(d time.Duration, f func()) { m.desktopAfter(p, d, func() { p.Mapper(); f(); p.Mapped() }) }
	ok := q.Input(ev)
	p.Mapped()
	return ok
}
func (m *Client) DesktopScroll(p *MobileDesktopPage, dx, dy float32) {
	q := p.Mapper()
	q.Send = func(in desktop.Input) { m.DesktopSend(p, in) }
	q.Scroll(p.Cursor, dx, dy)
	p.Mapped()
}
func (m *Client) DesktopAtCursor(p *MobileDesktopPage, kind string) {
	m.DesktopSend(p, desktop.Input{Kind: kind, X: p.Cursor.X, Y: p.Cursor.Y})
}
func (m *Client) DesktopKey(p *MobileDesktopPage, key string) {
	m.DesktopSend(p, desktop.Input{Kind: "key", Key: key, Modifier: p.Modifier})
	p.Modifier = ""
}
func (m *Client) DesktopSendText(p *MobileDesktopPage) {
	if p.Text == "" || !p.Active {
		return
	}
	if len(p.Text) > 4096 {
		p.Status = tr("Send up to 4096 bytes of text at a time")
		return
	}
	if p.Image == nil || p.Unlock.Locked || p.Unlock.Busy {
		return
	}
	value := p.Text
	p.Text = ""
	m.DesktopSend(p, desktop.Input{Kind: "text", Text: value})
}
func (m *Client) DesktopTogglePointer(p *MobileDesktopPage) {
	p.Trackpad = !p.Trackpad
	p.Mapping.Reset()
	path := m.desktopPrefsFile()
	mode := "touch"
	if p.Trackpad {
		mode = "trackpad"
	}
	go func() { _ = control.WritePrefs(path, mode) }()
}
func (m *Client) DesktopMinimize(p *MobileDesktopPage) {
	if m.DesktopPage != p || p.Phase == "ended" {
		return
	}
	p.Floating = true
	p.Mapping.Reset()
	p.Queue, p.Text, p.Modifier = nil, "", ""
	p.Keyboard, p.More, p.MoreKeys = false, false, false
	m.Path = m.DesktopReturn
}
func (m *Client) DesktopRestore(p *MobileDesktopPage) {
	if m.DesktopPage != p {
		return
	}
	p.Floating = false
	m.DesktopReturn = m.Path
	m.Path = "/desktop"
}
func (m *Client) DesktopSelectFull(p *MobileDesktopPage) {
	if !p.Pickable {
		return
	}
	if p.DisplaysAvailable {
		m.DesktopChooseDisplays(p)
		return
	}
	p.InvalidateTarget()
	m.DesktopStage(p, "connecting", tr("Opening desktop"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.SelectWindow(ctx, desktop.Window{})
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to open window"), err)
				return
			}
			p.WindowOnly = false
			p.Window = desktop.Window{}
			p.Display = desktop.Display{}
			m.desktopStart(p)
		}
	})
}
func DesktopDisplayTitle(d desktop.Display, n int) string { return desktopDisplayTitle(d, n) }
func DesktopWindowKey(w desktop.Window) string            { return desktopWindowKey(w) }
