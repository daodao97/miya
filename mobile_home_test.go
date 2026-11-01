package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
)

func mobileHomeFixture() *mobileApp {
	m := testMobileApp()
	m.state.Projects[0].Threads = []Thread{
		{ID: 1, ServerID: "recent", Title: "整理项目文档", LastActivityAt: 500},
		{ID: 2, ServerID: "approval", Title: "更新部署配置", LastActivityAt: 100},
		{ID: 3, ServerID: "older", Title: "修复登录问题", Agent: agentClaude, LastActivityAt: 200},
	}
	m.state.Projects[1].Threads = []Thread{
		{ID: 4, ServerID: "busy", Title: "实现手机端会话列表", Busy: true, LastActivityAt: 300},
		{ID: 5, ServerID: "unread", Title: "补充接口测试", Agent: agentClaude, LastActivityAt: 400},
	}
	m.state.Approvals = []codex.Approval{{ID: json.RawMessage(`1`), ThreadID: "approval"}}
	m.state.Notifications.Unread = []phoneUnread{{ServerID: "unread", Agent: agentClaude}}
	return m
}

func TestMobileHomePrioritizesActionAcrossProjects(t *testing.T) {
	m := mobileHomeFixture()
	// Cached sessions should remain visible while reconnecting too.
	m.connected, m.status = false, "正在连接桌面端…"
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	var previous float32 = -1
	for _, label := range []string{"更新部署配置", "实现手机端会话列表", "补充接口测试", "整理项目文档", "修复登录问题"} {
		r, ok := tt.Find(label)
		if !ok || r.Y <= previous {
			t.Fatalf("conversation %q is missing or out of order: %v", label, r)
		}
		previous = r.Y
	}
	if m.state.Projects[0].Threads[0].ServerID != "recent" {
		t.Fatal("home sorting changed the desktop's thread order")
	}
	if err := tt.Click("实现手机端会话列表"); err != nil {
		t.Fatal(err)
	}
	if m.open != 4 || m.router().Path() != "/thread" || m.view.c.current().ServerID != "busy" {
		t.Fatal("home did not open the conversation from the other project")
	}
}

func TestMobileHomeProjectFilterAndEmptyState(t *testing.T) {
	m := testMobileApp()
	m.state.Projects[1].Threads = nil
	m.showProject("/b")
	tt := ui.NewTester(m.viewRoot, 320, 568)
	tt.Frame()
	if !tt.HasText("还没有会话") || tt.HasText("thread-one") {
		t.Fatal("empty project should show an empty state without other projects' sessions")
	}
	if err := tt.Click("新会话"); err != nil {
		t.Fatal(err)
	}
	m.draft = "保留这条草稿"
	if err := tt.Click("选择项目"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("proj-a"); err != nil {
		t.Fatal(err)
	}
	if m.newProject != "/a" || m.draft != "保留这条草稿" || m.router().Path() != "/thread" {
		t.Fatal("changing the draft's project lost the draft or left the composer")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	openMobileDrawer(t, tt)
	if err := tt.Click("全部会话"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(drawerMotion.Spring.Duration * 2)
	tt.Frame()
	if m.selected != "" || !tt.HasText("thread-one") {
		t.Fatal("all conversations did not remove the project filter")
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
		tt.Frame()
		name := "mobile-home-light.png"
		if dark {
			name = "mobile-home-dark.png"
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, tt.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
