package nsmobile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func TestPhoneSettingsPushesAndPops(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	app.Type("id:mobile-composer", "keep this draft")
	before := app.Find("id:mobile-composer").ID
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Advance(500 * time.Millisecond)
	if m.pages.Top() != "settings" || m.drawerOpen.Get() || app.Find("id:page-title").Text != "Settings" {
		t.Fatalf("settings not pushed: %q", m.pages.Top())
	}
	app.Press("id:language")
	if m.pages.Top() != "language" || m.pages.Depth() != 2 {
		t.Fatalf("language not pushed over settings: %q %d", m.pages.Top(), m.pages.Depth())
	}
	app.Press("id:page-back")
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
