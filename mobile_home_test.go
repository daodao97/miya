package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/notification"
)

func mobileHomeFixture() *mobileApp {
	m := testMobileApp()
	m.state.Projects[0].Threads = []Thread{
		{ID: 1, ServerID: "recent", Title: "整理项目文档", LastActivityAt: 500},
		{ID: 2, ServerID: "approval", Title: "更新部署配置", LastActivityAt: 100},
		{ID: 3, ServerID: "older", Title: "修复登录问题", Agent: agentClaude, LastActivityAt: 200},
	}
	m.state.Projects[1].Threads = []Thread{
		{ID: 4, ServerID: "busy", Title: "实现手机端对话列表", Busy: true, LastActivityAt: 300},
		{ID: 5, ServerID: "unread", Title: "补充接口测试", Agent: agentClaude, LastActivityAt: 400},
	}
	m.state.Approvals = []codex.Approval{{ID: json.RawMessage(`1`), ThreadID: "approval"}}
	m.state.Notifications.Unread = []notification.Unread{{ServerID: "unread", Agent: agentClaude}}
	return m
}

func TestMobileDrawerGroupsConversationsByProject(t *testing.T) {
	m := mobileHomeFixture()
	// Cached sessions should remain visible while reconnecting too.
	m.connected, m.status = false, "Connecting to desktop…"
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	var previous float32 = -1
	for _, label := range []string{"Project proj-a", "整理项目文档", "修复登录问题", "更新部署配置", "Project proj-b"} {
		r, ok := tt.Find(label)
		if !ok || r.Y <= previous {
			t.Fatalf("conversation %q is missing or out of order: %v", label, r)
		}
		previous = r.Y
	}
	if m.state.Projects[0].Threads[0].ServerID != "recent" {
		t.Fatal("drawer sorting changed the desktop's thread order")
	}
	if err := tt.Click("Project proj-b"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("补充接口测试") || !tt.HasText("实现手机端对话列表") || tt.HasText("整理项目文档") {
		t.Fatal("the drawer did not show the selected project's cached conversations")
	}
	if err := tt.Click("实现手机端对话列表"); err != nil {
		t.Fatal(err)
	}
	if m.open != 4 || m.router().Path() != "/thread" || m.view.c.current().ServerID != "busy" {
		t.Fatal("drawer did not open the conversation from the other project")
	}
}

func TestMobileHomeProjectSelectionKeepsDraft(t *testing.T) {
	m := testMobileApp()
	m.state.Projects[1].Threads = nil
	m.showProject("/b")
	tt := ui.NewTester(m.viewRoot, 320, 568)
	tt.Frame()
	if !tt.HasText("Let’s build") || m.composerProject() != "/b" || tt.HasText("thread-one") {
		t.Fatal("home should compose in the selected project without showing conversations")
	}
	m.draft = "保留这条草稿"
	if err := tt.Click("Select project"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("proj-a"); err != nil {
		t.Fatal(err)
	}
	if m.composerProject() != "/a" || m.draft != "保留这条草稿" || m.router().Path() != "/" {
		t.Fatal("changing the draft's project lost the draft or left the composer")
	}
	openMobileDrawer(t, tt)
	if err := tt.Click("Project proj-b"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("No conversations yet") || m.draft != "保留这条草稿" {
		t.Fatal("an empty project should show its state in the drawer and keep the draft")
	}
}

func TestMobileDrawerSwitchesConversations(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if m.open != 1 || m.menu || tt.HasText("Desktop connections") {
		t.Fatal("the conversation should close the drawer and show only the arrow navigation button")
	}
	m.draft = "当前对话的草稿"
	openMobileDrawer(t, tt)
	if err := tt.Click("thread-one"); err != nil {
		t.Fatal(err)
	}
	if m.menu || m.draft != "当前对话的草稿" {
		t.Fatal("selecting the current conversation should only close the drawer")
	}
	tt.Advance(time.Second)
	openMobileDrawer(t, tt)
	if err := tt.Click("Project proj-b"); err != nil {
		t.Fatal(err)
	}
	if m.open != 1 || m.composerProject() != "/a" || m.draft != "当前对话的草稿" {
		t.Fatal("expanding another project changed the open conversation or its draft")
	}
	if err := tt.Click("thread-two"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if m.menu || m.open != 2 || m.selected != "/b" || len(m.router().History()) != 2 {
		t.Fatal("switching conversations should reuse the conversation page and close the drawer")
	}
	openMobileDrawer(t, tt)
	if err := tt.Click("New thread"); err != nil {
		t.Fatal(err)
	}
	if m.open != 0 || m.router().Path() != "/" || m.composerProject() != "/b" || !tt.HasText("Let’s build") {
		t.Fatal("New thread should return to the home composer in the selected project")
	}
}

func TestMobileDrawerShowsMoreConversations(t *testing.T) {
	m := testMobileApp()
	for i := 2; i <= 8; i++ {
		m.state.Projects[0].Threads = append(m.state.Projects[0].Threads, Thread{ID: i + 10, Title: fmt.Sprintf("对话 %d", i), LastActivityAt: int64(i)})
	}
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if !tt.HasText("对话 8") || tt.HasText("thread-one") || !tt.HasText("Expand") {
		t.Fatal("the drawer should initially show the five newest conversations")
	}
	if err := tt.Click("Expand"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("thread-one") || !tt.HasText("Collapse") {
		t.Fatal("expanding the list should reveal older conversations")
	}
	if err := tt.Click("Collapse"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("thread-one") || !tt.HasText("Expand") {
		t.Fatal("collapsing the list should restore the compact list")
	}
}

func TestMobileDrawerSearchesCollapsedProjects(t *testing.T) {
	m := mobileHomeFixture()
	m.draft = "保留首页草稿"
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("Search conversations"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search conversations"); err != nil {
		t.Fatal(err)
	}
	tt.Type("不存在的标题")
	if !tt.HasText("No matching conversations") || m.draft != "保留首页草稿" {
		t.Fatal("search should show an empty state and leave the composer draft alone")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("手机")
	if !tt.HasText("实现手机端对话列表") || tt.HasText("整理项目文档") {
		t.Fatal("search should find conversations in collapsed projects")
	}
	saveMobileNativeSnapshot(t, tt, "drawer-search")
	if err := tt.Click("实现手机端对话列表"); err != nil {
		t.Fatal(err)
	}
	if m.open != 4 || m.router().Path() != "/thread" || m.menu || m.drawerSearch || m.drawerQuery != "" {
		t.Fatal("selecting a search result should open the conversation and reset search")
	}
}

func TestMobileDrawerSettingsKeepsConnectionControls(t *testing.T) {
	m := testMobileApp()
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if tt.HasText("Reconnect") || tt.HasText("更换桌面端") {
		t.Fatal("connection management should be inside settings")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if m.menu || m.router().Path() != "/settings" || !tt.HasText("Reconnect") || !tt.HasText("Manage desktops") || !tt.HasText("Add desktop") || !tt.HasText("mac") {
		t.Fatal("settings should preserve the desktop's connection controls")
	}
}

func TestMobileDrawerFooterAtLargeTextSize(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := mobileHomeFixture()
		tt := ui.NewTester(m.viewRoot, width, 844)
		tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
		tt.SetPreferences(ui.Preferences{TextScale: 1.8})
		openMobileDrawer(t, tt)
		newThread, newOK := tt.Find("New thread")
		settings, settingsOK := tt.Find("Settings")
		if !newOK || !settingsOK || newThread.W < 44 || newThread.H < 44 || settings.W < 44 || settings.H < 44 || newThread.X+newThread.W > settings.X || settings.X+settings.W > m.drawerW || settings.Y+settings.H > 810 {
			t.Fatalf("footer controls overlap or leave the safe area at width %d: new=%v settings=%v", width, newThread, settings)
		}
		saveMobileNativeSnapshot(t, tt, fmt.Sprintf("drawer-large-text-%d", width))
	}
}

// MIYA_SNAPSHOT=dir go test -run TestMobileHomeSnapshot captures both themes.
func TestMobileHomeSnapshot(t *testing.T) {
	dir := os.Getenv("MIYA_SNAPSHOT")
	if dir == "" {
		t.Skip("set MIYA_SNAPSHOT")
	}
	for _, dark := range []bool{false, true} {
		m := mobileHomeFixture()
		tt := ui.NewTester(m.viewRoot, 390, 844)
		tt.SetDark(dark)
		tt.SetSafeArea(ui.SafeArea{Top: 59, Bottom: 34})
		tt.Frame()
		name := "mobile-home-light"
		if dark {
			name = "mobile-home-dark"
		}
		saveMobileNativeSnapshot(t, tt, name)
		openMobileDrawer(t, tt)
		saveMobileNativeSnapshot(t, tt, name+"-drawer")
	}
}
