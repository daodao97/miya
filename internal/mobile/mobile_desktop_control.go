package mobile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/interaction"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
)

type MobileDesktopApproval struct {
	ID      json.RawMessage
	Version string
}

// Only the service's resolution of this exact approval closes its control page.
// A disappearing window, wrong password or another approval cannot do so.
func (m *App) CloseResolvedDesktopControl(approvals []interaction.Request) {
	ref := m.DesktopApproval
	if len(ref.ID) == 0 {
		return
	}
	for _, a := range approvals {
		if textutil.SameJSON(a.ID, ref.ID) && phoneproto.ApprovalFingerprint(a) == ref.Version {
			return
		}
	}
	m.DesktopApproval = MobileDesktopApproval{}
	m.DesktopRequest = ""
	m.CloseDesktopPage(true)
}

func (m *App) openRemoteDesktop() {
	m.openDesktopWindow(nil, desktop.Window{})
}

func (m *App) openDesktopWindow(approval *interaction.Request, window desktop.Window) {
	if !m.Connected || m.Conn == nil || m.Open == 0 || m.desktopUIOpen() || m.DesktopRequest != "" {
		return
	}
	if !m.State.DesktopControl {
		m.SendErr = tr("Update Miya on your Mac running macOS 14 or later, then wait for the background service to switch versions when idle")
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		m.SendErr = err.Error()
		return
	}
	id := hex.EncodeToString(raw[:])
	m.DesktopRequest = id
	m.DesktopApproval = MobileDesktopApproval{}
	conn, gen := m.Conn, m.Generation
	r := m.SubscribeRequest(m.Open)
	r.Op, r.RequestID, r.Have = "desktop-open", id, nil
	if approval != nil {
		r.WindowOnly, r.WindowID, r.WindowPID = true, window.ID, window.PID
		r.Approval, r.ApprovalVersion = approval.ID, phoneproto.ApprovalFingerprint(*approval)
		m.DesktopApproval = MobileDesktopApproval{ID: approval.ID, Version: r.ApprovalVersion}
	} else {
		// The menu entry may choose any window or the full desktop.
		r.WindowPick = true
	}
	closing := m.DesktopClosing
	m.DesktopClosing = nil
	go func() {
		// Requests on one connection run in order: revoke earlier leases first.
		for _, token := range closing {
			_ = conn.Send(remote.Request{Op: "desktop-close", DesktopToken: token})
		}
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.DesktopRequest == id {
					m.DesktopRequest = ""
					m.DesktopApproval = MobileDesktopApproval{}
					m.SendErr = err.Error()
				}
			})
		}
	}()
	time.AfterFunc(15*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopRequest == id {
				m.DesktopRequest = ""
				m.DesktopApproval = MobileDesktopApproval{}
				m.SendErr = tr("Remote desktop is not responding. Try again.")
			}
		})
	})
}

func (m *App) receiveDesktopControl(conn *remote.Conn, data json.RawMessage, message string) {
	var reply phoneproto.DesktopReply
	if json.Unmarshal(data, &reply) != nil {
		return
	}
	if m.Conn != conn || reply.RequestID != m.DesktopRequest || m.DesktopRequest == "" || m.desktopUIOpen() {
		if reply.Token != "" {
			go conn.Send(remote.Request{Op: "desktop-close", DesktopToken: reply.Token})
		}
		return
	}
	m.DesktopRequest = ""
	if message != "" {
		m.DesktopApproval = MobileDesktopApproval{}
		m.SendErr = message
		return
	}
	if len(reply.Token) != 64 {
		m.DesktopApproval = MobileDesktopApproval{}
		m.SendErr = tr("Invalid remote desktop connection")
		return
	}
	m.openDesktopPage(conn, reply.Token)
}

// Human takeover is offered for every pending confirmation, including tools
// from external MCP servers. It never answers the original approval itself.
func (m *App) desktopControlAction(c *ui.Context, a interaction.Request) {
	if !m.State.DesktopControl || m.Open == 0 {
		return
	}
	// Tool-use consent has no desktop target. Previewing the foreground app
	// there leaks unrelated content and can bury the actual approval controls.
	if a.Kind() != interaction.DesktopControl {
		return
	}
	if m.State.DesktopWindows {
		m.desktopWindowPreview(c, a)
		return
	}
	if m.pill(c, "remote-desktop", tr("Open remote desktop"), false).FillWidth().Disabled(!m.Connected || m.DesktopRequest != "" || m.desktopUIOpen()).Clicked() {
		m.openRemoteDesktop()
	}
}

type MobileWindowPreview struct {
	RequestID     string
	Version       string
	Image         ui.ImageSource
	Window        desktop.Window
	Dialog        desktop.Dialog
	Message       string
	Loading       bool
	ActionPending bool
	actionRequest string
	expanded      bool
	Stale         bool
	ImageHash     [32]byte
}

func (m *App) requestWindowPreview(a interaction.Request) {
	if !m.Connected || m.Conn == nil {
		return
	}
	id := string(a.ID)
	old := m.DesktopPreviews[id]
	if old != nil && old.Version == phoneproto.ApprovalFingerprint(a) && (old.Loading || old.ActionPending) {
		return
	}
	if m.DesktopPreviews == nil {
		m.DesktopPreviews = map[string]*MobileWindowPreview{}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return
	}
	p := &MobileWindowPreview{RequestID: hex.EncodeToString(raw[:]), Version: phoneproto.ApprovalFingerprint(a), Loading: true}
	if old != nil && old.Version == p.Version {
		p.Image, p.Window, p.Dialog = old.Image, old.Window, old.Dialog
		p.expanded = old.expanded
		p.Stale, p.ImageHash = old.Stale, old.ImageHash
	}
	m.DesktopPreviews[id] = p
	r := m.ThreadRequest("desktop-preview", m.Open)
	r.RequestID, r.Approval, r.ApprovalVersion = p.RequestID, a.ID, p.Version
	r.WindowID, r.WindowPID = p.Window.ID, p.Window.PID
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				m.FailWindowPreview(id, p, tr("Connection lost. Reconnect to retry the preview."))
			})
		}
	}()
	time.AfterFunc(12*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopPreviews[id] == p && p.Loading {
				m.FailWindowPreview(id, p, tr("Window preview unavailable. Try again."))
			}
		})
	})
}

// Keep the last frame and geometry when a refresh fails. Failures pause
// polling until an explicit retry, so the card never cycles between an image,
// an empty error state and a loading state.
func (m *App) FailWindowPreview(id string, p *MobileWindowPreview, message string) {
	if m.DesktopPreviews[id] != p {
		return
	}
	p.Loading, p.Stale, p.Message = false, true, message
	uiruntime.Invalidate()
}

func (m *App) ReceiveWindowPreview(conn *remote.Conn, p control.Preview, image ui.ImageSource, message string) {
	if m.Conn != conn {
		return
	}
	current := m.DesktopPreviews[string(p.Approval)]
	if current == nil || current.RequestID != p.RequestID || !current.Loading {
		return
	}
	if message != "" || image == nil {
		m.FailWindowPreview(string(p.Approval), current, textutil.FirstNonEmpty(message, tr("Unable to load window preview. Try again.")))
		return
	}
	current.Loading, current.Stale, current.Message = false, false, ""
	hash := sha256.Sum256(p.Data)
	if current.Image == nil || hash != current.ImageHash {
		current.Image = image
		current.ImageHash = hash
	}
	if p.Window.ID != 0 {
		current.Window = p.Window
	}
	current.Dialog = p.Dialog
	for _, a := range m.State.Approvals {
		if textutil.SameJSON(a.ID, p.Approval) {
			m.scheduleWindowPreview(a, current)
			break
		}
	}
	uiruntime.Invalidate()
}

func (m *App) scheduleWindowPreview(a interaction.Request, p *MobileWindowPreview) {
	gen := m.Generation
	time.AfterFunc(2*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopPreviews[string(a.ID)] != p || p.Stale || !m.Connected || m.Router().Path() != "/thread" && m.Router().Path() != "/approval" {
				return
			}
			th, _ := m.find(m.Open)
			if th.ServerID != a.ThreadID {
				return
			}
			if m.desktopUIOpen() || p.ActionPending {
				m.scheduleWindowPreview(a, p)
				return
			}
			m.requestWindowPreview(a)
		})
	})
}

func (m *App) pressWindowAction(a interaction.Request, p *MobileWindowPreview, action desktop.Action) {
	if p.Stale || p.ActionPending || !action.Enabled || m.Conn == nil {
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return
	}
	p.ActionPending = true
	p.actionRequest = hex.EncodeToString(raw[:])
	p.Message = ""
	r := m.ThreadRequest("desktop-action", m.Open)
	r.RequestID, r.Approval, r.ApprovalVersion = p.actionRequest, a.ID, phoneproto.ApprovalFingerprint(a)
	r.WindowID, r.WindowPID, r.DesktopAction = p.Window.ID, p.Window.PID, action.ID
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				m.receiveWindowAction(conn, control.Preview{RequestID: r.RequestID, Approval: a.ID}, err.Error())
			})
		}
	}()
	time.AfterFunc(10*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopPreviews[string(a.ID)] == p && p.ActionPending {
				p.ActionPending = false
				p.Message = tr("Permission result unconfirmed. Check the window state first.")
				uiruntime.Invalidate()
			}
		})
	})
}

func (m *App) receiveWindowAction(conn *remote.Conn, reply control.Preview, message string) {
	if m.Conn != conn {
		return
	}
	p := m.DesktopPreviews[string(reply.Approval)]
	if p == nil || p.actionRequest != reply.RequestID {
		return
	}
	p.ActionPending = false
	p.Message = message
	if message == "" {
		for _, a := range m.State.Approvals {
			if textutil.SameJSON(a.ID, reply.Approval) {
				m.requestWindowPreview(a)
				break
			}
		}
	}
	uiruntime.Invalidate()
}

func (m *App) desktopWindowPreview(c *ui.Context, a interaction.Request) {
	id := string(a.ID)
	p := m.DesktopPreviews[id]
	if m.Connected && (p == nil || p.Version != phoneproto.ApprovalFingerprint(a)) {
		m.requestWindowPreview(a)
		p = m.DesktopPreviews[id]
	}
	if p == nil {
		ui.Text(c, a.Reason).FontSize(m.Font(16)).FontWeight(600)
		ui.Text(c, tr("Connect to your computer to preview the permission window")).FontSize(m.Font(13)).TextColor(m.Pal.muted)
		return
	}
	blocked := !m.Connected || m.DesktopRequest != "" || m.desktopUIOpen()
	ui.Text(c, textutil.FirstNonEmpty(p.Dialog.Title, a.Reason, p.Window.Title)).FontSize(m.Font(17)).FontWeight(600).LineHeight(1.4).MaxLines(2)
	if p.Dialog.Message != "" {
		if p.expanded {
			ui.Scroll(c).FillWidth().MaxHeight(144).Children(func() { ui.Text(c, p.Dialog.Message).FontSize(m.Font(14)).LineHeight(1.45).Selectable() })
		} else {
			ui.Text(c, p.Dialog.Message).FillWidth().FontSize(m.Font(14)).TextColor(m.Pal.muted).LineHeight(1.45).MaxLines(3)
		}
		if len([]rune(p.Dialog.Message)) > 100 {
			label := tr("Full description")
			if p.expanded {
				label = tr("Collapse description")
			}
			if m.pill(c, "window-details", label, false).Clicked() {
				p.expanded = !p.expanded
			}
		}
	}
	if p.Image != nil {
		button := m.phoneButton(c.Key("window-preview")).Column().FillWidth().Padding(8).Background(m.Pal.Bg).Label(tr("Control permission window")).Disabled(blocked)
		button.Children(func() {
			w, h := ui.ImageSize(p.Image)
			ratio := float32(1.5)
			if w > 0 && h > 0 {
				ratio = w / h
			}
			ui.Image(c, p.Image).FillWidth().AspectRatio(ratio).MaxHeight(260).Fit(ui.Contain)
		})
		if button.Clicked() {
			m.openDesktopWindow(&a, p.Window)
		}
	} else {
		message := p.Message
		if p.Loading {
			message = tr("Opening permission window preview…")
		}
		ui.Column(c).FillWidth().Height(200).Padding(16).Background(m.Pal.Bg).Radius(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, message).FontSize(m.Font(13)).TextColor(m.Pal.muted).TextAlign(ui.Center).MaxLines(3)
		})
	}
	status := tr("Tap the preview to control the target window")
	needsInput := false
	for _, action := range p.Dialog.Actions {
		if !action.Cancel && !action.Enabled {
			needsInput = true
			status = tr("Enter the required information before allowing access")
			break
		}
	}
	if p.Stale {
		status = tr("Preview paused")
		if p.Image != nil {
			status += tr(", showing the last image")
		}
	}
	ui.Text(c, status).FontSize(m.Font(12)).TextColor(m.Pal.muted).MaxLines(1)
	if len(p.Dialog.Actions) > 0 {
		ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
			if needsInput {
				if m.ApprovalButton(c, "fill-authorization", tr("Enter permission details"), true, blocked || p.ActionPending).Clicked() {
					m.openDesktopWindow(&a, p.Window)
				}
			}
			for _, action := range p.Dialog.Actions {
				primary := action.Default && !action.Cancel
				if !action.Cancel && !action.Enabled {
					continue // Offer the input entry until the native action is enabled.
				}
				if m.ApprovalButton(c, action.ID, action.Label, primary, blocked || p.Stale || !action.Enabled || p.ActionPending).Clicked() {
					m.pressWindowAction(a, p, action)
				}
			}
		})
	}
	if p.Stale {
		if p.Image != nil {
			ui.Text(c, p.Message).FontSize(m.Font(13)).TextColor(m.Pal.muted).MinHeight(38).MaxLines(2)
		}
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			if m.pill(c, "retry-preview", tr("Retry preview"), false).Grow(1).Disabled(blocked || p.Loading).Clicked() {
				m.requestWindowPreview(a)
			}
			if m.pill(c, "open-window", tr("Open permission window"), false).Grow(1).Disabled(blocked).Clicked() {
				m.openDesktopWindow(&a, p.Window)
			}
		})
	}
}
