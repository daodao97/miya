package integration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
	projectmodel "miya/internal/project/model"
)

func TestSidebarActivitySharedBetweenDesktopAndPhone(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetProjects([]projectmodel.Project{{Name: "current", Path: c.Cwd}, {Name: "other", Path: "/other"}})
	c.DesktopUnread = map[string]int64{}
	var threads []model.Thread
	for id := 1; id <= 7; id++ {
		threads = append(threads, model.Thread{ID: id, ServerID: fmt.Sprint(id), Busy: true, LastActivityAt: 100})
	}
	for id := 10; id <= 16; id++ {
		thread := model.Thread{ID: id, ServerID: fmt.Sprint(id), CompletedAtMs: 200, LastActivityAt: int64(id)}
		threads = append(threads, thread)
		c.DesktopUnread[appmodel.DesktopUnreadKey(thread)] = 200
	}
	threads = append(threads, model.Thread{ID: 30, Busy: true, Loading: true}, model.Thread{ID: 31})
	c.SetThreads(threads)
	other := []model.Thread{{ID: 20, ServerID: "other-running", Busy: true, LastActivityAt: 100}, {ID: 21, ServerID: "other-done", CompletedAtMs: 200}}
	c.ProjectCache = map[string][]model.Thread{"/other": other}
	c.DesktopUnread[appmodel.DesktopUnreadKey(other[1])] = 200
	m := testMobileApp()
	m.PushDisabled = true
	apply := func() {
		t.Helper()
		data, err := json.Marshal(c.PhoneSnapshot())
		if err != nil {
			t.Fatal(err)
		}
		var state phoneproto.State
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		m.ApplyPhoneState(state)
	}
	apply()
	running, completed := app.SidebarActivityThreads()
	phoneRunning, phoneCompleted := m.SidebarActivityThreads()
	if len(running) != 8 || len(completed) != 8 || !reflect.DeepEqual(running, phoneRunning) || !reflect.DeepEqual(completed, phoneCompleted) {
		t.Fatal("desktop and phone activity must share filtering, ordering and the complete cross-project lists")
	}
	if len(m.State.Notifications.Unread) != 0 || m.State.CompletionUnread == nil {
		t.Fatal("shared completions should arrive independently of push notification registration")
	}
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: "/other"}})
	apply()
	running, completed = app.SidebarActivityThreads()
	phoneRunning, phoneCompleted = m.SidebarActivityThreads()
	if !reflect.DeepEqual(running, phoneRunning) || !reflect.DeepEqual(completed, phoneCompleted) {
		t.Fatal("an unregistered current project should have the same activity order on both clients")
	}
	key := appmodel.DesktopUnreadKey(other[1])
	snapshot := c.PhoneSnapshot()
	snapshot.CompletionUnread[key] = 0
	if c.DesktopUnread[key] != 200 {
		t.Fatal("a phone snapshot must not alias the controller's unread state")
	}
	c.MarkDesktopRead(appmodel.DesktopRead{Key: key, Version: 200})
	apply()
	_, completed = m.SidebarActivityThreads()
	if len(completed) != 7 {
		t.Fatal("reading a desktop completion should remove it from the phone's activity too")
	}
	c.DesktopUnread = map[string]int64{}
	apply()
	_, completed = m.SidebarActivityThreads()
	if m.State.CompletionUnread == nil || len(completed) != 0 {
		t.Fatal("an empty shared unread map must survive the phone wire format")
	}
}

func TestMobileSidebarCompletionUnreadCompatibility(t *testing.T) {
	m := testMobileApp()
	thread := m.State.Projects[0].Threads[0]
	m.State.Notifications.Unread = []notification.Unread{{ServerID: thread.ServerID}}
	_, completed := m.SidebarActivityThreads()
	if len(completed) != 1 {
		t.Fatal("older desktops should still use per-phone unread completions")
	}
	m.State.CompletionUnread = map[string]int64{}
	_, completed = m.SidebarActivityThreads()
	if len(completed) != 0 {
		t.Fatal("stale push unread state must not override an authoritative shared completion state")
	}
	m.State.CompletionUnread[appmodel.DesktopUnreadKey(thread)] = 100
	m.State.Notifications.Unread = nil
	_, completed = m.SidebarActivityThreads()
	if len(completed) != 1 {
		t.Fatal("completion activity should work with push notifications disabled")
	}
}

func mobileActivityFixture() (first, second, done model.Thread) {
	return model.Thread{ID: 1, ServerID: "s1", Title: "Current project running", Busy: true, LastActivityAt: 10},
		model.Thread{ID: 2, ServerID: "s2", Title: "Other project running", Busy: true, LastActivityAt: 20},
		model.Thread{ID: 3, ServerID: "done", Title: "Other project result", CompletedAtMs: 100, LastActivityAt: 30}
}

func TestMobileDrawerActivityOpensAcrossProjects(t *testing.T) {
	m := testMobileApp()
	first, second, done := mobileActivityFixture()
	m.State.Projects[0].Threads = []model.Thread{first}
	m.State.Projects[1].Threads = []model.Thread{second, done}
	m.State.CompletionUnread = map[string]int64{appmodel.DesktopUnreadKey(done): 100}
	m.PushDisabled = true
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	runningHeader, running := tt.Find("In progress")
	completedHeader, completed := tt.Find("Completed")
	projectsHeader, _ := tt.Find("Projects")
	if !running || !completed || runningHeader.Y >= completedHeader.Y || completedHeader.Y >= projectsHeader.Y {
		t.Fatal("shared activity groups should appear above the phone's project list")
	}
	for _, thread := range []model.Thread{second, done} {
		if !m.Menu {
			openMobileDrawer(t, tt)
		}
		if err := tt.Click(thread.Title); err != nil {
			t.Fatal(err)
		}
		if m.Open != thread.ID || m.Selected != "/b" || m.Menu || m.Router().Path() != "/thread" {
			t.Fatal("activity should open the selected conversation in its own project and close the drawer")
		}
		tt.Advance(time.Second)
	}
	openMobileDrawer(t, tt)
	if err := tt.Click("Search conversations"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("In progress"); ok {
		t.Fatal("drawer search should show search results instead of duplicate activity sections")
	}
	if err := tt.Click("Close search"); err != nil {
		t.Fatal(err)
	}
	m.State.Projects[0].Threads[0].Busy = false
	m.State.Projects[1].Threads[0].Busy = false
	m.State.CompletionUnread = map[string]int64{}
	tt.Frame()
	for _, label := range []string{"In progress", "Completed"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("empty activity group %q should be hidden", label)
		}
	}
}

func TestMobileDrawerActivityFitsAccessibleLayouts(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, dark := range []bool{false, true} {
			for _, scale := range []float32{1, 1.8} {
				name := fmt.Sprintf("activity-%d-dark-%v-scale-%v", width, dark, scale)
				t.Run(name, func(t *testing.T) {
					m := testMobileApp()
					first, _, done := mobileActivityFixture()
					first.Title = "正在处理其他项目的长标题任务，点击即可查看执行进度"
					done.Title = "其他项目已经完成的长标题会话，点击即可查看完整结果"
					m.State.Projects[0].Threads = []model.Thread{first}
					m.State.Projects[1].Threads = []model.Thread{done}
					m.State.CompletionUnread = map[string]int64{appmodel.DesktopUnreadKey(done): 100}
					tt := ui.NewTester(m.ViewRoot, width, 852)
					tt.SetDark(dark)
					setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
					tt.SetPreferences(ui.Preferences{TextScale: scale})
					openMobileDrawer(t, tt)
					for _, title := range []string{first.Title, done.Title} {
						row, ok := tt.Find(title)
						if !ok || row.H < 44 || row.X < 0 || row.X+row.W > m.DrawerW+1 {
							t.Fatalf("activity row is missing, too small or outside the drawer: %v", row)
						}
					}
					saveMobileNativeSnapshot(t, tt, name)
				})
			}
		}
	}
}
