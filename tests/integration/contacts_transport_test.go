//go:build !windows

package integration

import (
	"encoding/json"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	"miya/internal/remote"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func TestContactPhoneCreatesAndRetriesAgainstSameBackgroundStore(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := serverController(t.TempDir(), "")
	conn, ex := serveTestPhone(t, c)
	call := func(r contacts.Request) appmodel.ContactResult {
		t.Helper()
		raw, _ := json.Marshal(r)
		conn.requests <- remote.Request{Op: "contacts", RequestID: r.ID, Contacts: raw}
		for {
			select {
			case f := <-conn.frames:
				if f.Type != "contacts" {
					continue
				}
				if f.Error != "" {
					t.Fatal(f.Error)
				}
				var out appmodel.ContactResult
				if err := json.Unmarshal(f.Data, &out); err != nil {
					t.Fatal(err)
				}
				if out.RequestID != r.ID {
					t.Fatalf("wrong response correlation: %s", out.RequestID)
				}
				return out
			case <-conn.ctx.Done():
				t.Fatal("contact phone request timed out")
				return appmodel.ContactResult{}
			}
		}
	}
	r := contacts.Request{ID: "phone-create-contact", Op: "save", Profile: contacts.Profile{Name: "Phone friend", Persona: "Friendly", Agent: "codex"}}
	one := call(r)
	two := call(r)
	if one.ProfileID == "" || one.ProfileID != two.ProfileID || len(two.State.Profiles) != 1 {
		t.Fatal("phone retry duplicated contact")
	}
	out := call(contacts.Request{ID: "phone-open-contact", Op: "open", BotID: one.ProfileID, Context: "daily"})
	if out.Thread.ID == 0 || out.Thread.Contact == nil || !out.Thread.Contact.Daily {
		t.Fatal("phone did not open daily context")
	}
	conn.requests <- remote.Request{Op: "subscribe", Thread: out.Thread.ID}
	if delta := conn.nextThread(t); delta.Thread.Contact == nil || delta.Thread.Contact.BotID != one.ProfileID {
		t.Fatal("subscription dropped contact identity")
	}
	ex.Do(func() {
		if len(c.ContactSnapshot().Profiles) != 1 || c.ThreadWithID(out.Thread.ID).Contact == nil {
			t.Error("phone wrote to a different store")
		}
	})
}

func TestContactOtherClientWorkspaceSwitchKeepsWindowContext(t *testing.T) {
	window := controller.NewMirrorController()
	defer window.Cancel()
	window.Remote = svcclient.NewClosedClient()
	defer window.Remote.Close()
	project := model.Thread{ID: 1, ServerID: "project-thread", Title: "Project task"}
	daily := model.Thread{ID: 2, ServerID: "daily-thread", Contact: &contacts.Session{ID: "role", BotID: "friend", Daily: true, Workspace: "/daily"}}
	window.ApplyState(svcproto.State{Cwd: "/project", Threads: []model.Thread{project}, ProjectThreads: map[string][]model.Thread{"/daily": {daily}}, Projects: []projectmodel.Project{{Name: "Project", Path: "/project"}}})
	window.SelectThread(project)
	window.ApplyState(svcproto.State{Cwd: "/daily", Threads: []model.Thread{daily}, ProjectThreads: map[string][]model.Thread{"/project": {project}}, Projects: []projectmodel.Project{{Name: "Project", Path: "/project"}}})
	if window.Cwd != "/project" || window.Current().ID != project.ID || window.ThreadWorkspace(project.ID) != "/project" {
		t.Fatal("another client's daily chat replaced the window's project context")
	}
}

func TestContactMirrorRestartResolvesStableSessionBeforeReusedNumber(t *testing.T) {
	window := controller.NewMirrorController()
	defer window.Cancel()
	window.Remote = svcclient.NewClosedClient()
	defer window.Remote.Close()
	role := contacts.Session{ID: "stable-contact-session", BotID: "friend", Daily: true, Workspace: "/daily"}
	old := model.Thread{ID: 1, ServerID: "original-native", Contact: &role}
	window.ApplyState(svcproto.State{ReferenceSource: "source", Cwd: "/daily", Threads: []model.Thread{old}})
	window.SelectThread(old)
	window.Mirror.Drafts[1] = "Unsent message for friend"
	renumbered := old
	renumbered.ID = 9
	unrelated := model.Thread{ID: 1, ServerID: "other-native", Title: "Different conversation"}
	window.ApplyState(svcproto.State{ReferenceSource: "source", Cwd: "/other", Threads: []model.Thread{unrelated}, ProjectThreads: map[string][]model.Thread{"/daily": {renumbered}}})
	if window.Current().ID != 9 || window.Cwd != "/daily" || window.Mirror.Drafts[9] != "Unsent message for friend" {
		t.Fatal("reconnect selected the reused number instead of stable contact identity")
	}
	if _, ok := window.Mirror.Drafts[1]; ok {
		t.Fatal("contact draft remained on a reused numeric slot")
	}
}

func TestContactServiceMirrorKeepsProjectAndDailyBindings(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	f := newServiceFixture(t)
	cl, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	r := contacts.Request{ID: "window-create-contact", Op: "save", Profile: contacts.Profile{Name: "Window friend", Agent: "codex", Persona: "Careful"}}
	var saved appmodel.ContactResult
	if err := cl.Call("contacts", r, &saved); err != nil {
		t.Fatal(err)
	}
	var opened appmodel.ContactResult
	if err := cl.Call("contacts", contacts.Request{ID: "window-open-contact", Op: "open", BotID: saved.ProfileID, Context: "daily"}, &opened); err != nil {
		t.Fatal(err)
	}
	window := f.window()
	f.win.Do(func() { window.AdoptContactResult(opened) })
	if opened.Thread.Contact == nil || opened.Workspace == "" {
		t.Fatal("service did not return contact binding")
	}
	f.serv.Do(func() {
		f.C.Update(opened.Thread.ID, func(t *model.Thread) { t.ServerID = "native-persisted"; t.Resumed = true })
	})
	f.serv.Do(func() {
		v := f.C.ContactSnapshot()
		if len(v.Sessions) != 1 || v.Sessions[0].NativeID != "native-persisted" {
			t.Error("native identity not durably bound")
		}
	})
}

func TestContactSavePublishesToOtherOpenWindows(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	f := newServiceFixture(t)
	first, second := f.window(), f.window()
	var created appmodel.ContactResult
	var err error
	f.win.Do(func() {
		created, err = first.ContactOp(contacts.Request{ID: "publish-contact-save", Op: "save", Profile: contacts.Profile{Name: "Visible in both windows", Agent: "codex"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		found := false
		f.win.Do(func() { _, found = second.ContactSnapshot().Find(created.ProfileID) })
		if found {
			return
		}
	}
	t.Fatal("saving a contact did not publish to the other open window")
}
