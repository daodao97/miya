package nsui

import (
	"fmt"
	"sync"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/uiruntime"
)

// Host is what the NSGo desktop needs of a window's controller. It grows
// toward internal/ui.Host as views move over; the controller already
// implements the whole of that.
type Host interface {
	State() *appmodel.WindowState
	Current() model.Thread
	SelectThread(t model.Thread)
	EnsurePhoneHost()
}

// Environment supplies controller creation at the executable's composition
// root, as internal/ui.Environment does.
type Environment struct {
	NewWindow func() Host
}

var environment Environment

func ConfigureEnvironment(e Environment) { environment = e }

// DesktopMain runs the desktop app on NSGo until it quits.
func DesktopMain() {
	nsgo.Run(appMain,
		nsgo.WindowSize(1180, 820),
		nsgo.WindowConstraints(core.WindowConstraints{MinWidth: 780, MinHeight: 580}),
	)
}

// appMain runs once in the app's scope: it bridges the controller to the
// engine, then opens the main window.
func appMain(cx *core.Scope) {
	b := installBridge(cx)
	installPlatformServices(cx)
	c := environment.NewWindow()
	// With the background service running, the service serves phones: a
	// window only mirrors the current project.
	c.EnsurePhoneHost()
	nsgo.SetMainView(cx, func(cx *core.Scope) core.Node { return root(cx, c) })
	b.setWindow(desktop.MainWindow(cx))
}

// installPlatformServices gives the controller NSGo's notifications. Idle
// time is left unset, so the user counts as present, until Miya reads it
// from the system itself.
func installPlatformServices(cx *core.Scope) {
	var mu sync.Mutex
	clicks := map[string]func(){}
	cx.OnNotificationPress(func(id string) {
		mu.Lock()
		f := clicks[id]
		delete(clicks, id)
		mu.Unlock()
		if f != nil {
			f()
		}
	})
	uiruntime.ShowNotice = func(n uiruntime.Notice) error {
		if n.OnClick != nil {
			mu.Lock()
			clicks[n.ID] = n.OnClick
			mu.Unlock()
		}
		return cx.ShowNotification(core.Notification{ID: n.ID, Title: n.Title, Body: n.Body}, nil)
	}
}

// root is the window's content. This is the migration's first view: the
// conversation list and the selected conversation's title, to prove the
// controller drives NSGo through the bridge.
func root(cx *core.Scope, c Host) core.Node {
	st := c.State()
	threads := Live(func() []model.Thread {
		if st.Threads == nil {
			return nil
		}
		return st.Threads()
	})
	current := Live(c.Current)
	return core.View{
		Style: core.Style{Direction: core.Row, FlexGrow: 1},
		Children: []core.Node{
			core.Scroll{
				Style: core.Style{Width: core.Pt(280), Background: core.RGBA(0x000000, 0.04)},
				Children: []core.Node{
					core.View{Style: core.Style{Padding: core.Symmetric(52, 8), Gap: 2}, Children: []core.Node{
						core.Each(threads, func(t model.Thread) int { return t.ID },
							func(cx *core.Scope, item func() model.Thread, _ func() int) core.Node {
								return threadRow(item, current, c)
							}),
					}},
				},
			},
			core.View{
				Style: core.Style{FlexGrow: 1, Padding: core.Symmetric(52, 24), Gap: 8},
				Children: []core.Node{
					core.Text{
						TestID:      "current-title",
						Style:       core.Style{FontSize: 20, FontWeight: core.FontWeightSemibold},
						ContentFunc: func() string { return threadTitle(current()) },
					},
					core.Text{
						Style:       core.Style{FontSize: 12, Color: core.RGBA(0x000000, 0.5)},
						ContentFunc: func() string { return current().Status },
					},
				},
			},
		},
	}
}

func threadRow(item func() model.Thread, current func() model.Thread, c Host) core.Node {
	// Rows are keyed by ID, so a row keeps the ID it was built with.
	id := fmt.Sprintf("thread-%d", item().ID)
	return core.View{
		Role: core.RoleButton,
		StyleFunc: func() core.Style {
			s := core.Style{Padding: core.Symmetric(6, 10), CornerRadius: 6}
			if current().ID == item().ID {
				s.Background = core.RGBA(0x000000, 0.08)
			}
			return s
		},
		OnPress: func(core.PressEvent) { c.SelectThread(item()) },
		Children: []core.Node{
			core.Text{TestID: id, MaxLines: 1, Style: core.Style{FontSize: 13}, ContentFunc: func() string { return threadTitle(item()) }},
		},
	}
}

func threadTitle(t model.Thread) string {
	if t.Title != "" {
		return t.Title
	}
	return "New conversation"
}
