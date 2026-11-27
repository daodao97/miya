package desktopui

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/uiruntime"
)

// The viewer stays bound to the workspace that opened it, even if the main
// window switches workspaces. Network workers publish results on its UI thread.
type peerDesktop struct {
	app          *App
	name         string
	window       *mygo.Window
	ctx          context.Context
	cancel       context.CancelFunc
	client       *control.Client
	stream       *control.FrameStream
	streamCtx    context.Context
	streamCancel context.CancelFunc

	session  control.Session
	display  desktop.Display
	target   string
	picker   string
	displays []desktop.Display
	windows  []desktop.Window
	apps     []desktop.Application
	search   string

	busy, live, polling, sending   bool
	pending                        func()
	lastFrameRequest, frameBackoff time.Time
	scrollingUntil                 time.Time
	nextPrivacy                    time.Time
	status                         string
	frameError                     string
	image                          ui.ImageSource
	hash                           [32]byte
	locked, privacy                bool
	password                       string

	viewport         ui.Rect
	focus            ui.Ref
	press            *ui.InputEvent
	queue            []desktop.Input
	scrollX, scrollY float32
	zoom, panX, panY float32
}

func (m *DesktopWorkspaces) openPeerDesktop() {
	app := m.Active
	link := app.C.State().Remote
	if !mygo.App.IsReady() || !link.Connected() || link.Wire() == nil {
		return
	}
	if p := m.peerDesktop; p != nil && !p.window.IsDestroyed() {
		if p.app == app {
			p.window.Show()
			return
		}
		p.end("")
		p.window.Close()
	}
	p := &peerDesktop{app: app, name: m.Store.Current().Name}
	title := tr("Remote desktop") + " · " + p.name
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: title, Width: 1120, Height: 750, MinWidth: 540, MinHeight: 380,
		BackgroundColor: windowBackground(app), Content: ui.View(p.view),
	})
	p.window, m.peerDesktop = w, p
	remove := uiruntime.AddCompanionWindow(w)
	w.OnClosed(func() {
		p.end("")
		remove()
		if m.peerDesktop == p {
			m.peerDesktop = nil
		}
	})
	p.connect()
}

func (p *peerDesktop) apply(ctx context.Context, f func()) {
	if ctx.Err() == nil {
		p.window.Update(func() {
			if ctx.Err() == nil && p.ctx == ctx {
				f()
			}
		})
	}
}

func (p *peerDesktop) end(status string) {
	p.stopStreaming()
	if p.cancel != nil {
		p.cancel()
	}
	p.cancel, p.client = nil, nil
	p.displays, p.windows, p.apps = nil, nil, nil
	p.busy, p.live, p.polling, p.sending = false, false, false, false
	p.image, p.pending = nil, nil
	p.discardInput()
	p.zoom, p.panX, p.panY = 1, 0, 0
	p.status, p.picker, p.target, p.password = status, "", "", ""
	p.frameError = ""
	p.lastFrameRequest, p.frameBackoff = time.Time{}, time.Time{}
	p.nextPrivacy = time.Time{}
	p.locked, p.privacy = false, false
}

func (p *peerDesktop) connect() {
	p.end("")
	link := p.app.C.State().Remote
	if !link.Connected() || link.Wire() == nil {
		p.status = tr("Connection lost. Reconnect to retry.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.ctx, p.cancel, p.busy = ctx, cancel, true
	p.status = tr("Connecting to Mac")
	wire := link.Wire()
	go func() {
		client, done, err := control.OpenPaired(ctx, wire)
		var session control.Session
		var displays []desktop.Display
		if err == nil {
			session, err = client.Session(ctx)
		}
		if err == nil && session.Pick && session.Displays {
			displays, err = client.Displays(ctx)
		}
		p.apply(ctx, func() {
			if err != nil {
				p.end(tr(err.Error()))
				return
			}
			p.client, p.session, p.displays = client, session, displays
			p.busy, p.status = false, ""
			switch {
			case !session.Pick || !session.Displays:
				p.selectTarget(desktop.Display{}, desktop.Window{})
			case len(displays) == 1:
				p.selectTarget(displays[0], desktop.Window{})
			default:
				p.picker = "displays"
			}
		})
		if done != nil {
			select {
			case <-ctx.Done():
			case <-done:
				p.apply(ctx, func() { p.end(tr("Connection lost. Reconnect to retry.")) })
			}
		}
	}()
}

// A target change must wait for the last frame and input request. Old input
// still in the local queue is discarded before the server selects a target.
func (p *peerDesktop) work(run func(context.Context, *control.Client) func()) {
	if p.client == nil || p.busy {
		return
	}
	ctx, client := p.ctx, p.client
	p.busy, p.status = true, ""
	p.discardInput()
	p.stopStreaming()
	p.pending = func() {
		go func() {
			apply := run(ctx, client)
			p.apply(ctx, func() {
				p.busy = false
				apply()
			})
		}()
	}
	p.advance()
}

func (p *peerDesktop) advance() {
	if p.pending != nil && !p.polling && !p.sending {
		start := p.pending
		p.pending = nil
		start()
	}
}

func (p *peerDesktop) showError(err error) {
	if control.Status(err) == 410 {
		p.end(tr("Control session ended"))
		return
	}
	p.status = tr(err.Error())
}

func (p *peerDesktop) selectTarget(display desktop.Display, window desktop.Window) {
	pickable := p.session.Pick
	p.live, p.image, p.locked, p.password = false, nil, false, ""
	p.work(func(ctx context.Context, client *control.Client) func() {
		var err error
		switch {
		case display.ID != 0:
			display, err = client.SelectDisplay(ctx, display)
		case pickable:
			err = client.SelectWindow(ctx, window)
		}
		if err == nil {
			err = client.Start(ctx)
		}
		return func() {
			if err != nil {
				p.showError(err)
				return
			}
			p.target = tr("Full desktop")
			if display.Name != "" {
				p.target = display.Name
			} else if window.ID != 0 {
				p.target = peerWindowName(window)
			}
			p.display, p.picker, p.live = display, "", true
			p.lastFrameRequest, p.frameBackoff = time.Time{}, time.Time{}
			p.zoom, p.panX, p.panY = 1, 0, 0
			p.frameError = ""
		}
	})
}

func (p *peerDesktop) pick(kind string) {
	p.work(func(ctx context.Context, client *control.Client) func() {
		var displays []desktop.Display
		var windows []desktop.Window
		var apps []desktop.Application
		var err error
		switch kind {
		case "displays":
			displays, err = client.Displays(ctx)
		case "windows":
			windows, err = client.Windows(ctx)
		case "apps":
			apps, err = client.Applications(ctx)
		}
		return func() {
			if err != nil {
				p.showError(err)
				return
			}
			p.picker, p.search, p.image = kind, "", nil
			p.displays, p.windows, p.apps = displays, windows, apps
		}
	})
}

func (p *peerDesktop) launch(app desktop.Application) {
	p.work(func(ctx context.Context, client *control.Client) func() {
		_, err := client.LaunchApplication(ctx, app.ID)
		return func() {
			if err != nil {
				p.showError(err)
				return
			}
			p.pick("windows")
		}
	})
}

func (p *peerDesktop) frameProfile(now time.Time) (desktop.FrameOptions, time.Duration, bool) {
	const pacingMargin = 10 * time.Millisecond
	if now.Before(p.scrollingUntil) {
		options := desktop.FrameOptions{MaxEdge: control.InteractiveFrameMaxEdge, Quality: 65}
		if p.session.InteractiveFrames {
			return options, control.InteractiveFrameInterval + pacingMargin, true
		}
		return options, control.FrameInterval + pacingMargin, false
	}
	return desktop.FrameOptions{MaxEdge: 1920, Quality: 75}, control.FrameInterval + pacingMargin, false
}

func (p *peerDesktop) poll(c *ui.Context) {
	if !p.live || p.busy || p.picker != "" || p.polling {
		return
	}
	now := time.Now()
	if p.session.Privacy && now.After(p.nextPrivacy) {
		p.refreshPrivacy(now)
	}
	if p.session.FrameStream {
		if p.streamCtx == nil {
			p.startStreaming()
		}
		return
	}
	options, interval, interactive := p.frameProfile(now)
	// Pace requests from their start. Capture and round-trip time already
	// use up part (or all) of that interval.
	due := p.lastFrameRequest.Add(interval)
	if p.frameBackoff.After(due) {
		due = p.frameBackoff
	}
	if now.Before(due) {
		c.After(due.Sub(now))
		return
	}
	p.polling = true
	p.lastFrameRequest = now
	ctx, client := p.ctx, p.client
	image, previousHash := p.image, p.hash
	fetch := client.FrameWithOptions
	if interactive {
		fetch = client.InteractiveFrame
	}
	go func() {
		frameCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		data, locked, err := fetch(frameCtx, options)
		var hash [32]byte
		if err == nil && !locked {
			hash = sha256.Sum256(data)
			if image == nil || hash != previousHash {
				image, err = ui.DecodeImage(data)
				if image == nil && err == nil {
					err = errors.New(tr("Unable to decode the screen. Retrying."))
				}
			}
		}
		p.apply(ctx, func() {
			p.polling = false
			defer p.advance()
			if p.busy {
				return
			}
			if control.Status(err) == 429 {
				p.frameBackoff = time.Now().Add(interval)
				return
			}
			p.receiveFrame(image, hash, locked, err)
		})
	}()
}

func (p *peerDesktop) receiveFrame(image ui.ImageSource, hash [32]byte, locked bool, err error) {
	if locked {
		p.locked, p.image = true, nil
		p.discardInput()
		p.frameError = ""
		p.frameBackoff = time.Now().Add(750 * time.Millisecond)
		return
	}
	if err != nil {
		p.image = nil
		p.discardInput()
		p.frameBackoff = time.Now().Add(time.Second)
		if control.Status(err) == 410 {
			p.showError(err)
		} else {
			p.frameError = tr(err.Error())
			if control.Status(err) == 409 && p.display.ID != 0 && p.session.Displays {
				p.live = false
				p.pick("displays")
			}
		}
		return
	}
	p.frameError = ""
	p.frameBackoff = time.Time{}
	p.locked, p.password = false, ""
	if p.image == nil || hash != p.hash {
		p.image, p.hash = image, hash
	}
}

func (p *peerDesktop) refreshPrivacy(now time.Time) {
	next, ctx, client := now.Add(3*time.Second), p.ctx, p.client
	p.nextPrivacy = next
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		state, err := client.Privacy(callCtx, nil)
		if err == nil {
			p.apply(ctx, func() {
				if p.nextPrivacy == next {
					p.privacy = state.Enabled
				}
			})
		}
	}()
}

func (p *peerDesktop) togglePrivacy() {
	enabled := !p.privacy
	p.nextPrivacy = time.Time{}
	p.work(func(ctx context.Context, client *control.Client) func() {
		state, err := client.Privacy(ctx, &enabled)
		return func() {
			if err != nil {
				p.showError(err)
				return
			}
			p.privacy = state.Enabled
		}
	})
}

func (p *peerDesktop) unlock() {
	password := p.password
	p.password = ""
	p.work(func(ctx context.Context, client *control.Client) func() {
		err := client.Unlock(ctx, password)
		return func() {
			if err != nil {
				p.showError(err)
			}
			p.image, p.frameBackoff = nil, time.Time{}
		}
	})
}
