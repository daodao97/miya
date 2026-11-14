package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/interaction"
	"miya/internal/phone/desktopview"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
)

type MobileDesktopApproval struct {
	ID      json.RawMessage
	Version string
}

// Only the service's resolution of this exact approval closes its control page.
// A disappearing window, wrong password or another approval cannot do so.
func (m *Client) CloseResolvedDesktopControl(approvals []interaction.Request) {
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
	m.stopDesktopOpen()
	m.CloseDesktopPage(true)
}

func (m *Client) OpenRemoteDesktop() {
	// A floating session returns to its page rather than opening another.
	if p := m.DesktopPage; p != nil && p.Floating && p.Phase != "ended" {
		m.DesktopRestore(p)
		return
	}
	m.OpenDesktopWindow(nil, desktop.Window{})
}

func (m *Client) OpenDesktopWindow(approval *interaction.Request, window desktop.Window) {
	// Only one lease at a time: an approval takeover replaces a floating
	// session, and an ended floating window just goes away.
	if p := m.DesktopPage; p != nil && p.Floating && (approval != nil || p.Phase == "ended") {
		m.CloseDesktopPage(false)
	}
	// An approval takeover needs its conversation; the full desktop opened
	// from the menu does not, so a new conversation can open it too.
	if !m.Connected || !m.CanSend() || (approval != nil && m.Open == 0) || m.desktopUIOpen() || m.DesktopRequest != "" {
		return
	}
	if !m.State.DesktopControl {
		m.SendErr = tr("Update Miya on your Mac running macOS 14 or later, then wait for the background service to switch versions when idle")
		return
	}
	m.DesktopApproval = MobileDesktopApproval{}
	r := m.SubscribeRequest(m.Open)
	r.Op, r.Have = "desktop-open", nil
	if approval != nil {
		r.WindowOnly, r.WindowID, r.WindowPID = true, window.ID, window.PID
		r.Approval, r.ApprovalVersion = approval.ID, phoneproto.ApprovalFingerprint(*approval)
		m.DesktopApproval = MobileDesktopApproval{ID: approval.ID, Version: r.ApprovalVersion}
	} else {
		r.WindowPick = true
	}
	m.sendDesktopOpen(r)
}

func (m *Client) sendDesktopOpen(r remote.Request) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		m.desktopOpenFailed(err.Error())
		return
	}
	id := hex.EncodeToString(raw[:])
	r.RequestID = id
	m.DesktopRequest, m.desktopOpening = id, r
	conn, gen := m.Conn, m.Generation
	send := m.write

	closing := m.DesktopClosing
	m.DesktopClosing = nil
	go func() {
		// Requests on one connection run in order: revoke earlier leases first.
		for _, token := range closing {
			_ = send(conn, remote.Request{Op: "desktop-close", DesktopToken: token})
		}
		if err := send(conn, r); err != nil {
			m.onMain(gen, func() {
				if m.DesktopRequest == id {
					m.desktopOpenFailed(err.Error())
				}
			})
		}
	}()
	m.desktopOpenTimer = time.AfterFunc(15*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopRequest == id {
				m.desktopOpenFailed(tr("Remote desktop is not responding. Try again."))
			}
		})
	})
}

func (m *Client) receiveDesktopControl(conn *remote.Conn, data json.RawMessage, Message string) {
	var reply phoneproto.DesktopReply
	if json.Unmarshal(data, &reply) != nil {
		return
	}
	resuming := m.DesktopPage != nil && m.DesktopPage.suspended && m.DesktopPage.resuming
	if m.Conn != conn || reply.RequestID != m.DesktopRequest || m.DesktopRequest == "" || (m.desktopUIOpen() && !resuming) {
		if reply.Token != "" {
			m.sendDesktopClose(conn, reply.Token)
		}
		return
	}
	m.stopDesktopOpen()
	if Message != "" {
		m.desktopOpenFailed(Message)
		return
	}
	if len(reply.Token) != 64 {
		m.desktopOpenFailed(tr("Invalid remote desktop connection"))
		return
	}
	if resuming {
		m.reopenDesktopPage(conn, reply.Token)
	} else {
		m.openDesktopPage(conn, reply.Token)
	}
}

func (m *Client) stopDesktopOpen() {
	if m.desktopOpenTimer != nil {
		m.desktopOpenTimer.Stop()
		m.desktopOpenTimer = nil
	}
	m.DesktopRequest = ""
}

func (m *Client) desktopOpenFailed(Message string) {
	m.stopDesktopOpen()
	if p := m.DesktopPage; p != nil && p.suspended {
		p.resuming, p.resumeBlocked = false, true
		m.DesktopStage(p, "paused", tr("Connection incomplete"), Message, false)
	} else {
		m.DesktopApproval = MobileDesktopApproval{}
		m.SendErr = Message
	}
}

type MobileWindowPreview struct {
	RequestID     string
	Version       string
	Image         desktopview.ImageSource
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

func (m *Client) RequestWindowPreview(a interaction.Request) {
	if !m.Connected || !m.CanSend() {
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
		if err := m.write(conn, r); err != nil {
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
func (m *Client) FailWindowPreview(id string, p *MobileWindowPreview, Message string) {
	if m.DesktopPreviews[id] != p {
		return
	}
	p.Loading, p.Stale, p.Message = false, true, Message
	m.change()
}

func (m *Client) ReceiveWindowPreview(conn *remote.Conn, p control.Preview, image desktopview.ImageSource, Message string) {
	if m.Conn != conn {
		return
	}
	current := m.DesktopPreviews[string(p.Approval)]
	if current == nil || current.RequestID != p.RequestID || !current.Loading {
		return
	}
	if Message != "" || image == nil {
		m.FailWindowPreview(string(p.Approval), current, textutil.FirstNonEmpty(Message, tr("Unable to load window preview. Try again.")))
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
	m.change()
}

func (m *Client) scheduleWindowPreview(a interaction.Request, p *MobileWindowPreview) {
	gen := m.Generation
	time.AfterFunc(2*time.Second, func() {
		m.onMain(gen, func() {
			if m.DesktopPreviews[string(a.ID)] != p || p.Stale || !m.Connected || m.Path != "/thread" && m.Path != "/approval" {
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
			m.RequestWindowPreview(a)
		})
	})
}

func (m *Client) PressWindowAction(a interaction.Request, p *MobileWindowPreview, action desktop.Action) {
	if p.Stale || p.ActionPending || !action.Enabled || !m.CanSend() {
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
		if err := m.write(conn, r); err != nil {
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
				m.change()
			}
		})
	})
}

func (m *Client) receiveWindowAction(conn *remote.Conn, reply control.Preview, Message string) {
	if m.Conn != conn {
		return
	}
	p := m.DesktopPreviews[string(reply.Approval)]
	if p == nil || p.actionRequest != reply.RequestID {
		return
	}
	p.ActionPending = false
	p.Message = Message
	if Message == "" {
		for _, a := range m.State.Approvals {
			if textutil.SameJSON(a.ID, reply.Approval) {
				m.RequestWindowPreview(a)
				break
			}
		}
	}
	m.change()
}

func DesktopApprovalVersion(a interaction.Request) string { return phoneproto.ApprovalFingerprint(a) }
