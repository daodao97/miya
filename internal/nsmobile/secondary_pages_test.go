package nsmobile

import (
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/automation"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/desktop"
	"miya/internal/interaction"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/nsrequests"
	phonecache "miya/internal/phone/cache"
	phoneclient "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	webservice "miya/internal/runtime/web"
)

func TestMain(m *testing.M) { nsgotest.Main(m) }

func TestPhoneSettingsScrollIndicatorFadesAfterScrolling(t *testing.T) {
	app, _, _ := startPhone(t)
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Dark: true, Locale: "zh-Hans",
		SafeArea: core.Edges{Top: 47, Bottom: 34}})
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.WaitFor("id:page-settings", time.Second)
	app.Advance(450 * time.Millisecond)

	const scroll = "id:page-settings >> id:grouped-scroll"
	bar := func() *nsgotest.View {
		view := app.Find(scroll)
		if !view.Scroll.HideIndicator || view.Content.Height <= view.Frame.Size.Height {
			t.Fatal("expected the fading scrollbar on an overflowing settings page")
		}
		// The theme's bar lives inside a host pinned to the scroll viewport.
		host := view.Children[len(view.Children)-1]
		if len(host.Children) != 1 {
			t.Fatal("missing fading scrollbar")
		}
		return host.Children[0]
	}
	if got := bar(); got.Props.PointerEvents != core.PointerEventsNone {
		t.Fatal("the idle scrollbar should let touches through")
	}
	app.Scroll(scroll, 160)
	app.Advance(160 * time.Millisecond)
	if bar().Props.PointerEvents != core.PointerEventsAuto || app.Find(scroll).Offset.Y <= 0 {
		t.Fatal("scrolling should reveal a draggable scrollbar")
	}
	captureNativePage(app, "settings-scrollbar-visible")
	app.Advance(1200 * time.Millisecond)
	if got := bar(); got.Props.PointerEvents != core.PointerEventsNone || got.Animated.Has(nsgotest.PropOpacity) && got.Animated.Opacity != 0 {
		t.Fatal("scrollbar still visible after scrolling stopped")
	}
	captureNativePage(app, "settings-scrollbar-idle")
}

func TestSecondaryPagesUseNativeNavigation(t *testing.T) {
	app, m, sent := startPhone(t)
	changePhone(app, m, func() {
		m.OpenThread(m.State.Projects[0].Threads[0])
		m.ShowThread(model.Thread{ID: 7, Agent: "codex", ServerID: "session", Title: "Review launch notes", HistoryLoaded: true, Messages: []model.Message{
			{ID: 1, Role: "user", Content: "Check the release notes."},
			{ID: 2, Role: "assistant", Content: "The notes are ready to review."},
		}})
		m.State.Memory, m.State.Automations, m.State.AutomationRun, m.State.AutomationCreate = true, true, true, true
		m.State.Runtime, m.State.WebPorts = true, true
		m.State.Collaboration = &model.CollaborationStatus{Version: 1}
		m.State.DesktopControl = true
		m.State.Agents = []phoneproto.AgentOptions{{Agent: "codex", Current: remote.SendOptions{Model: "codex", Effort: "medium", Policy: "on-request"},
			Models: []phoneproto.ModelOption{
				{Value: "codex", Label: "Codex", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
				{Value: "codex-mini", Label: "Codex Mini"},
			}}}
		m.Memories = phoneclient.Memory{Project: "/work", Scope: "project", RequestedAt: time.Now(), List: manager.List{
			Entries: []memory.Entry{{ID: "example", Kind: "preference", State: "active", Content: "Keep release notes short and focused."}},
		}}
		m.Autos = phoneclient.Automations{Loaded: true, RequestedAt: time.Now(), Detail: "review", List: []automation.View{
			{Automation: automation.Automation{ID: "review", Name: "Review release notes", Agent: "codex", Project: "/work",
				Schedule: "0 9 * * 1-5", Instructions: "Review the notes for the next release.", Enabled: true}},
		}}
		m.Desktops.Items = []phonecache.Desktop{{ID: "studio", Name: "Studio Mac", Link: m.Link}}
		m.Error = ""
		m.pages.Open("actions")
	})

	app.WaitFor("id:conversation-actions-sheet", time.Second)
	app.Advance(450 * time.Millisecond)
	if app.Find("id:action-memory").Frame.Size.Height < 44 {
		t.Fatal("conversation action has a small touch target")
	}
	captureNativePage(app, "01-conversation-actions")

	for _, page := range []struct {
		route, title, anchor, name string
		prepare                    func()
	}{
		{"memory", "Shared memory", "memory-scope", "02a-memory", nil},
		{"memory", "Shared memory", "memory-scope", "02b-memory-empty", func() { m.Memories.List = manager.List{} }},
		{"automations", "Automations", "automation-review", "03-automations", nil},
		{"automation", "Automations", "automation-run", "04-automation-detail", nil},
		{"automation-new", "New automation", "automation-request", "05-automation-form", m.OpenAutomationCreate},
		{"web-services", "Web services", "web-service-3000", "06-web-services", func() {
			m.web = &webServices{owner: m.composerOwner(), services: []webservice.Service{{Host: "localhost", Port: 3000}}}
		}},
		{"delegations", "Agent tasks", "", "07-agent-tasks", nil},
		{"desktops", "Desktops", "desktop-entry-studio", "08-desktops", nil},
		{"desktop-edit:studio", "Desktop name", "desktop-name-studio", "09-desktop-detail", nil},
		{"pair", "Add desktop", "pair-code", "10-pair", nil},
		{"projects", "Select project", "select-/work", "11-projects", nil},
		{"settings", "Settings", "language", "13-settings", nil},
		{"options", "Conversation settings", "", "14-options", nil},
		{"language", "Interface language", "language-en", "15-language", nil},
		{"voice", "Voice input", "voice-auto-send", "16-voice", nil},
		{"push", "Notifications", "", "17-notifications", nil},
		{"agent", "Agent", "", "18-agent", nil},
		{"model", "Model", "", "19-model", nil},
		{"effort", "Reasoning effort", "", "20-effort", nil},
		{"policy", "Permission mode", "", "21-permission", nil},
		{"request:22", "Pending approval", "", "22-approval", func() {
			m.State.Approvals = []interaction.Request{{ID: []byte("22"), Method: "item/commandExecution/requestApproval", Command: "go test ./..."}}
		}},
	} {
		t.Run(page.name, func(t *testing.T) {
			changePhone(app, m, func() {
				m.Connected, m.Error, m.Status = true, "", ""
				if page.prepare != nil {
					page.prepare()
				}
				m.pages.Open(page.route)
			})
			selector := "id:page-" + page.route
			app.WaitFor(selector, 2*time.Second)
			app.Advance(450 * time.Millisecond)
			root := app.Find(selector)
			if root.Screen.Title != page.title || !root.Screen.CanGoBack || root.Screen.NoPopOnSwipe {
				t.Fatalf("native header for %s: %+v", page.route, root.Screen)
			}
			if back := app.Find("id:page-back"); back.Frame.Size.Width < 44 || back.Frame.Size.Height < 44 {
				t.Fatalf("Back target is too small: %+v", back.Frame)
			}
			header := app.Find("id:page-header").Bounds()
			title := app.Find("id:page-title")
			bounds := title.Bounds()
			offset := bounds.Origin.X + bounds.Size.Width/2 - header.Origin.X - header.Size.Width/2
			if title.Text != page.title || offset < -1 || offset > 1 {
				t.Fatalf("title %q is offset by %v points in %s", title.Text, offset, page.route)
			}
			if page.route == "delegations" {
				r := awaitRequest(t, app, sent, "collaboration")
				changePhone(app, m, func() {
					m.ReceiveCollaboration(phoneJSON(map[string]any{"request_id": r.RequestID, "result": nsrequests.CollaborationListView{Tasks: []nsrequests.CollaborationTaskView{
						{ID: "task", Title: "Verify links", State: dg.Running, Agent: "codex", Progress: "Checking documentation links"},
					}}}), "")
				})
				app.WaitFor("id:collaboration-task-task", 2*time.Second)
				task := app.Find("id:collaboration-task-task").Bounds()
				if gap := task.Origin.Y - header.Origin.Y - header.Size.Height; gap < 0 || gap > 24 {
					t.Fatalf("first task starts %v points below the header", gap)
				}
			}
			if page.anchor != "" {
				content := app.Find("id:" + page.anchor)
				if content.Frame.Size.Width <= 0 || content.Frame.Size.Width > root.Frame.Size.Width {
					t.Fatalf("%s exceeds the screen: %+v", page.anchor, content.Frame)
				}
			}
			captureNativePage(app, page.name)
			if nsgotest.Target() == "" {
				app.Press("id:page-back")
			} else {
				changePhone(app, m, m.pages.Pop)
			}
			app.WaitFor("id:page-root", 2*time.Second)
			app.Advance(450 * time.Millisecond)
		})
	}

	t.Run("remote desktop controls", func(t *testing.T) {
		d := &phoneclient.MobileDesktopPage{Phase: "picker", Pickable: true, DisplaysAvailable: true, AppsAvailable: true,
			Windows: []desktop.Window{{ID: 1, PID: 1, App: "TextEdit", Title: "Release notes"}}}
		changePhone(app, m, func() { m.DesktopPage = d; m.Path = "/desktop" })
		app.WaitFor("id:desktop-picker-tabs", 2*time.Second)
		captureNativePage(app, "23-remote-picker")
		changePhone(app, m, func() { d.Phase = "live"; d.More = true })
		app.WaitFor("id:desktop-options-sheet", 2*time.Second)
		app.Advance(450 * time.Millisecond)
		captureNativePage(app, "24-remote-options")
		changePhone(app, m, func() { m.DesktopPage = nil; m.Path = "/thread" })
	})

	t.Run("dark Chinese headers", func(t *testing.T) {
		app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Dark: true, Locale: "zh-Hans",
			SafeArea: core.Edges{Top: 47, Bottom: 34}})
		for _, page := range []struct{ route, title string }{
			{"settings", "设置"}, {"language", "界面语言"}, {"delegations", "Agent 任务"}, {"automations", "自动化"},
		} {
			changePhone(app, m, func() {
				if page.route == "automations" {
					m.Autos.RequestedAt, m.Autos.Loading, m.Autos.Err = time.Now(), false, ""
				}
				m.pages.Open(page.route)
			})
			app.WaitFor("id:page-"+page.route, 2*time.Second)
			if page.route == "delegations" {
				r := awaitRequest(t, app, sent, "collaboration")
				changePhone(app, m, func() {
					m.ReceiveCollaboration(phoneJSON(map[string]any{"request_id": r.RequestID, "result": nsrequests.CollaborationListView{}}), "")
				})
				app.WaitFor("text:暂无协作任务", 2*time.Second)
			}
			app.Advance(450 * time.Millisecond)
			if title := app.Find("id:page-title").Text; title != page.title {
				t.Fatalf("title of %s = %q, want %q", page.route, title, page.title)
			}
			captureNativePage(app, "dark-"+page.route)
			app.Press("id:page-back")
			app.WaitForGone("id:page-"+page.route, 2*time.Second)
			app.Advance(450 * time.Millisecond)
		}
	})
}

func captureNativePage(app *nsgotest.App, name string) {
	if nsgotest.Target() == "ios_simulator" {
		app.Screenshot(name)
	}
}
