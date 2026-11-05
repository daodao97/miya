package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/memory"
	"miya/internal/memory/extractor/extractortest"
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
			for _, want := range []string{"Shared memory", "Current project", "User preferences", "本周暂缓手机协议升级", "Forget", "Edit", "Promote to project file…", "Use memory tools in this project", "Include user preferences in searches", "Automatically recall relevant memories when sending", "Automatically extract memories after each turn"} {
				if !tt.HasText(want) {
					t.Fatalf("missing %s: %q", want, tt.Texts())
				}
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-list-dark-%v", dark))
			tt.Scroll(700, 500, 0, 400)
			tt.Frame()
			if err := tt.Click("Edit"); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Edit memory", "Save memory", "Cancel"} {
				if !tt.HasText(want) {
					t.Fatalf("missing form %s", want)
				}
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-form-dark-%v", dark))
			if err := tt.Click("Cancel"); err != nil {
				t.Fatal(err)
			}
			app.memories.promotion = &memoryPromotion{File: "AGENTS.md", After: "长期约定\n"}
			app.memories.id = e.ID
			app.memories.version = e.Version
			app.memories.content = e.Content
			app.memories.file = "AGENTS.md"
			tt.Frame()
			if !tt.HasText("Confirm writing to project file") {
				t.Fatalf("promotion lacked final confirmation: %q", tt.Texts())
			}
			automationSnapshot(t, tt, fmt.Sprintf("memory-promotion-dark-%v", dark))
		})
	}
}

func TestMemoryExtractorPickerSavesAgentWithoutEnablingAutomation(t *testing.T) {
	extractortest.FakeCodex(t)
	app, tt := testApp(t)
	c := app.c
	attachMemory(t, c)
	c.claudeBinary = "configured-claude"
	d := &testDispatch{}
	c.run = d.push
	list := completeMemory(t, c, "memoryList", memoryRequest{}).(memoryList)
	d.drain()
	app.section = "Settings"
	app.settingsTab = 4
	app.memoryRevisionSeen = c.memoryRevision
	app.memories = memoryUI{scope: "project", project: c.cwd, list: list, loadedAt: time.Now()}
	tt.Frame()
	if err := tt.Click("Memory extraction agent: Codex"); err != nil {
		t.Fatal(err)
	}
	scope, _ := memory.ProjectScope(c.cwd)
	waitCodexResume(t, c, d, func() bool {
		_, settings, _, err := c.memory.List(scope)
		return err == nil && settings.ExtractionAgent() == agentCodex && !app.memories.busy
	})
	_, settings, _, _ := c.memory.List(scope)
	if settings.AutoExtract || settings.AutoInject {
		t.Fatal("choosing extractor unexpectedly enabled automation")
	}
	tt.Frame()
	d.drain()
}

func TestMemoryExtractionFailureReasonVisible(t *testing.T) {
	app, tt := testApp(t)
	attachMemory(t, app.c)
	app.section, app.settingsTab = "Settings", 4
	app.memories = memoryUI{scope: "project", project: app.c.cwd, loadedAt: time.Now(), list: memoryList{
		Extraction: memory.ExtractionStatus{Failed: 1, LastError: "Extraction input exceeds the limit. Skipped this turn."},
	}}
	tt.Frame()
	if !tt.HasText("Latest extraction issue: Extraction input exceeds the limit. Skipped this turn.") {
		t.Fatalf("missing extraction diagnostic: %q", tt.Texts())
	}
}
func TestMemoryExplicitNoticeContainsScopeAndUndo(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	attachMemory(t, c)
	e := completeMemory(t, c, "memorySave", memoryRequest{Scope: "user", Kind: "preference", Content: "偏好中文解释"}).(memory.Entry)
	notice := &MemoryNotice{Explicit: true, Scope: "user", ID: e.ID, Version: e.Version}
	c.setThreads([]Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, Messages: []Message{{ID: 10, Role: "user", Content: "记住"}, {ID: 11, Role: "tool", Kind: "commandExecution"}, {ID: 12, Role: "tool", Kind: "commandExecution"}, {ID: 13, Role: "memory", Memory: notice}}}})
	c.setActive(1)
	key := fmt.Sprintf("/%s/%s/%d", notice.Scope, notice.ID, notice.Version)
	app.memoryRevisionSeen = c.memoryRevision
	app.memoryNotes = map[string]*memoryNoticeUI{key: {entry: e, loadedAt: time.Now()}}
	tt.Frame()
	for _, want := range []string{"Miya memory · User preferences (across projects)", "偏好中文解释", "Undo memory change"} {
		if !tt.HasText(want) {
			t.Fatalf("notice hidden: %q", tt.Texts())
		}
	}
	automationSnapshot(t, tt, "memory-notice")
	e = completeMemory(t, c, "memoryForget", memoryRequest{Scope: "user", ID: e.ID, Version: e.Version}).(memory.Entry)
	app.memoryRevisionSeen = c.memoryRevision
	app.memoryNotes[key] = &memoryNoticeUI{entry: e, loadedAt: time.Now()}
	tt.Frame()
	if tt.HasText("偏好中文解释") || tt.HasText("Undo memory change") || !tt.HasText("Forgotten") {
		t.Fatal("forgotten notice retained plaintext")
	}
}

func TestMemoryLegacyAutomaticCardsStayOutOfConversation(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	legacy := Message{ID: 12, Role: "memory", Kind: "memory", Memory: &MemoryNotice{Scope: "project", ID: "old", Version: 1}}
	c.setThreads([]Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, Messages: []Message{
		{ID: 10, Role: "user", Content: "项目背景"}, {ID: 11, Role: "assistant", Content: "正常回答"}, legacy,
	}}})
	c.setActive(1)
	tt.Frame()
	if !tt.HasText("正常回答") || tt.HasText("Miya memory · Current project") || tt.HasText("Loading memory…") {
		t.Fatalf("legacy automatic card still occupies the timeline: %q", tt.Texts())
	}
	groups := groupMessages([]Message{legacy})
	if len(groups) != 0 {
		t.Fatal("hidden memory created an empty response or activity count")
	}
	if len(c.threadWithID(1).Messages) != 3 {
		t.Fatal("presentation filter mutated history")
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
	if err := tt.Click("Shared memory"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Shared memory", "本周暂缓手机协议升级", "Current project", "User preferences"} {
		if !tt.HasText(want) {
			t.Fatalf("mobile lacks %s: %q", want, tt.Texts())
		}
	}
	for _, forbidden := range []string{"Edit", "Forget", "Undo memory change", "Save memory"} {
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
