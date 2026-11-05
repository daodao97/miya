package main

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
	"miya/internal/textutil"
)

func (p *mobileDesktopPage) invalidateTarget() {
	p.targetRevision++
	p.active, p.image, p.queue, p.actionsFresh, p.more = false, nil, nil, false, false
	p.polling, p.sending, p.actionsBusy = false, false, false
	p.pointer = desktopPointer{}
	p.lastTap, p.taps = time.Time{}, nil
	p.scrollX, p.scrollY = 0, 0
}

func desktopDisplayTitle(display desktop.Display, number int) string {
	title := fmt.Sprintf(tr("Desktop %d"), number)
	title += " · " + textutil.FirstNonEmpty(display.Name, tr("Unnamed display"))
	return title
}

func (p *mobileDesktopPage) displayTitle() string {
	for i, display := range p.displays {
		if display.ID == p.display.ID && display.UUID == p.display.UUID {
			return desktopDisplayTitle(p.display, i+1)
		}
	}
	return textutil.FirstNonEmpty(p.display.Name, tr("Full desktop"))
}

func (m *mobileApp) desktopPresentDisplays(p *mobileDesktopPage, displays []desktop.Display) {
	p.invalidateTarget()
	p.displays, p.pickerTab, p.appStatus = displays, 2, ""
	m.desktopStage(p, "picker", tr("Full desktop"), "", false)
}

func (m *mobileApp) desktopChooseDisplays(p *mobileDesktopPage) {
	if !p.pickable || !p.displaysAvailable {
		return
	}
	p.invalidateTarget()
	p.pickerTab, p.appStatus = 2, ""
	m.desktopStage(p, "picker", tr("Full desktop"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		displays, err := p.client.displays(ctx)
		return func() {
			p.busy = false
			if desktopStatus(err) == 410 {
				m.desktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.appStatus = tr("Unable to load desktops") + ": " + err.Error()
				return
			}
			p.displays = displays
		}
	})
}

func (m *mobileApp) desktopSelectDisplay(p *mobileDesktopPage, display desktop.Display) {
	if !p.pickable || !p.displaysAvailable || display.ID == 0 {
		return
	}
	p.invalidateTarget()
	m.desktopStage(p, "connecting", tr("Opening desktop"), display.Name, true)
	m.desktopDo(p, func(ctx context.Context) func() {
		// An earlier capture may still occupy the server's capture path.
		var selected desktop.Display
		var err error
		for deadline := time.Now().Add(6 * time.Second); ; {
			selected, err = p.client.selectDisplay(ctx, display)
			if desktopStatus(err) != 429 || !time.Now().Before(deadline) || !desktopWait(ctx, 250*time.Millisecond) {
				break
			}
		}
		return func() {
			if err != nil {
				if desktopStatus(err) == 410 {
					m.desktopFinish(p, err.Error())
					return
				}
				m.desktopPresentDisplays(p, p.displays)
				p.appStatus = err.Error()
				return
			}
			p.display, p.window, p.windowOnly = selected, desktop.Window{}, false
			p.zoom, p.panX, p.panY = 1, 0, 0
			p.actions, p.actionsFresh = desktopActions{}, false
			m.desktopStart(p)
		}
	})
}

func (m *mobileApp) desktopDisplayList(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	if p.appStatus != "" {
		ui.Text(c, p.appStatus).FontSize(m.font(14)).TextColor(pal.muted).LineHeight(1.5)
	}
	if p.busy {
		ui.Spinner(c).Margin(12)
		return
	}
	if len(p.displays) == 0 {
		ui.Text(c, tr("No displays available. Wake your Mac and refresh the list.")).FontSize(m.font(14)).TextColor(pal.muted)
		return
	}
	for i, display := range p.displays {
		title := desktopDisplayTitle(display, i+1)
		button := m.phoneButton(c.Key("desktop-display-" + display.UUID)).FillWidth().Padding(14).Gap(12).Background(pal.surface).Radius(14).Label(title)
		button.Children(func() {
			icon(c, "device-desktop", 32, pal.muted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, title).FontSize(m.font(16)).FontWeight(600).LineHeight(1.3)
				detail := fmt.Sprintf("%d × %d", display.Width, display.Height)
				if display.Main {
					detail += " · " + tr("Main display")
				}
				ui.Text(c, detail).FontSize(m.font(13)).TextColor(pal.muted)
			})
			icon(c, "chevron-right", 18, pal.muted)
		})
		if button.Clicked() {
			c.Blur()
			m.desktopSelectDisplay(p, display)
		}
	}
}
