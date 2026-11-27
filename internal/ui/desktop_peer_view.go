package desktopui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (p *peerDesktop) view(c *ui.Context) {
	widgets.UseInterfaceLanguage(c)
	c.SetTheme(p.app.Theme)
	p.poll(c)
	ui.Column(c).Fill().Background(widgets.Canvas).TextColor(widgets.Ink).FontSize(13).Children(func() {
		p.toolbar(c)
		switch {
		case p.picker != "":
			p.targetPicker(c)
		case p.live:
			p.screen(c)
		default:
			ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
				if p.busy {
					ui.Spinner(c)
				} else if ui.Button(c.Key("reconnect"), tr("Reconnect")).Clicked() {
					p.connect()
				}
			})
		}
		if p.locked && p.picker == "" && p.live {
			p.unlockBar(c)
		}
		if p.status != "" || p.frameError != "" {
			ui.Text(c, textutil.FirstNonEmpty(p.status, p.frameError)).FillWidth().Padding(8, 14).TextColor(widgets.Muted).MaxLines(3)
		}
	})
}

func (p *peerDesktop) toolbar(c *ui.Context) {
	ui.Row(c).FillWidth().Shrink(0).Padding(8, 12).Gap(6).AlignItems(ui.Center).Background(widgets.Panel).Children(func() {
		ui.Text(c, textutil.FirstNonEmpty(p.target, p.name)).MinWidth(0).Grow(1).SingleLine().Ellipsis("…").FontWeight(600)
		if p.client != nil && p.session.Pick {
			target := ui.Button(c.Key("desktop-target"), tr("Full desktop")).Disabled(p.busy)
			if target.Clicked() {
				if p.session.Displays {
					p.pick("displays")
				} else {
					p.selectTarget(desktop.Display{}, desktop.Window{})
				}
			}
			if ui.Button(c.Key("desktop-windows"), tr("Windows")).Disabled(p.busy).Clicked() {
				p.pick("windows")
			}
			if p.session.Apps && ui.Button(c.Key("desktop-apps"), tr("Apps")).Disabled(p.busy).Clicked() {
				p.pick("apps")
			}
		}
		if p.client != nil && p.session.Privacy {
			button := ui.Button(c.Key("desktop-privacy"), tr("Privacy mode")).Disabled(p.busy || !p.privacy && (p.image == nil || p.locked))
			if p.privacy {
				button.Background(widgets.Hover)
			}
			if button.Clicked() {
				p.togglePrivacy()
			}
		}
		if ui.Button(c.Key("close-desktop"), tr("Close")).Clicked() {
			p.window.Close()
		}
	})
}

func (p *peerDesktop) screen(c *ui.Context) {
	vp := ui.Column(c.Key("peer-screen")).Grow(1).FillWidth().MinHeight(0).Clip().Background(ui.RGB(0, 0, 0)).
		Label(tr("Remote desktop")).Focusable().Ref(&p.focus).
		TextCaret(ui.Rect{W: 1, H: 16}).InputMethod(true).HandleInput(p.handleInput)
	if bounds := vp.Bounds(); bounds.W > 0 && bounds.H > 0 {
		p.viewport = bounds
	}
	vp.DrawOver(func(_ *ui.Painter, box ui.Rect) { p.viewport = box })
	vp.Children(func() {
		if p.image != nil {
			if r := p.imageRect(); p.zoom > 1 && r.W > 0 && r.H > 0 {
				ui.Image(c, p.image).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).Fit(ui.FillBox)
			} else {
				ui.Image(c, p.image).Fill().MinHeight(0).MinWidth(0).Fit(ui.Contain)
			}
			return
		}
		ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
			if p.locked {
				ui.Text(c, tr("Mac is locked")).TextColor(ui.RGB(255, 255, 255))
			} else {
				ui.Spinner(c)
				ui.Text(c, tr("Loading screen…")).TextColor(ui.RGB(255, 255, 255))
			}
		})
	})
}

func (p *peerDesktop) unlockBar(c *ui.Context) {
	ui.Row(c).FillWidth().Shrink(0).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key("unlock-password"), &p.password).Grow(1).Password().Label(tr("Mac login password")).
			Placeholder(tr("Mac login password")).Disabled(p.busy)
		if (ui.Button(c.Key("unlock-mac"), tr("Unlock")).Disabled(p.busy || !control.ValidUnlockPassword(p.password)).Clicked() || input.Submitted()) &&
			!p.busy && control.ValidUnlockPassword(p.password) {
			p.unlock()
		}
	})
}

func (p *peerDesktop) targetPicker(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(12, 16).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if p.live && ui.Button(c.Key("picker-back"), tr("Back")).Disabled(p.busy).Clicked() {
				p.picker, p.status, p.frameBackoff = "", "", time.Time{}
			}
			if ui.Button(c.Key("picker-refresh"), tr("Refresh")).Disabled(p.busy || p.picker == "").Clicked() {
				p.pick(p.picker)
			}
			if p.picker == "apps" {
				ui.TextInput(c.Key("target-search"), &p.search).Placeholder(tr("Search apps")).Label(tr("Search apps")).Grow(1)
			}
		})
		ui.Scroll(c.Key("targets-" + p.picker)).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				switch p.picker {
				case "displays":
					if len(p.displays) == 0 {
						ui.Text(c, tr("No displays available. Wake your Mac and refresh the list."))
					}
					for i, display := range p.displays {
						name := textutil.FirstNonEmpty(display.Name, tr("Unnamed display"))
						label := fmt.Sprintf(tr("Desktop %d"), i+1) + " · " + name
						label += fmt.Sprintf(" · %d × %d", display.Width, display.Height)
						if display.Main {
							label += " · " + tr("Main display")
						}
						if ui.Button(c.Key(display.UUID), label).FillWidth().Disabled(p.busy).Clicked() {
							p.selectTarget(display, desktop.Window{})
						}
					}
				case "windows":
					if len(p.windows) == 0 {
						ui.Text(c, tr("No shareable windows."))
					}
					for _, window := range p.windows {
						key := fmt.Sprintf("%d:%d", window.ID, window.PID)
						if ui.Button(c.Key(key), peerWindowName(window)).FillWidth().Disabled(p.busy).Clicked() {
							p.selectTarget(desktop.Display{}, window)
						}
					}
				case "apps":
					for _, app := range p.apps {
						if peerMatches(app.Name, p.search) || peerMatches(app.BundleID, p.search) {
							if ui.Button(c.Key(app.ID), app.Name).FillWidth().Disabled(p.busy).Clicked() {
								p.launch(app)
							}
						}
					}
				}
			})
		})
	})
}

func peerWindowName(window desktop.Window) string {
	if window.Title == "" || window.App == "" || window.Title == window.App {
		return textutil.FirstNonEmpty(window.Title, window.App)
	}
	return window.App + " · " + window.Title
}

func peerMatches(value, search string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(search)))
}
