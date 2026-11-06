package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/remote"
	"miya/internal/textutil"

	"github.com/egoist/mygo/ui"
)

type mobileDesktopApproval struct {
	ID      json.RawMessage
	Version string
}

// Only the service's resolution of this exact approval closes its control page.
// A disappearing window, wrong password or another approval cannot do so.
func (m *mobileApp) closeResolvedDesktopControl(approvals []codex.Approval) {
	ref := m.desktopApproval
	if len(ref.ID) == 0 {
		return
	}
	for _, a := range approvals {
		if sameJSON(a.ID, ref.ID) && approvalFingerprint(a) == ref.Version {
			return
		}
	}
	m.desktopApproval = mobileDesktopApproval{}
	m.desktopRequest = ""
	m.closeDesktopPage(true)
}

func (m *mobileApp) openRemoteDesktop() {
	m.openDesktopWindow(nil, desktop.Window{})
}

func (m *mobileApp) openDesktopWindow(approval *codex.Approval, window desktop.Window) {
	if !m.connected || m.conn == nil || m.open == 0 || m.desktopUIOpen() || m.desktopRequest != "" {
		return
	}
	if !m.state.DesktopControl {
		m.sendErr = tr("Update Miya on your Mac running macOS 14 or later, then wait for the background service to switch versions when idle")
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		m.sendErr = err.Error()
		return
	}
	id := hex.EncodeToString(raw[:])
	m.desktopRequest = id
	m.desktopApproval = mobileDesktopApproval{}
	conn, gen := m.conn, m.generation
	r := m.subscribeRequest(m.open)
	r.Op, r.RequestID, r.Have = "desktop-open", id, nil
	if approval != nil {
		r.WindowOnly, r.WindowID, r.WindowPID = true, window.ID, window.PID
		r.Approval, r.ApprovalVersion = approval.ID, approvalFingerprint(*approval)
		m.desktopApproval = mobileDesktopApproval{ID: approval.ID, Version: r.ApprovalVersion}
	} else {
		// The menu entry may choose any window or the full desktop.
		r.WindowPick = true
	}
	closing := m.desktopClosing
	m.desktopClosing = nil
	go func() {
		// Requests on one connection run in order: revoke earlier leases first.
		for _, token := range closing {
			_ = conn.Send(remote.Request{Op: "desktop-close", DesktopToken: token})
		}
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.desktopRequest == id {
					m.desktopRequest = ""
					m.desktopApproval = mobileDesktopApproval{}
					m.sendErr = err.Error()
				}
			})
		}
	}()
	time.AfterFunc(15*time.Second, func() {
		m.onMain(gen, func() {
			if m.desktopRequest == id {
				m.desktopRequest = ""
				m.desktopApproval = mobileDesktopApproval{}
				m.sendErr = tr("Remote desktop is not responding. Try again.")
			}
		})
	})
}

func (m *mobileApp) receiveDesktopControl(conn *remote.Conn, data json.RawMessage, message string) {
	var reply phoneDesktopReply
	if json.Unmarshal(data, &reply) != nil {
		return
	}
	if m.conn != conn || reply.RequestID != m.desktopRequest || m.desktopRequest == "" || m.desktopUIOpen() {
		if reply.Token != "" {
			go conn.Send(remote.Request{Op: "desktop-close", DesktopToken: reply.Token})
		}
		return
	}
	m.desktopRequest = ""
	if message != "" {
		m.desktopApproval = mobileDesktopApproval{}
		m.sendErr = message
		return
	}
	if len(reply.Token) != 64 {
		m.desktopApproval = mobileDesktopApproval{}
		m.sendErr = tr("Invalid remote desktop connection")
		return
	}
	m.openDesktopPage(conn, reply.Token)
}

// Human takeover is offered for every pending confirmation, including tools
// from external MCP servers. It never answers the original approval itself.
func (m *mobileApp) desktopControlAction(c *ui.Context, a codex.Approval) {
	if !m.state.DesktopControl || m.open == 0 {
		return
	}
	// Tool-use consent has no desktop target. Previewing the foreground app
	// there leaks unrelated content and can bury the actual approval controls.
	if a.Method != "miya/desktopControl" {
		return
	}
	if m.state.DesktopWindows {
		m.desktopWindowPreview(c, a)
		return
	}
	if m.pill(c, "remote-desktop", tr("Open remote desktop"), false).FillWidth().Disabled(!m.connected || m.desktopRequest != "" || m.desktopUIOpen()).Clicked() {
		m.openRemoteDesktop()
	}
}

type mobileWindowPreview struct {
	requestID     string
	version       string
	image         ui.ImageSource
	window        desktop.Window
	dialog        desktop.Dialog
	message       string
	loading       bool
	actionPending bool
	actionRequest string
	expanded      bool
	stale         bool
	imageHash     [32]byte
}

func (m *mobileApp) requestWindowPreview(a codex.Approval) {
	if !m.connected || m.conn == nil {
		return
	}
	id := string(a.ID)
	old := m.desktopPreviews[id]
	if old != nil && old.version == approvalFingerprint(a) && (old.loading || old.actionPending) {
		return
	}
	if m.desktopPreviews == nil {
		m.desktopPreviews = map[string]*mobileWindowPreview{}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return
	}
	p := &mobileWindowPreview{requestID: hex.EncodeToString(raw[:]), version: approvalFingerprint(a), loading: true}
	if old != nil && old.version == p.version {
		p.image, p.window, p.dialog = old.image, old.window, old.dialog
		p.expanded = old.expanded
		p.stale, p.imageHash = old.stale, old.imageHash
	}
	m.desktopPreviews[id] = p
	r := m.threadRequest("desktop-preview", m.open)
	r.RequestID, r.Approval, r.ApprovalVersion = p.requestID, a.ID, p.version
	r.WindowID, r.WindowPID = p.window.ID, p.window.PID
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				m.failWindowPreview(id, p, tr("Connection lost. Reconnect to retry the preview."))
			})
		}
	}()
	time.AfterFunc(12*time.Second, func() {
		m.onMain(gen, func() {
			if m.desktopPreviews[id] == p && p.loading {
				m.failWindowPreview(id, p, tr("Window preview unavailable. Try again."))
			}
		})
	})
}

// Keep the last frame and geometry when a refresh fails. Failures pause
// polling until an explicit retry, so the card never cycles between an image,
// an empty error state and a loading state.
func (m *mobileApp) failWindowPreview(id string, p *mobileWindowPreview, message string) {
	if m.desktopPreviews[id] != p {
		return
	}
	p.loading, p.stale, p.message = false, true, message
	invalidate()
}

func (m *mobileApp) receiveWindowPreview(conn *remote.Conn, p control.Preview, image ui.ImageSource, message string) {
	if m.conn != conn {
		return
	}
	current := m.desktopPreviews[string(p.Approval)]
	if current == nil || current.requestID != p.RequestID || !current.loading {
		return
	}
	if message != "" || image == nil {
		m.failWindowPreview(string(p.Approval), current, textutil.FirstNonEmpty(message, tr("Unable to load window preview. Try again.")))
		return
	}
	current.loading, current.stale, current.message = false, false, ""
	hash := sha256.Sum256(p.Data)
	if current.image == nil || hash != current.imageHash {
		current.image = image
		current.imageHash = hash
	}
	if p.Window.ID != 0 {
		current.window = p.Window
	}
	current.dialog = p.Dialog
	for _, a := range m.state.Approvals {
		if sameJSON(a.ID, p.Approval) {
			m.scheduleWindowPreview(a, current)
			break
		}
	}
	invalidate()
}

func (m *mobileApp) scheduleWindowPreview(a codex.Approval, p *mobileWindowPreview) {
	gen := m.generation
	time.AfterFunc(2*time.Second, func() {
		m.onMain(gen, func() {
			if m.desktopPreviews[string(a.ID)] != p || p.stale || !m.connected || m.router().Path() != "/thread" && m.router().Path() != "/approval" {
				return
			}
			th, _ := m.find(m.open)
			if th.ServerID != a.ThreadID {
				return
			}
			if m.desktopUIOpen() || p.actionPending {
				m.scheduleWindowPreview(a, p)
				return
			}
			m.requestWindowPreview(a)
		})
	})
}

func (m *mobileApp) pressWindowAction(a codex.Approval, p *mobileWindowPreview, action desktop.Action) {
	if p.stale || p.actionPending || !action.Enabled || m.conn == nil {
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return
	}
	p.actionPending = true
	p.actionRequest = hex.EncodeToString(raw[:])
	p.message = ""
	r := m.threadRequest("desktop-action", m.open)
	r.RequestID, r.Approval, r.ApprovalVersion = p.actionRequest, a.ID, approvalFingerprint(a)
	r.WindowID, r.WindowPID, r.DesktopAction = p.window.ID, p.window.PID, action.ID
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				m.receiveWindowAction(conn, control.Preview{RequestID: r.RequestID, Approval: a.ID}, err.Error())
			})
		}
	}()
	time.AfterFunc(10*time.Second, func() {
		m.onMain(gen, func() {
			if m.desktopPreviews[string(a.ID)] == p && p.actionPending {
				p.actionPending = false
				p.message = tr("Permission result unconfirmed. Check the window state first.")
				invalidate()
			}
		})
	})
}

func (m *mobileApp) receiveWindowAction(conn *remote.Conn, reply control.Preview, message string) {
	if m.conn != conn {
		return
	}
	p := m.desktopPreviews[string(reply.Approval)]
	if p == nil || p.actionRequest != reply.RequestID {
		return
	}
	p.actionPending = false
	p.message = message
	if message == "" {
		for _, a := range m.state.Approvals {
			if sameJSON(a.ID, reply.Approval) {
				m.requestWindowPreview(a)
				break
			}
		}
	}
	invalidate()
}

func (m *mobileApp) desktopWindowPreview(c *ui.Context, a codex.Approval) {
	id := string(a.ID)
	p := m.desktopPreviews[id]
	if m.connected && (p == nil || p.version != approvalFingerprint(a)) {
		m.requestWindowPreview(a)
		p = m.desktopPreviews[id]
	}
	if p == nil {
		ui.Text(c, a.Reason).FontSize(m.font(16)).FontWeight(600)
		ui.Text(c, tr("Connect to your computer to preview the permission window")).FontSize(m.font(13)).TextColor(m.pal.muted)
		return
	}
	blocked := !m.connected || m.desktopRequest != "" || m.desktopUIOpen()
	ui.Text(c, textutil.FirstNonEmpty(p.dialog.Title, a.Reason, p.window.Title)).FontSize(m.font(17)).FontWeight(600).LineHeight(1.4).MaxLines(2)
	if p.dialog.Message != "" {
		if p.expanded {
			ui.Scroll(c).FillWidth().MaxHeight(144).Children(func() { ui.Text(c, p.dialog.Message).FontSize(m.font(14)).LineHeight(1.45).Selectable() })
		} else {
			ui.Text(c, p.dialog.Message).FillWidth().FontSize(m.font(14)).TextColor(m.pal.muted).LineHeight(1.45).MaxLines(3)
		}
		if len([]rune(p.dialog.Message)) > 100 {
			label := tr("Full description")
			if p.expanded {
				label = tr("Collapse description")
			}
			if m.pill(c, "window-details", label, false).Clicked() {
				p.expanded = !p.expanded
			}
		}
	}
	if p.image != nil {
		button := m.phoneButton(c.Key("window-preview")).Column().FillWidth().Padding(8).Background(m.pal.bg).Label(tr("Control permission window")).Disabled(blocked)
		button.Children(func() {
			w, h := ui.ImageSize(p.image)
			ratio := float32(1.5)
			if w > 0 && h > 0 {
				ratio = w / h
			}
			ui.Image(c, p.image).FillWidth().AspectRatio(ratio).MaxHeight(260).Fit(ui.Contain)
		})
		if button.Clicked() {
			m.openDesktopWindow(&a, p.window)
		}
	} else {
		message := p.message
		if p.loading {
			message = tr("Opening permission window preview…")
		}
		ui.Column(c).FillWidth().Height(200).Padding(16).Background(m.pal.bg).Radius(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, message).FontSize(m.font(13)).TextColor(m.pal.muted).TextAlign(ui.Center).MaxLines(3)
		})
	}
	status := tr("Tap the preview to control the target window")
	needsInput := false
	for _, action := range p.dialog.Actions {
		if !action.Cancel && !action.Enabled {
			needsInput = true
			status = tr("Enter the required information before allowing access")
			break
		}
	}
	if p.stale {
		status = tr("Preview paused")
		if p.image != nil {
			status += tr(", showing the last image")
		}
	}
	ui.Text(c, status).FontSize(m.font(12)).TextColor(m.pal.muted).MaxLines(1)
	if len(p.dialog.Actions) > 0 {
		ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
			if needsInput {
				if m.approvalButton(c, "fill-authorization", tr("Enter permission details"), true, blocked || p.actionPending).Clicked() {
					m.openDesktopWindow(&a, p.window)
				}
			}
			for _, action := range p.dialog.Actions {
				primary := action.Default && !action.Cancel
				if !action.Cancel && !action.Enabled {
					continue // Offer the input entry until the native action is enabled.
				}
				if m.approvalButton(c, action.ID, action.Label, primary, blocked || p.stale || !action.Enabled || p.actionPending).Clicked() {
					m.pressWindowAction(a, p, action)
				}
			}
		})
	}
	if p.stale {
		if p.image != nil {
			ui.Text(c, p.message).FontSize(m.font(13)).TextColor(m.pal.muted).MinHeight(38).MaxLines(2)
		}
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			if m.pill(c, "retry-preview", tr("Retry preview"), false).Grow(1).Disabled(blocked || p.loading).Clicked() {
				m.requestWindowPreview(a)
			}
			if m.pill(c, "open-window", tr("Open permission window"), false).Grow(1).Disabled(blocked).Clicked() {
				m.openDesktopWindow(&a, p.window)
			}
		})
	}
}
