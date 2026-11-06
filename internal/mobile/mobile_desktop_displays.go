package mobile

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (p *MobileDesktopPage) InvalidateTarget() {
	p.targetRevision++
	p.Active, p.Image, p.Queue, p.actionsFresh, p.More = false, nil, nil, false, false
	p.Polling, p.Sending, p.actionsBusy = false, false, false
	p.pointer = desktopPointer{}
	p.lastTap, p.Taps = time.Time{}, nil
	p.scrollX, p.scrollY = 0, 0
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

func (m *App) DesktopPresentDisplays(p *MobileDesktopPage, displays []desktop.Display) {
	p.InvalidateTarget()
	p.Displays, p.PickerTab, p.appStatus = displays, 2, ""
	m.DesktopStage(p, "picker", tr("Full desktop"), "", false)
}

func (m *App) desktopChooseDisplays(p *MobileDesktopPage) {
	if !p.Pickable || !p.DisplaysAvailable {
		return
	}
	p.InvalidateTarget()
	p.PickerTab, p.appStatus = 2, ""
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
				p.appStatus = tr("Unable to load desktops") + ": " + err.Error()
				return
			}
			p.Displays = displays
		}
	})
}

func (m *App) DesktopSelectDisplay(p *MobileDesktopPage, display desktop.Display) {
	if !p.Pickable || !p.DisplaysAvailable || display.ID == 0 {
		return
	}
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
				p.appStatus = err.Error()
				return
			}
			p.Display, p.Window, p.WindowOnly = selected, desktop.Window{}, false
			p.Zoom, p.PanX, p.panY = 1, 0, 0
			p.actions, p.actionsFresh = control.Actions{}, false
			m.desktopStart(p)
		}
	})
}

func (m *App) desktopDisplayList(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	if p.appStatus != "" {
		ui.Text(c, p.appStatus).FontSize(m.Font(14)).TextColor(pal.muted).LineHeight(1.5)
	}
	if p.Busy {
		ui.Spinner(c).Margin(12)
		return
	}
	if len(p.Displays) == 0 {
		ui.Text(c, tr("No displays available. Wake your Mac and refresh the list.")).FontSize(m.Font(14)).TextColor(pal.muted)
		return
	}
	for i, display := range p.Displays {
		title := desktopDisplayTitle(display, i+1)
		button := m.phoneButton(c.Key("desktop-display-" + display.UUID)).FillWidth().Padding(14).Gap(12).Background(pal.surface).Radius(14).Label(title)
		button.Children(func() {
			widgets.Icon(c, "device-desktop", 32, pal.muted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, title).FontSize(m.Font(16)).FontWeight(600).LineHeight(1.3)
				detail := fmt.Sprintf("%d × %d", display.Width, display.Height)
				if display.Main {
					detail += " · " + tr("Main display")
				}
				ui.Text(c, detail).FontSize(m.Font(13)).TextColor(pal.muted)
			})
			widgets.Icon(c, "chevron-right", 18, pal.muted)
		})
		if button.Clicked() {
			c.Blur()
			m.DesktopSelectDisplay(p, display)
		}
	}
}
