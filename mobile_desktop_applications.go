package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
)

func (m *mobileApp) desktopChooseApps(p *mobileDesktopPage) {
	if !p.pickable || !p.appsAvailable || p.busy {
		return
	}
	p.invalidateTarget()
	p.pickerTab, p.appStatus = 1, ""
	m.desktopStage(p, "picker", tr("Apps"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		apps, err := p.client.Applications(ctx)
		icons := map[string]ui.ImageSource{}
		if err == nil {
			for i := range apps {
				if len(apps[i].Icon) > 0 {
					icons[apps[i].ID], _ = ui.DecodeImage(apps[i].Icon)
				}
				apps[i].Icon = nil
			}
		}
		return func() {
			p.busy = false
			if control.Status(err) == 410 {
				m.desktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.appStatus = tr("Unable to load apps") + ": " + err.Error()
				return
			}
			p.apps, p.appIcons = apps, icons
		}
	})
}

// Match bundle identity, never a window title or the app's display name.
// Multiple possible windows stay in the picker so the user can choose.
func launchedDesktopWindow(app desktop.Application, before, after []desktop.Window) (desktop.Window, bool) {
	if app.BundleID == "" {
		return desktop.Window{}, false
	}
	known := map[string]bool{}
	for _, w := range before {
		known[desktopWindowKey(w)] = true
	}
	var matches, fresh []desktop.Window
	for _, w := range after {
		if w.BundleID != app.BundleID {
			continue
		}
		matches = append(matches, w)
		if !known[desktopWindowKey(w)] {
			fresh = append(fresh, w)
		}
	}
	if len(fresh) == 1 {
		return fresh[0], true
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return desktop.Window{}, false
}

func (m *mobileApp) desktopLaunchApp(p *mobileDesktopPage, app desktop.Application) {
	if !p.pickable || !p.appsAvailable || p.busy {
		return
	}
	m.desktopStage(p, "connecting", fmt.Sprintf(tr("Opening %s…"), app.Name), tr("Waiting for the app window on your Mac."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		// A frame requested before opening the launcher may still occupy the
		// capture path. Wait for it before taking the pre-launch snapshot.
		before, err := desktopAppWindows(ctx, p.client)
		var opened desktop.Application
		if err == nil {
			opened, err = p.client.LaunchApplication(ctx, app.ID)
		}
		if err != nil {
			return func() {
				if control.Status(err) == 410 {
					m.desktopFinish(p, err.Error())
					return
				}
				m.desktopStage(p, "picker", tr("Apps"), "", false)
				p.appStatus = tr("Unable to open app") + ": " + err.Error()
				if control.Status(err) == 423 {
					m.desktopSelectFull(p)
				}
			}
		}
		var windows []desktop.Window
		for deadline := time.Now().Add(9 * time.Second); time.Now().Before(deadline); {
			// Give cold launches a moment to create their first window.
			if !control.Wait(ctx, 750*time.Millisecond) {
				return func() {}
			}
			windows, err = p.client.Windows(ctx)
			if control.Status(err) == 429 {
				continue
			}
			if err != nil {
				break
			}
			if w, ok := launchedDesktopWindow(opened, before, windows); ok {
				return func() { m.desktopSelect(p, w) }
			}
		}
		return func() {
			if control.Status(err) == 410 {
				m.desktopFinish(p, err.Error())
				return
			}
			p.windows, p.thumbs, p.pickerTab = windows, nil, 0
			m.desktopStage(p, "picker", tr("Select a window to control"), "", false)
			p.appStatus = tr("App opened. Select its window below, or view the full desktop.")
			if err != nil {
				p.appStatus = err.Error()
			}
			m.desktopLoadThumbs(p)
		}
	})
}

func desktopAppWindows(ctx context.Context, client *control.Client) ([]desktop.Window, error) {
	deadline := time.Now().Add(6 * time.Second)
	for {
		windows, err := client.Windows(ctx)
		if control.Status(err) != 429 || !time.Now().Before(deadline) {
			return windows, err
		}
		if !control.Wait(ctx, 250*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
}

func (m *mobileApp) desktopApplicationList(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	ui.TextInput(c.Key("desktop-app-search"), &p.appSearch).Label(tr("Search apps")).Placeholder(tr("Search apps")).FillWidth().MinHeight(44).Padding(10).FontSize(m.font(16)).Background(pal.surface).Radius(12).Disabled(p.busy)
	if p.appStatus != "" {
		ui.Text(c, p.appStatus).FontSize(m.font(14)).TextColor(pal.muted).LineHeight(1.5)
	}
	if p.busy {
		ui.Spinner(c).Margin(12)
		return
	}
	var apps []desktop.Application
	query := strings.ToLower(strings.TrimSpace(p.appSearch))
	for _, app := range p.apps {
		if strings.Contains(strings.ToLower(app.Name+" "+app.BundleID+" "+app.ID), query) {
			apps = append(apps, app)
		}
	}
	if len(apps) == 0 && p.appStatus == "" {
		ui.Text(c, tr("No apps found.")).FontSize(m.font(15)).TextColor(pal.muted)
	}
	for i := 0; i < len(apps); i += 3 {
		row := apps[i:min(i+3, len(apps))]
		ui.Row(c.Key(fmt.Sprintf("desktop-app-row-%d", i))).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			for _, app := range row {
				button := m.phoneButton(c.Key("desktop-app-" + app.ID)).Column().Grow(1).Basis(0).MinWidth(0).Padding(8).Gap(6).Radius(12).Background(pal.surface).AlignItems(ui.Center).Label(fmt.Sprintf(tr("Open %s"), app.Name))
				button.Children(func() {
					if image := p.appIcons[app.ID]; image != nil {
						ui.Image(c, image).Size(48, 48).Fit(ui.Contain)
					} else {
						ui.Box(c).Size(48, 48).AlignItems(ui.Center).Justify(ui.Center).Children(func() { icon(c, "apps", 28, pal.muted) })
					}
					ui.Text(c, app.Name).FontSize(m.font(13)).MinHeight(m.font(13) * 2.6).TextAlign(ui.Center).MaxLines(2).LineHeight(1.3)
				})
				if button.Clicked() {
					c.Blur()
					m.desktopLaunchApp(p, app)
				}
			}
			for n := len(row); n < 3; n++ {
				ui.Box(c).Grow(1).Basis(0).MinWidth(0)
			}
		})
	}
}
