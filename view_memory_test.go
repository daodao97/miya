package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/memory"
)

func TestMemoryManagementPageAndPromotionPreview(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			app, tt := testApp(t)
			c := app.c
			attachMemory(t, c)
			if dark {
				app.appearance = "dark"
			} else {
				app.appearance = "light"
			}
			e := completeMemory(t, c, "memorySave", memoryRequest{Kind: "fact", Content: "本周暂缓手机协议升级"}).(memory.Entry)
			list := completeMemory(t, c, "memoryList", memoryRequest{}).(memoryList)
			app.memoryRevisionSeen = c.memoryRevision
			app.section = "Settings"
			app.settingsTab = 4
			app.memories = memoryUI{scope: "project", project: c.cwd, list: list, loadedAt: time.Now()}
			tt.Frame()
			for _, want := range []string{"共享记忆", "当前项目", "用户偏好", "本周暂缓手机协议升级", "忘记", "编辑", "提升到项目文件…", "在当前项目使用记忆工具", "查询时包含用户偏好"} {
				if !tt.HasText(want) {
					t.Fatalf("missing %s: %q", want, tt.Texts())
				}
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-list-dark-%v", dark))
			if err := tt.Click("编辑"); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"编辑记忆", "保存记忆", "取消"} {
				if !tt.HasText(want) {
					t.Fatalf("missing form %s", want)
				}
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-form-dark-%v", dark))
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			app.memories.promotion = &memoryPromotion{File: "AGENTS.md", After: "长期约定\n"}
			app.memories.id = e.ID
			app.memories.version = e.Version
			app.memories.content = e.Content
			app.memories.file = "AGENTS.md"
			tt.Frame()
			if !tt.HasText("确认写入项目文件") {
				t.Fatalf("promotion lacked final confirmation: %q", tt.Texts())
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-promotion-dark-%v", dark))
		})
	}
}
func TestMemoryNoticeAlwaysVisibleAndContainsScopeAndUndo(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	attachMemory(t, c)
	e := completeMemory(t, c, "memorySave", memoryRequest{Scope: "user", Kind: "preference", Content: "偏好中文解释"}).(memory.Entry)
	notice := &memoryNotice{Scope: "user", ID: e.ID, Version: e.Version}
	c.setThreads([]Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, Messages: []Message{{ID: 10, Role: "user", Content: "记住"}, {ID: 11, Role: "tool", Kind: "commandExecution"}, {ID: 12, Role: "tool", Kind: "commandExecution"}, {ID: 13, Role: "memory", Memory: notice}}}})
	c.setActive(1)
	key := fmt.Sprintf("/%s/%s/%d", notice.Scope, notice.ID, notice.Version)
	app.memoryRevisionSeen = c.memoryRevision
	app.memoryNotes = map[string]*memoryNoticeUI{key: {entry: e, loadedAt: time.Now()}}
	tt.Frame()
	for _, want := range []string{"Miya 记忆 · 用户级偏好（跨项目）", "偏好中文解释", "撤销记忆"} {
		if !tt.HasText(want) {
			t.Fatalf("notice hidden: %q", tt.Texts())
		}
	}
	automationSnapshot(t, tt, "memory-notice")
	e = completeMemory(t, c, "memoryForget", memoryRequest{Scope: "user", ID: e.ID, Version: e.Version}).(memory.Entry)
	app.memoryRevisionSeen = c.memoryRevision
	app.memoryNotes[key] = &memoryNoticeUI{entry: e, loadedAt: time.Now()}
	tt.Frame()
	if tt.HasText("偏好中文解释") || tt.HasText("撤销记忆") || !tt.HasText("已忘记") {
		t.Fatal("forgotten notice retained plaintext")
	}
}
func TestMobileMemoryReadOnlyPageAndDesktopIsolation(t *testing.T) {
	m := testMobileApp()
	m.state.Memory = true
	m.memories = mobileMemory{project: "/a", scope: "project", requestedAt: time.Now()}
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	list := memoryList{Entries: []memory.Entry{{ID: "memory", Version: 1, Content: "本周暂缓手机协议升级", Kind: "fact", State: "active", ObservedAt: at}}}
	data, _ := json.Marshal(phoneMemoryResult{Project: "/a", Scope: "project", List: list})
	m.receiveMemory(data, "")
	tt := ui.NewTester(m.viewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	if err := tt.Click("共享记忆"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"共享记忆", "本周暂缓手机协议升级", "当前项目", "用户偏好"} {
		if !tt.HasText(want) {
			t.Fatalf("mobile lacks %s: %q", want, tt.Texts())
		}
	}
	for _, forbidden := range []string{"编辑", "忘记", "撤销记忆", "保存记忆"} {
		if _, ok := tt.Find(forbidden); ok {
			t.Fatalf("mobile exposes %s", forbidden)
		}
	}
	automationSnapshot(t, tt, "memory-mobile")
	// A delayed response from another project must not populate this page.
	other, _ := json.Marshal(phoneMemoryResult{Project: "/b", Scope: "project", List: memoryList{}})
	m.receiveMemory(other, "")
	if len(m.memories.list.Entries) != 1 {
		t.Fatal("foreign response changed active page")
	}
	m.resetDesktopState()
	if len(m.memories.list.Entries) != 0 {
		t.Fatal("another desktop's memory retained")
	}
}

func TestMemoryRefreshReplacesOmittedSettingsAndOldEntries(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	attachMemory(t, c)
	callbacks := make(chan func(), 4)
	c.run = func(f func()) { callbacks <- f }
	app.memories = memoryUI{project: c.cwd, scope: "project", list: memoryList{Entries: []memory.Entry{{ID: "old", Content: "旧正文"}}, Settings: memory.Settings{UseUserPreferences: true}}}
	app.loadMemory()
	for app.memories.loading || c.memoryCalls > 0 {
		select {
		case f := <-callbacks:
			f()
		case <-time.After(5 * time.Second):
			t.Fatal("memory refresh did not finish")
		}
	}
	if app.memories.err != "" || len(app.memories.list.Entries) != 0 || app.memories.list.Settings.UseUserPreferences {
		t.Fatalf("refresh merged obsolete fields: %+v", app.memories)
	}
}
