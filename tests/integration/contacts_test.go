//go:build !windows

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/agent/claude/native/transcript"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/controller"
	"miya/internal/memory"
	projectmodel "miya/internal/project/model"
	"miya/internal/remote"
	"miya/internal/widgets"
	"miya/locales"
)

func contactOp(t *testing.T, c *controller.Controller, r contacts.Request) appmodel.ContactResult {
	t.Helper()
	if r.ID == "" {
		r.ID = fmt.Sprint("contact-", time.Now().UnixNano())
	}
	out, err := c.ContactOp(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestContactControllerDailyAndProjectsShareOneConversation(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	a, b := t.TempDir(), t.TempDir()
	c := serverController(a, "")
	c.SetProjects([]projectmodel.Project{{Name: "A", Path: a}, {Name: "B", Path: b}})
	saved := contactOp(t, c, contacts.Request{Op: "save", Profile: contacts.Profile{Name: "Ada", Agent: "codex", Persona: "Be precise"}})
	id := saved.ProfileID
	if id == "" {
		t.Fatal("creation omitted stable profile identity")
	}
	daily := contactOp(t, c, contacts.Request{Op: "open", BotID: id, Context: "daily"})
	pa := contactOp(t, c, contacts.Request{Op: "open", BotID: id, Context: "project", Project: a})
	pb := contactOp(t, c, contacts.Request{Op: "open", BotID: id, Context: "project", Project: b})
	if daily.Thread.ID == pa.Thread.ID || pa.Thread.ID == pb.Thread.ID {
		t.Fatal("different contexts reused a conversation")
	}
	if len(c.Projects()) != 2 || c.ThreadWorkspace(daily.Thread.ID) == a {
		t.Fatal("daily chat became a project")
	}
	if got := contactOp(t, c, contacts.Request{Op: "open", BotID: id, Context: "project", Project: a}); got.Thread.ID != pa.Thread.ID {
		t.Fatal("project selection duplicated a conversation")
	}
	if len(c.Threads()) != 1 || c.Threads()[0].ID != pa.Thread.ID {
		t.Fatal("project entry does not share contact conversation")
	}
	c.Update(daily.Thread.ID, func(t *model.Thread) { t.Busy = true; t.LastActivityAt = time.Now().UnixMilli() })
	c.Update(daily.Thread.ID, func(t *model.Thread) {
		t.Busy = false
		t.Messages = []model.Message{{Role: "assistant", Content: "A completed answer"}}
	})
	snapshot := c.ContactSnapshot()
	found := false
	for _, s := range snapshot.Sessions {
		if s.ID == daily.Thread.Contact.ID {
			found = s.Preview == "A completed answer"
		}
	}
	if !found {
		t.Fatal("completed answer missing from contact preview")
	}
	phone := c.PhoneSnapshot()
	if len(phone.Projects) != 2 || len(phone.ContactThreads) != 1 || len(phone.ContactThreads[0].Threads) != 1 {
		t.Fatalf("phone lost daily context or polluted projects: %+v", phone)
	}
	profile := snapshot.Profiles[0]
	profile.Persona = "A new role"
	contactOp(t, c, contacts.Request{Op: "save", Profile: profile})
	if c.ThreadWithID(pa.Thread.ID).Contact.Persona != "Be precise" {
		t.Fatal("editing a contact mutated an existing role")
	}
	if _, err := c.ContactOp(contacts.Request{ID: "unknown-project", Op: "open", BotID: id, Context: "project", Project: t.TempDir()}); err == nil {
		t.Fatal("unregistered project accepted")
	}
}

func TestContactNativeInputUsesFrozenPersonaAndUserText(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c, d, log := codexResumeFixture(t)
	saved := contactOp(t, c, contacts.Request{Op: "save", Profile: contacts.Profile{Name: "Researcher", Agent: "codex", Model: "fixture", Effort: "high", Persona: "Keep facts distinct from guesses"}})
	out := contactOp(t, c, contacts.Request{Op: "open", BotID: saved.ProfileID, Context: "daily"})
	query := "Please inspect this question"
	if got := c.Submit(model.SendRequest{ThreadID: out.Thread.ID, Text: query}); got != out.Thread.ID {
		t.Fatalf("send: %d %s", got, c.LastSubmitError)
	}
	waitCodexResume(t, c, d, func() bool { v := c.ThreadWithID(out.Thread.ID); return !v.Busy && len(v.Messages) > 2 })
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var input string
	var cwd string
	var effort string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call struct {
			Method string
			Params struct {
				Cwd    string
				Effort string
				Input  []struct{ Text string }
			}
		}
		_ = json.Unmarshal([]byte(line), &call)
		if call.Method == "turn/start" && len(call.Params.Input) > 0 {
			input, cwd, effort = call.Params.Input[0].Text, call.Params.Cwd, call.Params.Effort
		}
	}
	if !strings.Contains(input, "Keep facts distinct from guesses") || contacts.StripInput(input) != query || cwd != out.Workspace || effort != "high" {
		t.Fatalf("wrong native input: %q %q %q", input, cwd, effort)
	}
	texts := userTexts(c.ThreadWithID(out.Thread.ID))
	if len(texts) != 1 || texts[0] != query {
		t.Fatalf("profile leaked into user text: %q", texts)
	}
	if got := transcript.VisibleText([]transcript.Block{{Type: "text", Text: input}}); got != query {
		t.Fatal("profile leaked into native history", got)
	}
	if got := c.ContactSnapshot().Sessions[0].NativeID; got == "" {
		t.Fatal("native identity was not persisted")
	}
	identities := map[string]bool{c.ThreadWithID(out.Thread.ID).ServerID: true}
	for i := range 2 {
		persona := fmt.Sprintf("Independent role %d", i)
		saved := contactOp(t, c, contacts.Request{Op: "save", Profile: contacts.Profile{Name: fmt.Sprintf("Colleague %d", i), Agent: "codex", Model: "fixture", Persona: persona}})
		opened := contactOp(t, c, contacts.Request{Op: "open", BotID: saved.ProfileID, Context: "daily"})
		if c.Submit(model.SendRequest{ThreadID: opened.Thread.ID, Text: query}) != opened.Thread.ID {
			t.Fatal(c.LastSubmitError)
		}
		waitCodexResume(t, c, d, func() bool { v := c.ThreadWithID(opened.Thread.ID); return !v.Busy && len(v.Messages) > 2 })
		thread := c.ThreadWithID(opened.Thread.ID)
		if thread.ServerID == "" || identities[thread.ServerID] || thread.Contact.Persona != persona {
			t.Fatal("contacts shared native session or persona")
		}
		identities[thread.ServerID] = true
	}
}

func TestContactDailyMemoryNeverReadsProjectMemory(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := serverController(t.TempDir(), "")
	attachMemory(t, c)
	project, _ := memory.ProjectScope(c.Cwd)
	user := memory.UserScope()
	for _, scope := range []memory.Scope{project, user} {
		if err := c.Memories().Store.Configure(scope, memory.Settings{AutoInject: true}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	source := []memory.Source{{EvidenceKind: "manual", ObservedAt: now}}
	for _, entry := range []struct {
		scope      memory.Scope
		kind, text string
	}{{project, "fact", "The project launch code is PROJECT-SECRET"}, {user, "preference", "For launch questions prefer concise Chinese"}} {
		if _, err := c.Memories().Store.Save(entry.scope, memory.Mutation{Kind: entry.kind, Content: entry.text, ObservedAt: now, Sources: source}); err != nil {
			t.Fatal(err)
		}
	}
	tt := model.Thread{Contact: &contacts.Session{Daily: true, Name: "Friend", Persona: "Warm"}}
	input := c.Memories().PrepareInput(tt, model.SendRequest{Text: "launch questions"}, c.Cwd, "session-daily").Text("launch questions")
	if strings.Contains(input, "PROJECT-SECRET") || !strings.Contains(input, "concise Chinese") || !strings.Contains(input, "Warm") {
		t.Fatalf("daily memory scope: %q", input)
	}
}

func contactFixture() contacts.Snapshot {
	p := contacts.Profile{ID: "ada", Revision: 1, PersonaVersion: 2, Name: "Ada", Description: "Research and everyday ideas", Persona: "Be precise", Agent: "codex"}
	s := contacts.Session{ID: "daily-ada", BotID: p.ID, Name: p.Name, PersonaVersion: 1, Persona: "Earlier role", Agent: p.Agent, Daily: true, Workspace: "/daily/ada", NativeID: "ada-native", Title: "Daily ideas", Preview: "Let’s explore that", LastActivity: time.Now().UnixMilli()}
	return contacts.Snapshot{Version: 1, Revision: 4, Profiles: []contacts.Profile{p}, Sessions: []contacts.Session{s}}
}

func TestContactViewsListFormAndDetailsAtPhoneWidths(t *testing.T) {
	for _, width := range []int{320, 390, 860} {
		for _, page := range []string{"list", "form", "detail"} {
			t.Run(fmt.Sprintf("%d-%s", width, page), func(t *testing.T) {
				p := widgets.ContactUI{}
				snapshot := contactFixture()
				p.Use(snapshot)
				if page == "form" {
					p.Edit(snapshot.Profiles[0])
				}
				if page == "detail" {
					p.Detail = "ada"
				}
				v := widgets.ContactView{Snapshot: snapshot, Connected: true, Projects: []model.ProjectSnapshot{{Name: "Miya", Path: "/miya"}}, Engines: []widgets.ContactEngine{{ID: "codex", Name: "Codex", Models: []agent.ModelOption{{Value: "fixture", Label: "Fixture"}}}}, Ink: widgets.Ink, Muted: widgets.Muted, Border: widgets.Line}
				tt := ui.NewTester(func(c *ui.Context) {
					ui.Scroll(c).Fill().Children(func() { ui.Column(c).FillWidth().Padding(16).Children(func() { p.View(c, v) }) })
				}, width, 900)
				tt.Frame()
				label := "Chat with Ada"
				if page == "form" {
					label = "Name"
				}
				if page == "detail" {
					label = "Continue conversation"
				}
				if bounds, ok := tt.Find(label); !ok || bounds.X < 0 || bounds.X+bounds.W > float32(width) {
					t.Fatalf("missing or overflowing %s: %+v %s", label, bounds, tt.Texts())
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("contacts-%s-%d", page, width))
			})
		}
	}
}

func TestContactMobileDailyThreadAndSettingsAreIndependent(t *testing.T) {
	m := testMobileApp()
	m.State.Contacts = contactFixture()
	session := m.State.Contacts.Sessions[0]
	th := model.Thread{ID: 20, Agent: session.Agent, Contact: &session, ServerID: session.NativeID, Title: session.Title, LaunchModel: "fixture", LaunchEffort: "high", HistoryLoaded: true}
	m.State.ContactThreads = []model.ProjectSnapshot{{Name: "Daily chat", Path: session.Workspace, Threads: []model.Thread{th}}}
	m.Options = map[string]remote.SendOptions{"codex": {Model: "unrelated-project-model", Effort: "low"}}
	m.OpenThread(th)
	m.ShowThread(th)
	if m.ComposerProject() != session.Workspace || m.SendOptions("codex").Model != "fixture" || m.SendOptions("codex").Effort != "high" {
		t.Fatal("daily chat inherited project settings")
	}
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	if !tt.HasText("Daily chat") || !tt.HasText("Using an earlier persona") {
		t.Fatalf("missing contact context: %s", tt.Texts())
	}
	saveMobileNativeSnapshot(t, tt, "contacts-mobile-chat")
	m.Contacts.Detail = "ada"
	m.OpenDrawerPage("/contacts")
	tt.Frame()
	if !tt.HasText("Continue conversation") {
		t.Fatalf("contact navigation missing: %s", tt.Texts())
	}
}

func TestContactDesktopCreationAndSidebarEntry(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	app, tt := testApp(t)
	app.ShowContacts()
	tt.Frame()
	if err := tt.Click("New contact"); err != nil {
		t.Fatal(err)
	}
	label, ok := tt.Find("Name")
	if !ok {
		t.Fatal("name field missing")
	}
	tt.ClickAt(label.X+40, label.Y+label.H+18)
	tt.Type("Mira")
	if app.Contacts.Draft.Name != "Mira" {
		t.Fatalf("name input did not accept text: %q", app.Contacts.Draft.Name)
	}
	app.Contacts.Draft.Persona = "A thoughtful colleague"
	tt.Frame()
	if bounds, ok := tt.Find("Save contact"); !ok || bounds.Y+bounds.H > 820 {
		tt.Scroll(800, 600, 0, 1000)
		tt.Frame()
	}
	if err := tt.Click("Save contact"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Contacts.Err != "" || !tt.HasText("Mira") {
		t.Fatal("contact did not save", app.Contacts.Err, tt.Texts())
	}
	if err := tt.Click("Contact Mira"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.C.Current().Contact == nil || !app.C.Current().Contact.Daily || app.Section != "" {
		t.Fatal("sidebar did not open daily chat")
	}
	saveMobileNativeSnapshot(t, tt, "contacts-desktop-chat")
}

func TestContactChineseNarrowScreenAndOldBackend(t *testing.T) {
	previous := locales.Current()
	locales.SetCurrent("zh-Hans")
	defer locales.SetCurrent(previous)
	m := testMobileApp()
	m.State.Contacts = contactFixture()
	m.OpenDrawerPage("/contacts")
	tt := ui.NewTester(m.ViewRoot, 320, 844)
	tt.SetLocale("zh-Hans")
	if _, ok := tt.Find("新建联系人"); !ok {
		t.Fatal("contact page was not translated", tt.Texts())
	}
	saveMobileNativeSnapshot(t, tt, "contacts-mobile-zh")
	m.State.Contacts = contacts.Snapshot{}
	tt.Frame()
	if !tt.HasText("请更新 Miya 后台服务以使用联系人") {
		t.Fatal("old background service was not explained", tt.Texts())
	}
}
