package main

import (
	json "encoding/json"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	memory "miya/internal/memory"
	extractortest "miya/internal/memory/extractor/extractortest"
	manager "miya/internal/memory/manager"
	mobile "miya/internal/mobile"
	desktopui "miya/internal/ui"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

func TestMemoryManagementPageAndPromotionPreview(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			attachMemory(t, c)
			if dark {
				app.Appearance = "dark"
			} else {
				app.Appearance = "light"
			}
			e := completeMemory(t, c, "memorySave", manager.Request{Kind: "fact", Content: "本周暂缓手机协议升级"}).(memory.Entry)
			list := completeMemory(t, c, "memoryList", manager.Request{}).(manager.List)
			app.MemoryRevisionSeen = c.MemoryRevision
			app.Section = "Settings"
			app.SettingsTab = 4
			app.Memories = desktopui.MemoryUI{Scope: "project", Project: c.Cwd, List: list, LoadedAt: time.Now()}
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
			app.Memories.Promotion = &manager.Promotion{File: "AGENTS.md", After: "长期约定\n"}
			app.Memories.Id = e.ID
			app.Memories.Version = e.Version
			app.Memories.Content = e.Content
			app.Memories.File = "AGENTS.md"
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
	c := app.C.(*controller.Controller)
	attachMemory(t, c)
	c.ClaudeBinary = "configured-claude"
	d := &testDispatch{}
	c.Run = d.push
	list := completeMemory(t, c, "memoryList", manager.Request{}).(manager.List)
	d.drain()
	app.Section = "Settings"
	app.SettingsTab = 4
	app.MemoryRevisionSeen = c.MemoryRevision
	app.Memories = desktopui.MemoryUI{Scope: "project", Project: c.Cwd, List: list, LoadedAt: time.Now()}
	tt.Frame()
	if err := tt.Click("Memory extraction agent: Codex"); err != nil {
		t.Fatal(err)
	}
	scope, _ := memory.ProjectScope(c.Cwd)
	waitCodexResume(t, c, d, func() bool {
		_, settings, _, err := c.Memories().Store.List(scope)
		return err == nil && settings.ExtractionAgent() == appmodel.AgentCodex && !app.Memories.Busy
	})
	_, settings, _, _ := c.Memories().Store.List(scope)
	if settings.AutoExtract || settings.AutoInject {
		t.Fatal("choosing extractor unexpectedly enabled automation")
	}
	tt.Frame()
	d.drain()
}

func TestMemoryExtractionFailureReasonVisible(t *testing.T) {
	app, tt := testApp(t)
	attachMemory(t, app.C.(*controller.Controller))
	app.Section, app.SettingsTab = "Settings", 4
	app.Memories = desktopui.MemoryUI{Scope: "project", Project: app.C.(*controller.Controller).Cwd, LoadedAt: time.Now(), List: manager.List{
		Extraction: memory.ExtractionStatus{Failed: 1, LastError: "Extraction input exceeds the limit. Skipped this turn."},
	}}
	tt.Frame()
	if !tt.HasText("Latest extraction issue: Extraction input exceeds the limit. Skipped this turn.") {
		t.Fatalf("missing extraction diagnostic: %q", tt.Texts())
	}
}

func TestMemoryExplicitNoticeContainsScopeAndUndo(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	attachMemory(t, c)
	e := completeMemory(t, c, "memorySave", manager.Request{Scope: "user", Kind: "preference", Content: "偏好中文解释"}).(memory.Entry)
	notice := &appmodel.MemoryNotice{Explicit: true, Scope: "user", ID: e.ID, Version: e.Version}
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, Messages: []appmodel.Message{{ID: 10, Role: "user", Content: "记住"}, {ID: 11, Role: "tool", Kind: "commandExecution"}, {ID: 12, Role: "tool", Kind: "commandExecution"}, {ID: 13, Role: "memory", Memory: notice}}}})
	c.SetActive(1)
	key := fmt.Sprintf("/%s/%s/%d", notice.Scope, notice.ID, notice.Version)
	app.MemoryRevisionSeen = c.MemoryRevision
	app.MemoryNotes = map[string]*desktopui.MemoryNoticeUI{key: {Entry: e, LoadedAt: time.Now()}}
	tt.Frame()
	for _, want := range []string{"Miya memory · User preferences (across projects)", "偏好中文解释", "Undo memory change"} {
		if !tt.HasText(want) {
			t.Fatalf("notice hidden: %q", tt.Texts())
		}
	}
	automationSnapshot(t, tt, "memory-notice")
	e = completeMemory(t, c, "memoryForget", manager.Request{Scope: "user", ID: e.ID, Version: e.Version}).(memory.Entry)
	app.MemoryRevisionSeen = c.MemoryRevision
	app.MemoryNotes[key] = &desktopui.MemoryNoticeUI{Entry: e, LoadedAt: time.Now()}
	tt.Frame()
	if tt.HasText("偏好中文解释") || tt.HasText("Undo memory change") || !tt.HasText("Forgotten") {
		t.Fatal("forgotten notice retained plaintext")
	}
}

func TestMemoryLegacyAutomaticCardsStayOutOfConversation(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	legacy := appmodel.Message{ID: 12, Role: "memory", Kind: "memory", Memory: &appmodel.MemoryNotice{Scope: "project", ID: "old", Version: 1}}
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, Messages: []appmodel.Message{
		{ID: 10, Role: "user", Content: "项目背景"}, {ID: 11, Role: "assistant", Content: "正常回答"}, legacy,
	}}})
	c.SetActive(1)
	tt.Frame()
	if !tt.HasText("正常回答") || tt.HasText("Miya memory · Current project") || tt.HasText("Loading memory…") {
		t.Fatalf("legacy automatic card still occupies the timeline: %q", tt.Texts())
	}
	groups := appmodel.GroupMessages([]appmodel.Message{legacy})
	if len(groups) != 0 {
		t.Fatal("hidden memory created an empty response or activity count")
	}
	if len(c.ThreadWithID(1).Messages) != 3 {
		t.Fatal("presentation filter mutated history")
	}
}

func TestMobileMemoryReadOnlyPageAndDesktopIsolation(t *testing.T) {
	m := testMobileApp()
	m.State.Memory = true
	m.Memories = mobile.MobileMemory{Project: "/a", Scope: "project", RequestedAt: time.Now()}
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	list := manager.List{Entries: []memory.Entry{{ID: "memory", Version: 1, Content: "本周暂缓手机协议升级", Kind: "fact", State: "active", ObservedAt: at}}}
	data, _ := json.Marshal(appmodel.PhoneMemoryResult{Project: "/a", Scope: "project", List: list})
	m.ReceiveMemory(data, "")
	tt := ui.NewTester(m.ViewRoot, 390, 844)
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
	other, _ := json.Marshal(appmodel.PhoneMemoryResult{Project: "/b", Scope: "project", List: manager.List{}})
	m.ReceiveMemory(other, "")
	if len(m.Memories.List.Entries) != 1 {
		t.Fatal("foreign response changed active page")
	}
	m.ResetDesktopState()
	if len(m.Memories.List.Entries) != 0 {
		t.Fatal("another desktop's memory retained")
	}
}

func TestMemoryRefreshReplacesOmittedSettingsAndOldEntries(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	attachMemory(t, c)
	callbacks := make(chan func(), 4)
	c.Run = func(f func()) { callbacks <- f }
	app.Memories = desktopui.MemoryUI{Project: c.Cwd, Scope: "project", List: manager.List{Entries: []memory.Entry{{ID: "old", Content: "旧正文"}}, Settings: memory.Settings{UseUserPreferences: true}}}
	app.LoadMemory()
	for app.Memories.Loading || c.Memories().Calls > 0 {
		select {
		case f := <-callbacks:
			f()
		case <-time.After(5 * time.Second):
			t.Fatal("memory refresh did not finish")
		}
	}
	if app.Memories.Err != "" || len(app.Memories.List.Entries) != 0 || app.Memories.List.Settings.UseUserPreferences {
		t.Fatalf("refresh merged obsolete fields: %+v", app.Memories)
	}
}
