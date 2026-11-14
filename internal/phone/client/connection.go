package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/chat/model"
	"miya/internal/desktop/control"
	"miya/internal/notification"
	"miya/internal/phone/desktopview"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/thumbnail"
)

func (m *Client) Run(ctx context.Context, gen int, link string, wake <-chan struct{}) {
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			m.onMain(gen, func() {
				m.RetryAt, m.RetryError = time.Time{}, ""
				m.Status = tr("Reconnecting to desktop…")
			})
		}
		dial := m.DialDesktop
		if dial == nil {
			dial = remote.Connect
		}
		var conn *remote.Conn
		var err error
		if m.PrepareNetwork != nil {
			err = m.PrepareNetwork(ctx, remote.RelayURL(link))
		}
		if err == nil {
			conn, err = dial(ctx, link)
		}
		if err == nil {
			if ctx.Err() != nil {
				conn.Close()
				return
			}
			stopClose := context.AfterFunc(ctx, func() { conn.Close() })
			var greeted bool
			greeted, err = m.read(gen, conn)
			stopClose()
			conn.Close()
			if greeted {
				attempt = 0
			}
		}
		if ctx.Err() != nil {
			return
		}
		delay := min(time.Second<<min(attempt, 4), 15*time.Second)
		if errors.Is(err, ErrIncompatible) {
			delay = 15 * time.Second
		}
		message := fmt.Sprintf(tr("Disconnected. Retrying in %d seconds."), int(delay.Seconds()))
		if err != nil {
			message += ": " + err.Error()
		}
		retryAt := time.Now().Add(delay)
		retryError := ""
		if err != nil {
			retryError = err.Error()
		}
		m.onMain(gen, func() {

			m.interruptFeatureRequests()
			m.interruptApprovals()
			m.suspendDesktopPage()
			m.stopDesktopOpen()
			m.DesktopPreviews = nil
			m.Conn, m.Connected, m.Status = nil, false, message

			m.RetryAt, m.RetryError = retryAt, retryError
			if m.Sending {
				m.Sending, m.SendErr = false, tr("Connection lost. The message may not have been sent.")
				m.failPendingSend()
			}
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			// Someone needs the desktop now: a send, or a network that came back.
			timer.Stop()
		}
	}
}

func (m *Client) read(gen int, conn *remote.Conn) (greeted bool, err error) {
	for {
		// The desktop sends at least a ping every 15 seconds.
		conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		var f remote.Frame
		if err := conn.Receive(&f); err != nil {
			return greeted, err
		}
		switch f.Type {
		case "hello":
			if f.Version != remote.ProtocolVersion {
				return greeted, ErrIncompatible
			}
			greeted = true
			if err := conn.EnableAttachments(f.AttachmentToken); err != nil {
				return greeted, err
			}
			conn.DecompressReceives()
			host := f.Host
			m.onMain(gen, func() {
				m.Host, m.Connected, m.Status, m.Error = host, true, "", ""
				item, _ := m.Desktops.Remember(m.Link, host)
				m.Desktops.Active = item.ID
				m.SaveDesktops()
				m.RetryAt, m.RetryError = time.Time{}, ""
				m.Conn = conn
				m.CompletionRead = notification.DesktopRead{}
				m.CompletionReceived = notification.DesktopRead{}
				m.PreviewPaths, m.PreviewRetries = nil, nil
				if m.Open != 0 {
					r := m.SubscribeRequest(m.Open)
					go m.write(conn, r)
				}
				m.BadgeRevision = 0
			})
		case "action":
			message := f.Error
			m.onMain(gen, func() {
				if message != "" {
					m.SendErr = message
				}
			})
		case "push":
			message := f.Error
			m.onMain(gen, func() {
				if m.OnPushResult != nil {
					m.OnPushResult(message)
				}
				if message == "" {
					m.readPending = nil
					m.markOpenRead()
				}
			})
		case "notice":
			var n notification.LiveMessage
			if json.Unmarshal(f.Data, &n) == nil {
				m.onMain(gen, func() {
					if m.OnLiveNotification != nil {
						m.OnLiveNotification(n)
					}
				})
			}
		case "read":
			var n notification.PhoneState
			if json.Unmarshal(f.Data, &n) == nil {
				m.onMain(gen, func() { m.ApplyNotifications(n) })
			}
		case "state":
			var s phoneproto.State
			if json.Unmarshal(f.Data, &s) == nil {
				m.onMain(gen, func() {
					m.ApplyPhoneState(s)
				})
			}
		case "thread":
			var d phoneproto.ThreadDelta
			if json.Unmarshal(f.Data, &d) == nil {
				id := f.Thread
				m.onMain(gen, func() {
					m.ReceiveThread(id, d)
				})
			}
		case "image":
			var preview thumbnail.Image
			if json.Unmarshal(f.Data, &preview) == nil {
				if f.Error != "" {
					m.onMain(gen, func() { m.RetryImage(preview.Source, conn) })
					continue
				}
				if f.Attachment != "" {
					id := f.Attachment
					go func() {
						data, err := conn.ReadAttachment(conn.Context(), id)
						if err != nil {
							m.onMain(gen, func() { m.RetryImage(preview.Source, conn) })
							return
						}
						preview.Data = data
						m.onMain(gen, func() {
							if m.Conn == conn {
								m.ReceiveImage(preview)
							}
						})
					}()
				} else {
					m.onMain(gen, func() { m.ReceiveImage(preview) })
				}
			}
		case "commands":
			var pc phoneproto.Commands
			if json.Unmarshal(f.Data, &pc) == nil {
				m.onMain(gen, func() {
					if m.Commands == nil {
						m.Commands = map[string][]model.ComposerChoice{}
					}
					if pc.Commands == nil {
						pc.Commands = []model.ComposerChoice{}
					}
					if pc.Agent == "" {
						pc.Agent = agentmeta.LegacyCommandAgent()
					}
					m.Commands[agent.CommandKey(pc.Agent, pc.Project)] = pc.Commands
				})
			}
		case "responded":
			var result phoneproto.ApprovalReply
			if json.Unmarshal(f.Data, &result) == nil {
				m.onMain(gen, func() {
					if m.Conn == conn {
						m.ApprovalAnswered(result.ID, f.Error)
					}
				})
			}
		case "memory", "automations", "automation-webhook", "automation-run", "automation-draft", "automation-create", "collaboration":
			m.onMain(gen, func() {
				if m.Conn == conn {
					m.ReceiveFeatureFrame(f)
				}
			})
		case "desktop-open":
			data, message := f.Data, f.Error
			m.onMain(gen, func() { m.receiveDesktopControl(conn, data, message) })
		case "desktop-preview":
			var p control.Preview
			if json.Unmarshal(f.Data, &p) == nil {
				image, _ := desktopview.DecodeImage(p.Data)
				message := f.Error
				m.onMain(gen, func() { m.ReceiveWindowPreview(conn, p, image, message) })
			}
		case "desktop-action":
			var p control.Preview
			if json.Unmarshal(f.Data, &p) == nil {
				message := f.Error
				m.onMain(gen, func() { m.receiveWindowAction(conn, p, message) })
			}
		case "sent":
			id, msg := f.Thread, f.Error
			m.onMain(gen, func() { m.Sent(id, msg) })
		}
	}
}
