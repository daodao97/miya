package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func testMobileApp() *mobileApp {
	m := newMobileApp()
	m.loaded, m.link, m.connected, m.host = true, "miya://connect", true, "mac"
	m.state = phoneState{Projects: []phoneProject{
		{Name: "proj-a", Path: "/a", Current: true, Threads: []Thread{{ID: 1, Title: "thread-one", ServerID: "s1"}}},
		{Name: "proj-b", Path: "/b", Threads: []Thread{{ID: 2, Title: "thread-two"}}},
	}}
	// Ordinary interaction tests disable motion; navigation motion has its
	// own coverage with the production router defaults.
	m.router().Transition = ui.TransitionNone
	return m
}

func openMobileDrawer(t *testing.T, tt *ui.Tester) {
	t.Helper()
	label := "桌面端连接"
	if _, ok := tt.Find("打开侧边栏"); ok {
		label = "打开侧边栏"
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
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	for _, label := range []string{"Miya", "新建对话", "Let’s build", "消息", "发送", "选择项目"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("the new-conversation page should show %q", label)
		}
	}
	for _, label := range []string{"thread-one", "thread-two", "全部对话"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("conversation navigation belongs in the drawer: %s", label)
		}
	}
	m.draft = "未发送的消息"
	if err := tt.Click("新建对话"); err != nil {
		t.Fatal(err)
	}
	if m.router().Path() != "/" || m.composerProject() != "/a" || m.draft != "" || !tt.HasText("Let’s build") {
		t.Fatal("new conversation should reset the draft on the home page")
	}
}

func TestMobileOpensConversationAndDrawer(t *testing.T) {
	m := testMobileApp()
	m.state.Approvals = nil
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.open != 1 || m.router().Path() != "/thread" {
		t.Fatalf("open = %d, path = %q; want the conversation page", m.open, m.router().Path())
	}
	if _, ok := tt.Find("Codex · proj-a"); !ok {
		t.Error("the header should name the agent and the project")
	}
	if _, ok := tt.Find("发送"); !ok {
		t.Error("the conversation page should have a send button")
	}
	if _, ok := tt.Find("项目文件"); ok {
		t.Fatal("project files should be inside the conversation's more menu")
	}
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("项目文件") || !tt.HasText("新对话") {
		t.Fatal("the conversation's more menu should offer files and a new conversation")
	}
	tt.Key(0, ui.KeyEscape)
	openMobileDrawer(t, tt)
	if m.open != 1 || m.router().Path() != "/thread" || !m.menu {
		t.Fatalf("opening the drawer left the conversation: open = %d, path = %q", m.open, m.router().Path())
	}
	if err := tt.Click("新建对话"); err != nil {
		t.Fatal(err)
	}
	if m.open != 0 || m.router().Path() != "/" {
		t.Fatal("New thread should return to the home composer")
	}
}

func TestMobileDarkAppearance(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.SetDark(true)
	tt.Frame()
	if !m.dark || m.pal != darkPalette || canvas != darkPalette.bg {
		t.Fatalf("dark = %v; the phone should follow the system's dark appearance", m.dark)
	}
	tt.SetDark(false)
	tt.Frame()
	if m.dark || canvas != lightPalette.bg {
		t.Fatal("the phone should return to the light appearance")
	}
}

func TestMobileDrawer(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("桌面端连接"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !m.menu {
		t.Fatal("the menu button should open the drawer")
	}
	// Let it slide in before tapping inside it.
	tt.Advance(time.Second)
	tt.Frame()
	if r, ok := tt.Find("设置"); !ok || r.X < 0 {
		t.Fatalf("the drawer should have slid in: %v, %v", r, ok)
	}
	if _, ok := tt.Find("设置"); !ok {
		t.Error("the drawer should offer settings")
	}
	if !tt.HasText("thread-one") || tt.HasText("thread-two") {
		t.Fatal("only the expanded project's conversations should appear in the drawer")
	}
	if err := tt.Click("项目 proj-b"); err != nil {
		t.Fatal(err)
	}
	if !m.menu || m.selected != "/b" || m.expandedProject != "/b" {
		t.Fatalf("choosing a project should expand it inside the drawer: menu = %v, selected = %q", m.menu, m.selected)
	}
	if !tt.HasText("thread-two") || tt.HasText("thread-one") {
		t.Fatal("switching projects did not replace the expanded conversation list")
	}
	if err := tt.Click("项目 proj-b"); err != nil {
		t.Fatal(err)
	}
	if m.expandedProject != "" || tt.HasText("thread-two") {
		t.Fatal("tapping the open project should collapse its conversations")
	}
	if err := tt.Click("新建对话"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if m.menu || m.router().Path() != "/" || m.composerProject() != "/b" || !tt.HasText("Let’s build") {
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
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	drag(tt, 20, 300, 400)
	if !m.menu {
		t.Fatalf("dragging right should open the drawer (offset %v)", m.drawerOffset)
	}
	if r, ok := tt.Find("设置"); !ok || r.X < 0 {
		t.Fatalf("the drawer should show: %v, %v", r, ok)
	}
	drag(tt, 370, 60, 400)
	if m.menu {
		t.Fatalf("dragging left should close the drawer (offset %v)", m.drawerOffset)
	}
	dragOver(tt, 10, 60, 400, 120*time.Millisecond)
	if m.menu {
		t.Fatal("a short, slow drag should settle closed")
	}
	dragOver(tt, 10, 90, 400, 8*time.Millisecond)
	if !m.menu {
		t.Fatal("a short, quick throw should open the drawer, as on iOS")
	}
}

func TestMobileDrawerScrim(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	m.menu = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	// The list is pushed right; a tap on it closes the drawer and opens
	// nothing.
	tt.ClickAt(370, 110)
	tt.Frame()
	if m.menu || m.open != 0 {
		t.Fatalf("a tap beside the drawer should only close it: menu = %v, open = %d", m.menu, m.open)
	}
}

// A finger leaves no pointer behind once it lifts, unlike a mouse.
func TestMobileDrawerScrimTouch(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	m.menu = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	tt.Press(370, 500)
	tt.Frame()
	tt.Release(370, 500)
	tt.Move(-50, -50)
	tt.Frame()
	if m.menu {
		t.Fatal("a tap on the scrim should close the drawer")
	}
}

// A tap that moves a few points is a drag to MyGo, yet still a tap.
func TestMobileDrawerScrimWobblyTap(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	m.menu = true
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
	if m.menu {
		t.Fatal("a tap on the scrim that moves a little should still close the drawer")
	}
}
