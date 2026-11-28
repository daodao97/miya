package nsmobile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"miya/internal/chat/model"
	"miya/internal/interaction"
)

func TestPhoneDrawerOpensFromHeaderAndClosesOnScrim(t *testing.T) {
	app, m, _ := startPhone(t)
	// The drawer stays built, hidden while closed, so pulling it out builds nothing.
	if m.drawerActive.Get() || app.Exists("id:drawer-scrim") {
		t.Fatal("drawer shown before it was opened")
	}
	openPhoneDrawer(app)
	if !m.drawerOpen.Get() || !app.Exists("id:mobile-drawer") || !app.Exists("id:drawer-scrim") {
		t.Fatal("sidebar button did not open the drawer")
	}
	if off, w := m.drawerAt, drawerWidth(m.cx); off != float64(w) || w != 320 || !m.drawerActive.Get() {
		t.Fatalf("drawer settled at %v of %v", off, w)
	}
	app.Press("id:drawer-scrim")
	app.Advance(500 * time.Millisecond)
	if m.drawerOpen.Get() || m.drawerActive.Get() || m.drawerAt != 0 || app.Exists("id:drawer-scrim") {
		t.Fatal("scrim did not close the drawer")
	}
	// Back closes an open drawer before anything else.
	openPhoneDrawer(app)
	app.Back()
	app.Advance(500 * time.Millisecond)
	if m.drawerOpen.Get() || !app.Exists("id:mobile-home") {
		t.Fatal("back did not close the drawer")
	}
}

func TestPhoneDrawerOpensThreadAndCloses(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	if m.Open != 7 || m.Path != "/thread" || m.drawerOpen.Get() || app.Exists("id:drawer-scrim") {
		t.Fatal("thread row did not open the conversation and close the drawer")
	}
	if got := app.Find("id:mobile-title").Text; got != "Fix the phone" {
		t.Fatalf("title %q", got)
	}
	if got := app.Find("id:mobile-subtitle").Text; !strings.Contains(got, "Work") {
		t.Fatalf("subtitle %q", got)
	}
	openPhoneDrawer(app)
	app.Press("id:drawer-new-thread")
	app.Advance(500 * time.Millisecond)
	if m.Open != 0 || !app.Exists("id:mobile-home") {
		t.Fatal("New thread did not return to the new-conversation page")
	}
}

func TestPhoneDrawerShowsAutomationThreadsInProjectHistory(t *testing.T) {
	app, m, sent := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.State.WebPorts = true
		m.State.Projects[0].Threads = []model.Thread{
			{ID: 1, Title: "Review logs", LastActivityAt: 7, Automation: "check"},
			{ID: 2, Title: "Publish notes", LastActivityAt: 6},
			{ID: 3, Title: "Verify settings", LastActivityAt: 5},
			{ID: 4, Title: "Build preview", LastActivityAt: 4},
			{ID: 5, Title: "Review tests", LastActivityAt: 3},
			{ID: 6, Title: "Daily report", LastActivityAt: 2, Automation: "daily", Agent: "codex", ServerID: "daily-report"},
		}
	})
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Locale: "zh-Hans",
		SafeArea: core.Edges{Top: 47, Bottom: 34}})
	changePhone(app, m, func() { m.Connected, m.Error = true, "" })
	openPhoneDrawer(app)
	if app.Exists("id:project-web-/work") || app.Exists("id:automation-runs-/work") {
		t.Fatal("unexpected action in the project conversation list")
	}
	if !app.Exists("id:automations") || !app.Exists("id:thread-1") || app.Exists("id:thread-6") {
		t.Fatal("project did not show its recent conversations in order")
	}
	checkToggleAlignment := func() {
		t.Helper()
		icon := app.Find("id:thread-1").Children[0]
		label := app.Find("id:more-/work").Children[0]
		if labelX, iconX := label.Bounds().Origin.X, icon.Bounds().Origin.X; labelX != iconX {
			t.Fatalf("%q starts at %v, conversation icon starts at %v", label.Text, labelX, iconX)
		}
	}
	checkToggleAlignment()
	captureNativePage(app, "drawer-project-conversations")
	app.Press("id:more-/work")
	checkToggleAlignment()
	app.Press("id:thread-6")
	if r := awaitRequest(t, app, sent, "subscribe"); r.Thread != 6 || r.ServerID != "daily-report" {
		t.Fatalf("opening the older automation conversation subscribed to %#v", r)
	}
	app.Advance(500 * time.Millisecond)
	app.Press("id:conversation-actions")
	app.WaitFor("id:action-web", 2*time.Second)
}

func TestPhoneSettingsPushesAndPops(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	app.Type("id:mobile-composer", "keep this draft")
	before := app.Find("id:mobile-composer").ID
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Advance(500 * time.Millisecond)
	if m.pages.Top() != "settings" || m.drawerOpen.Get() || app.Find("id:page-settings").Screen.Title != "Settings" {
		t.Fatalf("settings not pushed: %q", m.pages.Top())
	}
	app.Press("id:language")
	if m.pages.Top() != "language" || m.pages.Depth() != 2 {
		t.Fatalf("language not pushed over settings: %q %d", m.pages.Top(), m.pages.Depth())
	}
	app.Back()
	if m.pages.Top() != "settings" {
		t.Fatalf("back button went to %q", m.pages.Top())
	}
	app.Back()
	if m.pages.Top() != "" || m.pages.Depth() != 0 {
		t.Fatalf("system back left %q", m.pages.Top())
	}
	if m.Open != 7 || m.Draft != "keep this draft" || app.Find("id:mobile-composer").ID != before {
		t.Fatal("pages changed the conversation under them")
	}
}

func TestPhoneDrawerHandsOffCleanlyToSettingsSwipeBack(t *testing.T) {
	app, m, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.Open, m.Path = 7, "/thread"
		m.Draft = "Draft under settings"
	})
	editor := app.Find("id:mobile-composer").ID
	viewport := app.Find("id:page-root").Frame.Size
	stateOnPush := make(chan bool, 1)
	app.Post(func() {
		m.cx.Effect(func() {
			if m.pages.Depth() == 1 {
				select {
				case stateOnPush <- m.drawerActive.GetUntracked():
				default:
				}
			}
		})
	})
	app.DragAt("id:drawer-edge", core.Point{X: 5, Y: 80}, core.Point{X: 80}, core.Point{X: 280})
	app.WaitFor("id:drawer-scrim", time.Second)
	app.Advance(450 * time.Millisecond)
	app.Press("id:settings")
	app.WaitFor("id:page-settings", time.Second)
	select {
	case active := <-stateOnPush:
		if active {
			t.Error("settings pushed while the conversation still had a drawer transform")
		}
	case <-time.After(time.Second):
		t.Fatal("settings push was not observed")
	}
	screen := app.Find("id:page-settings")
	if screen.Frame.Size != viewport {
		t.Errorf("the navigation viewport changed on push: %+v to %+v", viewport, screen.Frame.Size)
	}
	if !screen.Screen.CanGoBack || screen.Screen.NoPopOnSwipe || app.Exists("id:drawer-edge") {
		t.Fatal("settings should own the back swipe while the drawer gesture is suspended")
	}
	app.Advance(450 * time.Millisecond)
	captureNativePage(app, "settings-after-drawer")
	app.Back()
	app.WaitForGone("id:page-settings", 2*time.Second)
	if m.drawerActive.GetUntracked() || app.Exists("id:drawer-scrim") || app.Find("id:mobile-composer").ID != editor || m.Draft != "Draft under settings" {
		t.Fatal("returning from settings changed the conversation or left the drawer visible")
	}
	app.Advance(450 * time.Millisecond)
	captureNativePage(app, "conversation-after-settings")
}

func TestPhoneConversationShowsOnlyItsApprovals(t *testing.T) {
	app, m, sent := startPhone(t)
	var own, other, global interaction.Request
	for raw, into := range map[string]*interaction.Request{
		`{"ID":1,"Method":"item/commandExecution/requestApproval","command":"go test","threadId":"session"}`:  &own,
		`{"ID":2,"Method":"item/commandExecution/requestApproval","command":"rm -rf","threadId":"elsewhere"}`: &other,
		`{"ID":3,"Method":"item/commandExecution/requestApproval","command":"ls"}`:                            &global,
	} {
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			t.Fatal(err)
		}
	}
	changePhone(app, m, func() {
		m.State.DesktopControl = true
		m.State.Projects = append(m.State.Projects, model.ProjectSnapshot{Name: "Other", Path: "/other", Threads: []model.Thread{{ID: 8, ServerID: "elsewhere", Title: "Other task"}}})
		m.State.Approvals = []interaction.Request{own, other, global}
	})
	openPhoneThread(t, app, m, sent)
	if !app.Exists("id:review-1") || !app.Exists("id:review-3") || app.Exists("id:review-2") {
		t.Fatal("conversation showed another conversation's request")
	}
	// A command approval is no request for the desktop.
	if app.Exists("id:desktop-preview-open") {
		t.Fatal("remote desktop offered for an approval")
	}
	changePhone(app, m, func() { m.State.Approvals = []interaction.Request{other} })
	if app.Exists("id:review-2") {
		t.Fatal("other conversation's request shown")
	}
	openPhoneDrawer(app)
	app.Press("id:project-/other")
	if !app.Exists("id:thread-8") || !app.Exists("text:Pending approval") {
		t.Fatalf("drawer did not mark the waiting conversation: %s", phoneTexts(app))
	}
}
