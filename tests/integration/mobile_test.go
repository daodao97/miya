package integration

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/mobile"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/widgets"
)

func testMobileApp() *mobile.App {
	m := mobile.NewMobileApp()
	m.Loaded, m.Link, m.Connected, m.Host = true, "miya://connect", true, "mac"
	m.State = phoneproto.State{ReferenceSource: testReferenceSource, Projects: []model.ProjectSnapshot{
		{Name: "proj-a", Path: "/a", Current: true, Threads: []model.Thread{{ID: 1, Title: "thread-one", ServerID: "s1"}}},
		{Name: "proj-b", Path: "/b", Threads: []model.Thread{{ID: 2, Title: "thread-two", ServerID: "s2"}}},
	}}
	// Ordinary interaction tests disable motion; navigation motion has its
	// own coverage with the production router defaults.
	m.Router().Transition = ui.TransitionNone
	return m
}

func openMobileDrawer(t *testing.T, tt *ui.Tester) {
	t.Helper()
	label := "Desktop connections"
	if _, ok := tt.Find("Open sidebar"); ok {
		label = "Open sidebar"
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
}

func TestMobileHomeStartsNewConversation(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	for _, label := range []string{"Miya", "More", "Let’s build", "Message", "Send", "Select project"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("the new-conversation page should show %q", label)
		}
	}
	for _, label := range []string{"New thread", "thread-one", "thread-two", "全部对话"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("conversation navigation belongs in the drawer: %s", label)
		}
	}
	m.Draft = "未发送的消息"
	openMobileDrawer(t, tt)
	if err := tt.Click("New thread"); err != nil {
		t.Fatal(err)
	}
	if m.Router().Path() != "/" || m.ComposerProject() != "/a" || m.Draft != "" || !tt.HasText("Let’s build") {
		t.Fatal("new conversation should reset the draft on the home page")
	}
}

func TestMobileOpensConversationAndDrawer(t *testing.T) {
	m := testMobileApp()
	m.State.Approvals = nil
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.Open != 1 || m.Router().Path() != "/thread" {
		t.Fatalf("open = %d, path = %q; want the conversation page", m.Open, m.Router().Path())
	}
	if _, ok := tt.Find("Codex · proj-a"); !ok {
		t.Error("the header should name the agent and the project")
	}
	if _, ok := tt.Find("Send"); !ok {
		t.Error("the conversation page should have a send button")
	}
	if _, ok := tt.Find("Project files"); ok {
		t.Fatal("project files should be inside the conversation's more menu")
	}
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Project files") || !tt.HasText("New conversation") {
		t.Fatal("the conversation's more menu should offer files and a new conversation")
	}
	tt.Key(0, ui.KeyEscape)
	openMobileDrawer(t, tt)
	if m.Open != 1 || m.Router().Path() != "/thread" || !m.Menu {
		t.Fatalf("opening the drawer left the conversation: open = %d, path = %q", m.Open, m.Router().Path())
	}
	if err := tt.Click("New thread"); err != nil {
		t.Fatal(err)
	}
	if m.Open != 0 || m.Router().Path() != "/" {
		t.Fatal("New thread should return to the home composer")
	}
}

func TestMobileDarkAppearance(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.SetDark(true)
	tt.Frame()
	if !m.Dark || m.Pal != mobile.DarkPalette || widgets.Canvas != mobile.DarkPalette.Bg {
		t.Fatalf("dark = %v; the phone should follow the system's dark appearance", m.Dark)
	}
	tt.SetDark(false)
	tt.Frame()
	if m.Dark || widgets.Canvas != mobile.LightPalette.Bg {
		t.Fatal("the phone should return to the light appearance")
	}
}

func TestMobileDrawer(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Desktop connections"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !m.Menu {
		t.Fatal("the menu button should open the drawer")
	}
	// Let it slide in before tapping inside it.
	tt.Advance(time.Second)
	tt.Frame()
	if r, ok := tt.Find("Settings"); !ok || r.X < 0 {
		t.Fatalf("the drawer should have slid in: %v, %v", r, ok)
	}
	if _, ok := tt.Find("Settings"); !ok {
		t.Error("the drawer should offer settings")
	}
	if !tt.HasText("thread-one") || tt.HasText("thread-two") {
		t.Fatal("only the expanded project's conversations should appear in the drawer")
	}
	if err := tt.Click("Project proj-b"); err != nil {
		t.Fatal(err)
	}
	if !m.Menu || m.Selected != "/b" || m.ExpandedProject != "/b" {
		t.Fatalf("choosing a project should expand it inside the drawer: menu = %v, selected = %q", m.Menu, m.Selected)
	}
	if !tt.HasText("thread-two") || tt.HasText("thread-one") {
		t.Fatal("switching projects did not replace the expanded conversation list")
	}
	if err := tt.Click("Project proj-b"); err != nil {
		t.Fatal(err)
	}
	if m.ExpandedProject != "" || tt.HasText("thread-two") {
		t.Fatal("tapping the open project should collapse its conversations")
	}
	if err := tt.Click("New thread"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if m.Menu || m.Router().Path() != "/" || m.ComposerProject() != "/b" || !tt.HasText("Let’s build") {
		t.Fatal("the new-conversation action should use the selected project")
	}
}

// drag moves a finger across the window at row y, from x0 to x1.
func drag(tt *ui.Tester, x0, x1, y float32) { dragOver(tt, x0, x1, y, 16*time.Millisecond) }

// dragOver drags in 8 steps of step each: slowly, the drawer settles by
// where it is; quickly, a throw carries it on.
func dragOver(tt *ui.Tester, x0, x1, y float32, step time.Duration) {
	tt.Press(x0, y)
	tt.Frame()
	for i := 1; i <= 8; i++ {
		tt.Advance(step)
		tt.Move(x0+(x1-x0)*float32(i)/8, y)
		tt.Frame()
	}
	tt.Release(x1, y)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
}

func TestMobileDrawerFollowsDrags(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	drag(tt, 20, 300, 400)
	if !m.Menu {
		t.Fatalf("dragging right should open the drawer (offset %v)", m.DrawerOffset)
	}
	if r, ok := tt.Find("Settings"); !ok || r.X < 0 {
		t.Fatalf("the drawer should show: %v, %v", r, ok)
	}
	drag(tt, 370, 60, 400)
	if m.Menu {
		t.Fatalf("dragging left should close the drawer (offset %v)", m.DrawerOffset)
	}
	dragOver(tt, 10, 60, 400, 120*time.Millisecond)
	if m.Menu {
		t.Fatal("a short, slow drag should settle closed")
	}
	dragOver(tt, 10, 90, 400, 8*time.Millisecond)
	if !m.Menu {
		t.Fatal("a short, quick throw should open the drawer, as on iOS")
	}
}

func TestMobileDrawerScrim(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.Menu = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	// The list is pushed right; a tap on it closes the drawer and opens
	// nothing.
	tt.ClickAt(370, 110)
	tt.Frame()
	if m.Menu || m.Open != 0 {
		t.Fatalf("a tap beside the drawer should only close it: menu = %v, open = %d", m.Menu, m.Open)
	}
}

// A finger leaves no pointer behind once it lifts, unlike a mouse.
func TestMobileDrawerScrimTouch(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.Menu = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Press(370, 500)
	tt.Frame()
	tt.Release(370, 500)
	tt.Move(-50, -50)
	tt.Frame()
	if m.Menu {
		t.Fatal("a tap on the scrim should close the drawer")
	}
}

// A tap that moves a few points is a drag to MyGo, yet still a tap.
func TestMobileDrawerScrimWobblyTap(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	m.Menu = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Press(360, 500)
	tt.Frame()
	tt.Advance(16 * time.Millisecond)
	tt.Move(354, 502)
	tt.Frame()
	tt.Release(354, 502)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if m.Menu {
		t.Fatal("a tap on the scrim that moves a little should still close the drawer")
	}
}
