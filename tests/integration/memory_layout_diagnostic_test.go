package integration

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"miya/internal/controller"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/mobile"
	desktopui "miya/internal/ui"
	"strings"
	"testing"
	"time"
)

func TestMemoryLayoutDiagnostic(t *testing.T) {
	content := strings.Repeat("手机与桌面应当正常显示项目记忆，检查长段落换行及列表宽度。", 15)
	entries := []memory.Entry{}
	for i := 0; i < 12; i++ {
		entries = append(entries, memory.Entry{ID: fmt.Sprint(i), Version: 1, Kind: "fact", State: "active", Content: fmt.Sprint(i) + content, Sources: []memory.Source{{Quote: strings.Repeat("来源内容需要正常换行。", 30)}}})
	}
	list := manager.List{Entries: entries}
	t.Run("phone", func(t *testing.T) {
		m := testMobileApp()
		m.State.Memory = true
		m.Memories = mobile.MobileMemory{Project: "/a", Scope: "project", List: list, RequestedAt: time.Now()}
		m.OpenDrawerPage("/memory")
		tt := ui.NewTester(m.ViewRoot, 390, 844)
		tt.Frame()
		r, ok := tt.Find(entries[0].Content)
		t.Logf("body: %v found %v", r, ok)
		saveMobileNativeSnapshot(t, tt, "diagnostic-memory-mobile")
	})
	t.Run("desktop", func(t *testing.T) {
		app, tt := testApp(t)
		c := app.C.(*controller.Controller)
		app.Section = "Settings"
		app.SettingsTab = 3
		app.Memories = desktopui.MemoryUI{Project: c.Cwd, Scope: "project", List: list, LoadedAt: time.Now()}
		tt.Frame()
		r, ok := tt.Find(entries[0].Content)
		t.Logf("body: %v found %v", r, ok)
		tt.Scroll(700, 500, 0, 650)
		tt.Frame()
		automationSnapshot(t, tt, "diagnostic-memory-desktop")
	})
}
