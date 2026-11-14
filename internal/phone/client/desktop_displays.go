package client

import (
	"context"
	"fmt"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/textutil"
)

func (p *MobileDesktopPage) InvalidateTarget() {
	p.targetRevision++
	p.Active, p.Image, p.Queue, p.ActionsFresh, p.More = false, nil, nil, false, false
	p.Polling, p.Sending, p.actionsBusy = false, false, false
	p.Actions = control.Actions{}
	p.actionsUnsupported, p.ActionPending = false, false
	p.ThumbBusy = false
	p.Mapping.Reset()
}

func desktopDisplayTitle(display desktop.Display, number int) string {
	title := fmt.Sprintf(tr("Desktop %d"), number)
	title += " · " + textutil.FirstNonEmpty(display.Name, tr("Unnamed display"))
	return title
}

func (p *MobileDesktopPage) displayTitle() string {
	for i, display := range p.Displays {
		if display.ID == p.Display.ID && display.UUID == p.Display.UUID {
			return desktopDisplayTitle(p.Display, i+1)
		}
	}
	return textutil.FirstNonEmpty(p.Display.Name, tr("Full desktop"))
}

func (m *Client) DesktopPresentDisplays(p *MobileDesktopPage, displays []desktop.Display) {
	p.InvalidateTarget()
	p.Displays, p.PickerTab, p.AppStatus = displays, 2, ""
	m.DesktopStage(p, "picker", tr("Full desktop"), "", false)
}

func (m *Client) DesktopChooseDisplays(p *MobileDesktopPage) {
	if !p.Pickable || !p.DisplaysAvailable {
		return
	}
	p.InvalidateTarget()
	p.PickerTab, p.AppStatus = 2, ""
	m.DesktopStage(p, "picker", tr("Full desktop"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		displays, err := p.Client.Displays(ctx)
		return func() {
			p.Busy = false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.AppStatus = tr("Unable to load desktops") + ": " + err.Error()
				return
			}
			p.Displays = displays
		}
	})
}

func (m *Client) DesktopSelectDisplay(p *MobileDesktopPage, display desktop.Display) {
	if !p.Pickable || !p.DisplaysAvailable || display.ID == 0 {
		return
	}
	same := !p.WindowOnly && p.Display.ID == display.ID && p.Display.UUID == display.UUID
	p.InvalidateTarget()
	m.DesktopStage(p, "connecting", tr("Opening desktop"), display.Name, true)
	m.desktopDo(p, func(ctx context.Context) func() {
		// An earlier capture may still occupy the server's capture path.
		var selected desktop.Display
		var err error
		for deadline := time.Now().Add(6 * time.Second); ; {
			selected, err = p.Client.SelectDisplay(ctx, display)
			if control.Status(err) != 429 || !time.Now().Before(deadline) || !control.Wait(ctx, 250*time.Millisecond) {
				break
			}
		}
		return func() {
			if err != nil {
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				m.DesktopPresentDisplays(p, p.Displays)
				p.AppStatus = err.Error()
				return
			}
			p.Display, p.Window, p.WindowOnly = selected, desktop.Window{}, false
			if !same {
				p.Zoom, p.PanX, p.panY = 1, 0, 0
			}
			p.Actions, p.ActionsFresh = control.Actions{}, false
			m.desktopStart(p)
		}
	})
}
