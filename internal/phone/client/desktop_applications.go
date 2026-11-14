package client

import (
	"context"
	"fmt"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/phone/desktopview"
)

func (m *Client) DesktopChooseApps(p *MobileDesktopPage) {
	if !p.Pickable || !p.AppsAvailable || p.Busy {
		return
	}
	p.InvalidateTarget()
	p.PickerTab, p.AppStatus = 1, ""
	m.DesktopStage(p, "picker", tr("Apps"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		apps, err := p.Client.Applications(ctx)
		icons := map[string]desktopview.ImageSource{}
		if err == nil {
			for i := range apps {
				if len(apps[i].Icon) > 0 {
					icons[apps[i].ID], _ = desktopview.DecodeImage(apps[i].Icon)
				}
				apps[i].Icon = nil
			}
		}
		return func() {
			p.Busy = false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.AppStatus = tr("Unable to load apps") + ": " + err.Error()
				return
			}
			p.Apps, p.AppIcons = apps, icons
		}
	})
}

// Match bundle identity, never a window title or the app's display name.
// Multiple possible windows stay in the picker so the user can choose.
func LaunchedDesktopWindow(app desktop.Application, before, after []desktop.Window) (desktop.Window, bool) {
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

func (m *Client) DesktopLaunchApp(p *MobileDesktopPage, app desktop.Application) {
	if !p.Pickable || !p.AppsAvailable || p.Busy {
		return
	}
	m.DesktopStage(p, "connecting", fmt.Sprintf(tr("Opening %s…"), app.Name), tr("Waiting for the app window on your Mac."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		// A frame requested before opening the launcher may still occupy the
		// capture path. Wait for it before taking the pre-launch snapshot.
		before, err := desktopAppWindows(ctx, p.Client)
		var opened desktop.Application
		if err == nil {
			opened, err = p.Client.LaunchApplication(ctx, app.ID)
		}
		if err != nil {
			return func() {
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				m.DesktopStage(p, "picker", tr("Apps"), "", false)
				p.AppStatus = tr("Unable to open app") + ": " + err.Error()
				if control.Status(err) == 423 {
					m.DesktopSelectFull(p)
				}
			}
		}
		var windows []desktop.Window
		for deadline := time.Now().Add(9 * time.Second); time.Now().Before(deadline); {
			// Give cold launches a moment to create their first window.
			if !control.Wait(ctx, 750*time.Millisecond) {
				return func() {}
			}
			windows, err = p.Client.Windows(ctx)
			if control.Status(err) == 429 {
				continue
			}
			if err != nil {
				break
			}
			if w, ok := LaunchedDesktopWindow(opened, before, windows); ok {
				return func() { m.DesktopSelectWindow(p, w) }
			}
		}
		return func() {
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			p.Windows, p.Thumbs, p.PickerTab = windows, nil, 0
			m.DesktopStage(p, "picker", tr("Select a window to control"), "", false)
			p.AppStatus = tr("App opened. Select its window below, or view the full desktop.")
			if err != nil {
				p.AppStatus = err.Error()
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
