package client

import (
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/remote"
)

// Keep the target and local view preferences, never live input or image data.
// Reconnection creates a new connection-bound lease rather than reusing a token
// whose connection was closed while iOS suspended the app.
func (m *Client) suspendDesktopPage() {
	p := m.DesktopPage
	if p == nil || p.Phase == "ended" {
		return
	}
	if p.suspended {
		p.resuming, p.resumeBlocked = false, false
		m.DesktopStage(p, "paused", tr("Connecting to Mac"), "", true)
		return
	}
	p.resumePicker = p.Phase == "picker"
	p.suspended, p.resuming = true, false
	p.Cancel()
	p.InvalidateTarget()
	p.ImageHash = [32]byte{}
	p.Text, p.Modifier, p.Unlock.Text = "", "", ""
	p.Keyboard, p.MoreKeys = false, false
	p.Actions = control.Actions{}
	p.Unlock = DesktopUnlockState{Key: p.Unlock.Key}
	p.Privacy = mobileDesktopPrivacy{}
	p.Apps, p.AppIcons, p.Thumbs = nil, nil, nil
	m.DesktopStage(p, "paused", tr("Connecting to Mac"), "", true)
	m.desktopRelease(p)
	if p.Client != nil {
		go p.Client.Stop()
	}
}

// Called after a fresh state: pending approvals and stable conversation IDs
// must be checked against the reconnected Mac, not a cached screen.
func (m *Client) resumeDesktopPage() {
	p := m.DesktopPage
	if m.background || p == nil || !p.suspended || p.resuming || p.resumeBlocked || !m.Connected || m.Conn == nil || m.DesktopRequest != "" {
		return
	}
	if p.desktopLink != m.Link {
		m.DesktopFinish(p, tr("Remote control ended"))
		return
	}
	r := p.owner
	if (r.WindowOnly && r.ServerID == "") || !m.State.DesktopControl {
		return
	}
	if r.WindowOnly && p.Window.ID != 0 {
		r.WindowID, r.WindowPID = p.Window.ID, p.Window.PID
	}
	p.resuming = true
	m.DesktopStage(p, "paused", tr("Connecting to Mac"), tr("Checking the connection and system permissions."), true)
	m.sendDesktopOpen(r)
}

func (m *Client) reopenDesktopPage(conn *remote.Conn, token string) {
	old := m.DesktopPage
	p := m.newDesktopPage(conn, token)
	p.owner, p.desktopLink = old.owner, old.desktopLink
	p.WindowOnly, p.Window, p.Display = old.WindowOnly, old.Window, old.Display
	p.PickerTab, p.resumePicker = old.PickerTab, old.resumePicker
	p.Zoom, p.PanX, p.panY = old.Zoom, old.PanX, old.panY
	p.Cursor, p.Trackpad, p.TouchMode = old.Cursor, old.Trackpad, old.TouchMode
	p.Secret, p.Floating, p.FloatX, p.FloatY = old.Secret, old.Floating, old.FloatX, old.FloatY
	p.restoring = true
	m.DesktopPage = p
	m.DesktopInitialize(p)
}

func (m *Client) initializeResumedDesktop(p *MobileDesktopPage, s control.Session, displays []desktop.Display, windows []desktop.Window, err error) {
	p.restoring = false
	p.Busy = false
	if err != nil {
		m.desktopFail(p, tr("Connection incomplete"), err)
		return
	}
	p.Pickable, p.AppsAvailable = s.Pick, s.Apps
	p.frameStream = s.FrameStream
	p.DisplaysAvailable, p.Displays = s.Pick && s.Displays, displays
	p.Windows = windows
	m.DesktopInitPrivacy(p, s)
	switch {
	case p.resumePicker:
		switch p.PickerTab {
		case 1:
			m.DesktopChooseApps(p)
		case 2:
			m.DesktopPresentDisplays(p, displays)
		default:
			m.DesktopChooseWindows(p)
		}
	case p.WindowOnly && p.Window.ID != 0:
		for _, w := range windows {
			if w.ID == p.Window.ID && w.PID == p.Window.PID {
				m.DesktopSelectWindow(p, w)
				return
			}
		}
		m.DesktopFinish(p, tr("The permission window closed. Return to the conversation to view the result."))
	case p.Display.ID != 0:
		for _, d := range displays {
			if d.ID == p.Display.ID && d.UUID == p.Display.UUID {
				m.DesktopSelectDisplay(p, d)
				return
			}
		}
		m.DesktopPresentDisplays(p, displays)
		p.AppStatus = tr("The display changed or disconnected. Select a desktop.")
	default:
		if !s.WindowOnly {
			p.WindowOnly = false
			m.desktopStart(p)
		} else {
			m.DesktopSelectFull(p)
		}
	}
}
