package nsui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/nswidgets"
	"miya/internal/phone/desktopview"
	"miya/internal/remote"
	"miya/internal/service/workspaces"
)

func (m *workspaceHub) openPeerDesktop() {
	item := m.store.GetUntracked().Current()
	link := m.active.GetUntracked().State().Remote
	if item.ID == workspaces.Local || link == nil || !link.Connected() || link.Wire() == nil {
		return
	}
	wire := link.Wire()
	m.cx.OpenWindow(core.WindowOptions{
		Key: "peer-desktop:" + item.ID, Title: tr("Remote desktop") + " · " + item.Name, Width: 1120, Height: 750,
		Constraints: core.WindowConstraints{MinWidth: 540, MinHeight: 380},
	}, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		p := newPeerDesktop(cx, wire)
		cx.Cleanup(p.end)
		cx.Post(p.connect)
		return p.view()
	})
}

type peerDesktop struct {
	cx                            *core.Scope
	wire                          *remote.Conn
	ctx, frameCtx                 context.Context
	cancel, cancelFrames          context.CancelFunc
	client                        *control.Client
	session                       control.Session
	display                       desktop.Display
	stream                        *control.FrameStream
	frame                         *core.Signal[*desktopview.Frame]
	busy, locked, live, connected *core.Signal[bool]
	status, target, filter        *core.Signal[string]
	picker                        *core.Signal[string]
	pickerRev                     *core.Signal[int]
	privacy                       *core.Signal[bool]
	displays                      []desktop.Display
	windows                       []desktop.Window
	apps                          []desktop.Application
	queue                         []desktop.Input
	sending                       bool
	pendingWork                   func()
}

func newPeerDesktop(cx *core.Scope, wire *remote.Conn) *peerDesktop {
	return &peerDesktop{cx: cx, wire: wire, busy: core.NewSignal(cx, false), locked: core.NewSignal(cx, false),
		live: core.NewSignal(cx, false), connected: core.NewSignal(cx, false), privacy: core.NewSignal(cx, false), frame: core.NewSignal[*desktopview.Frame](cx, nil),
		status: core.NewSignal(cx, ""), target: core.NewSignal(cx, ""), picker: core.NewSignal(cx, ""), pickerRev: core.NewSignal(cx, 0), filter: core.NewSignal(cx, "")}
}

func (p *peerDesktop) end() {
	p.stopFrames()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.ctx, p.client = nil, nil
	p.pendingWork = nil
	p.connected.Set(false)
	p.picker.Set("")
	p.busy.Set(false)
}

func (p *peerDesktop) connect() {
	p.end()
	p.status.Set(tr("Connecting to Mac"))
	p.busy.Set(true)
	ctx, cancel := context.WithCancel(p.cx.Context())
	p.ctx, p.cancel = ctx, cancel
	go func() {
		client, done, err := control.OpenPaired(ctx, p.wire)
		var session control.Session
		if err == nil {
			sessionCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			session, err = client.Session(sessionCtx)
			stop()
		}
		p.cx.Post(func() {
			if p.ctx != ctx {
				return
			}
			p.busy.Set(false)
			if err != nil {
				p.end()
				p.status.Set(err.Error())
				return
			}
			p.client, p.session = client, session
			p.connected.Set(true)
			if session.Pick {
				if session.Displays && !session.WindowOnly {
					p.pick("displays")
				} else {
					p.pick("windows")
				}
			} else {
				p.selectTarget(desktop.Display{}, desktop.Window{})
			}
		})
		if err == nil {
			select {
			case <-ctx.Done():
			case <-done:
				p.cx.Post(func() {
					if p.ctx == ctx {
						p.end()
						p.status.Set(tr("Control session ended"))
					}
				})
			}
		}
	}()
}

// work serializes target changes with input. A new selection never inherits
// events queued for the old target.
func (p *peerDesktop) work(run func(context.Context, *control.Client) (func(), error)) {
	if p.client == nil || p.busy.Get() {
		return
	}
	p.busy.Set(true)
	p.queue = nil
	p.status.Set("")
	if p.sending {
		p.pendingWork = func() {
			p.busy.Set(false)
			p.work(run)
		}
		return
	}
	ctx, client := p.ctx, p.client
	go func() {
		call, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		done, err := run(call, client)
		p.cx.Post(func() {
			if p.ctx != ctx {
				return
			}
			p.busy.Set(false)
			if err != nil {
				p.showError(err)
				return
			}
			if done != nil {
				done()
			}
		})
	}()
}

func (p *peerDesktop) showError(err error) {
	if control.Status(err) == 410 {
		p.end()
		p.status.Set(tr("Control session ended"))
		return
	}
	p.status.Set(err.Error())
}

func (p *peerDesktop) pick(kind string) {
	p.work(func(ctx context.Context, client *control.Client) (func(), error) {
		switch kind {
		case "displays":
			items, err := client.Displays(ctx)
			return func() { p.displays = items; p.showPicker(kind) }, err
		case "windows":
			items, err := client.Windows(ctx)
			return func() { p.windows = items; p.showPicker(kind) }, err
		default:
			items, err := client.Applications(ctx)
			return func() { p.apps = items; p.showPicker(kind) }, err
		}
	})
}

func (p *peerDesktop) showPicker(kind string) {
	p.stopFrames()
	p.filter.Set("")
	p.picker.Set(kind)
	p.pickerRev.Update(func(n int) int { return n + 1 })
}

func (p *peerDesktop) selectTarget(display desktop.Display, window desktop.Window) {
	pick, privacy := p.session.Pick, p.session.Privacy
	p.work(func(ctx context.Context, client *control.Client) (func(), error) {
		var err error
		if display.ID != 0 {
			display, err = client.SelectDisplay(ctx, display)
		} else if pick {
			err = client.SelectWindow(ctx, window)
		}
		if err == nil {
			err = client.Start(ctx)
		}
		var private bool
		if err == nil && privacy {
			state, readErr := client.Privacy(ctx, nil)
			private = readErr == nil && state.Enabled
		}
		return func() {
			p.display = display
			p.privacy.Set(private)
			p.picker.Set("")
			switch {
			case display.Name != "":
				p.target.Set(display.Name)
			case window.ID != 0:
				p.target.Set(peerWindowName(window))
			default:
				p.target.Set(tr("Full desktop"))
			}
			p.startFrames()
		}, err
	})
}

func peerWindowName(w desktop.Window) string {
	if w.Title == "" || w.Title == w.App {
		return w.App
	}
	return w.App + " · " + w.Title
}

func (p *peerDesktop) stopFrames() {
	if p.cancelFrames != nil {
		p.cancelFrames()
		p.cancelFrames = nil
	}
	p.frameCtx, p.stream = nil, nil
	p.queue = nil
	p.live.Set(false)
	p.locked.Set(false)
	p.frame.Set(nil)
}

func (p *peerDesktop) startFrames() {
	p.stopFrames()
	ctx, cancel := context.WithCancel(p.ctx)
	p.frameCtx, p.cancelFrames = ctx, cancel
	p.live.Set(true)
	client, streamSupported := p.client, p.session.FrameStream
	go func() {
		var latest atomic.Pointer[desktopview.Frame]
		var posted atomic.Bool
		send := func(data []byte) {
			frame, err := desktopview.DecodeImage(data)
			if err != nil {
				return
			}
			latest.Store(frame)
			if posted.CompareAndSwap(false, true) {
				p.cx.Post(func() {
					posted.Store(false)
					if p.frameCtx == ctx {
						p.locked.Set(false)
						p.frame.Set(latest.Load())
					}
				})
			}
		}
		flagLocked := func() {
			if p.frameCtx == ctx {
				p.frame.Set(nil)
				p.locked.Set(true)
			}
		}
		report := func(err error) {
			p.cx.Post(func() {
				if p.frameCtx == ctx {
					p.showError(err)
				}
			})
		}
		if streamSupported {
			dial, stop := context.WithTimeout(ctx, 5*time.Second)
			stream, err := client.OpenFrameStream(dial)
			stop()
			if err == nil {
				p.cx.Post(func() {
					if p.frameCtx == ctx {
						p.stream = stream
					}
				})
				for ctx.Err() == nil {
					data, notice, err := stream.Receive(ctx)
					if err != nil {
						break
					}
					if notice == nil {
						send(data)
					} else if notice.Status == 423 {
						p.cx.Post(flagLocked)
					} else if notice.Status == 410 {
						report(&notice.HTTPError)
						stream.Close()
						return
					} else if notice.Status != 429 {
						report(&notice.HTTPError)
					}
				}
				stream.Close()
				p.cx.Post(func() {
					if p.frameCtx == ctx {
						p.stream = nil
						p.frame.Set(nil)
					}
				})
			} else if control.Status(err) == 410 {
				report(err)
				return
			}
		}
		for ctx.Err() == nil {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			data, locked, err := client.FrameWithOptions(call, desktop.FrameOptions{MaxEdge: control.InteractiveFrameMaxEdge, Quality: 65})
			cancel()
			if err == nil && !locked {
				send(data)
			} else if locked || control.Status(err) == 423 {
				p.cx.Post(flagLocked)
			} else if ctx.Err() == nil && control.Status(err) != 429 {
				report(err)
				if control.Status(err) == 410 {
					return
				}
			}
			pause := 150 * time.Millisecond
			if locked || err != nil {
				pause = time.Second
			}
			if !control.Wait(ctx, pause) {
				return
			}
		}
	}()
}

func (p *peerDesktop) view() core.Node {
	colors := nswidgets.ColorsOf(p.cx)
	password := core.NewSignal(p.cx, "")
	typed := core.NewSignal(p.cx, "")
	disabled := func() bool { return p.busy.Get() }
	ready := func() bool { return p.live.Get() && !p.busy.Get() && !p.locked.Get() && p.frame.Get() != nil }
	return core.View{TestID: "peer-desktop", Style: ui.FillRest.Merge(core.Style{Padding: core.All(12), Gap: 10}),
		StyleFunc: func() core.Style { return core.Style{Background: colors.Canvas()} }, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node {
				if !p.connected.Get() {
					return settingsActions(autoHeading(tr("Remote desktop")), settingsButton("peer-reconnect", tr("Reconnect"), p.connect, disabled),
						settingsText(cx, p.status.Get))
				}
				var bar []core.Node
				if p.session.Pick {
					if p.session.Displays && !p.session.WindowOnly {
						bar = append(bar, settingsButton("peer-displays", tr("Displays"), func() { p.pick("displays") }, disabled))
					}
					bar = append(bar, settingsButton("peer-windows", tr("Windows"), func() { p.pick("windows") }, disabled))
				}
				if p.session.Apps {
					bar = append(bar, settingsButton("peer-apps", tr("Apps"), func() { p.pick("apps") }, disabled))
				}
				if p.session.Privacy && (p.privacy.Get() || p.display.ID != 0 && p.live.Get()) {
					label := tr("Enable privacy mode")
					if p.privacy.Get() {
						label = tr("Restore Mac displays")
					}
					bar = append(bar, settingsButton("peer-privacy", label, func() {
						on := !p.privacy.Get()
						p.work(func(ctx context.Context, client *control.Client) (func(), error) {
							state, err := client.Privacy(ctx, &on)
							return func() { p.privacy.Set(state.Enabled) }, err
						})
					}, func() bool { return p.busy.Get() || !p.privacy.Get() && !ready() }))
				}
				bar = append(bar, settingsButton("peer-stop", tr("End control"), func() {
					p.end()
					p.status.Set(tr("Control session ended"))
				}, nil))
				return settingsColumn(settingsActions(bar...), settingsText(cx, p.target.Get), settingsText(cx, p.status.Get))
			}),
			core.Show{When: p.locked.Get, Then: func(cx *core.Scope) core.Node {
				unlock := func() {
					value := password.Get()
					if value == "" || p.busy.Get() {
						return
					}
					p.work(func(ctx context.Context, client *control.Client) (func(), error) {
						err := client.Unlock(ctx, value)
						return nil, err
					})
					password.Set("")
				}
				return settingsActions(ui.Input{TestID: "peer-password", Label: tr("Enter Mac login password"), Masked: true,
					Placeholder: tr("Enter Mac login password"), Value: password.Get, OnChange: password.Set, OnSubmit: func(string) { unlock() }},
					settingsButton("peer-unlock", tr("Unlock"), unlock, disabled))
			}},
			core.Show{When: p.live.Get, Then: func(cx *core.Scope) core.Node {
				return core.View{Style: ui.FillRest.Merge(core.Style{Gap: 8}), Children: []core.Node{
					p.screen(cx, ready),
					settingsActions(ui.Input{TestID: "peer-text", Label: tr("Enter text to send"), Placeholder: tr("Enter text to send"),
						Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(180)}, Value: typed.Get, OnChange: typed.Set,
						OnSubmit: func(text string) {
							if ready() && p.enqueue(desktop.Input{Kind: "text", Text: text}) {
								typed.Set("")
							}
						}}, settingsButton("peer-send-text", tr("Send"), func() {
						if ready() && p.enqueue(desktop.Input{Kind: "text", Text: typed.Get()}) {
							typed.Set("")
						}
					}, func() bool { return !ready() || typed.Get() == "" })),
				}}
			}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				p.pickerRev.Get()
				kind := p.picker.Get()
				if kind == "" {
					return nil
				}
				var rows []core.Node
				switch kind {
				case "displays":
					for _, d := range p.displays {
						rows = append(rows, ui.FormRow{Title: d.Name, Detail: fmt.Sprintf("%d × %d", d.Width, d.Height),
							TestID: "peer-display-" + strconv.FormatUint(uint64(d.ID), 10), Disabled: disabled, OnPress: func() { p.selectTarget(d, desktop.Window{}) }})
					}
				case "windows":
					if !p.session.WindowOnly && !p.session.Displays {
						rows = append(rows, ui.FormRow{Title: tr("Full desktop"), TestID: "peer-full-desktop", Disabled: disabled,
							OnPress: func() { p.selectTarget(desktop.Display{}, desktop.Window{}) }})
					}
					for _, w := range p.windows {
						rows = append(rows, ui.FormRow{Title: peerWindowName(w), TestID: "peer-window-" + strconv.FormatUint(uint64(w.ID), 10),
							Disabled: disabled, OnPress: func() { p.selectTarget(desktop.Display{}, w) }})
					}
				default:
					filter := strings.ToLower(strings.TrimSpace(p.filter.Get()))
					for _, app := range p.apps {
						if !strings.Contains(strings.ToLower(app.Name), filter) {
							continue
						}
						rows = append(rows, ui.FormRow{Title: app.Name, TestID: "peer-app-" + app.ID, Disabled: disabled, OnPress: func() {
							p.work(func(ctx context.Context, client *control.Client) (func(), error) {
								_, err := client.LaunchApplication(ctx, app.ID)
								return func() { p.pick("windows") }, err
							})
						}})
					}
				}
				if len(rows) == 0 {
					rows = append(rows, ui.FormRow{Title: tr("No matching results")})
				}
				var content []core.Node
				if kind == "apps" {
					content = append(content, ui.Input{Label: tr("Search apps"), Placeholder: tr("Search apps"), Value: p.filter.Get, OnChange: p.filter.Set})
				}
				content = append(content, core.Scroll{Style: ui.FillRest, Children: []core.Node{ui.FormSection{Children: rows}}})
				return core.View{Style: ui.FillRest.Merge(core.Style{Gap: 8}), Children: content}
			}),
		}}
}
