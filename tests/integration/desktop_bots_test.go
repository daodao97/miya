package integration

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/controller"
)

func TestSidebarBots(t *testing.T) {
	app, full := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()

	tt := ui.NewTester(app.SidebarView, 260, 1000)
	tt.Frame()
	if !tt.HasText("Bots") {
		t.Fatalf("no Bots section before the first Bot: %v", tt.Texts())
	}
	if _, ok := tt.Find("New Bot"); !ok {
		t.Fatal("no button to add a Bot")
	}

	// As after a restart: the store names the session of Rin's conversation.
	path := filepath.Join(t.TempDir(), "bots.json")
	stored := bot.Store{Bots: []bot.Bot{{ID: "rin", Name: "Rin", Note: "Reviews pull requests", Agent: model.AgentCodex, Project: c.Cwd, Session: "srv5", Preview: "Earlier reply"}}}
	if err := stored.Save(path); err != nil {
		t.Fatal(err)
	}
	c.StartBots(path)
	for _, b := range []bot.Bot{
		{Name: "Kai", Note: "Writes release notes", Agent: model.AgentCodex, Project: c.Cwd},
		{Name: "Mio", Agent: model.AgentCodex, Project: c.Cwd},
	} {
		if _, err := c.BotOp("botSave", bot.Request{Bot: b}); err != nil {
			t.Fatalf("save %s: %v", b.Name, err)
		}
	}
	standing := model.Thread{ID: 5, ServerID: "srv5", Agent: model.AgentCodex, Title: "Standing conversation", Loaded: true, HistoryLoaded: true, Resumed: true}
	c.SetThreads([]model.Thread{standing, {ID: 6, ServerID: "srv6", Agent: model.AgentCodex, Title: "Ordinary conversation", Loaded: true, HistoryLoaded: true, Resumed: true}})
	tt.Frame()

	// Each row is the name over the last reply, else what the Bot is for.
	for _, label := range []string{"Rin", "Earlier reply", "Kai", "Writes release notes", "Mio", "No messages yet"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q in %v", label, tt.Texts())
		}
	}
	if tt.HasText("Reviews pull requests") {
		t.Fatal("the role note is shown beside the last reply")
	}
	bots, _ := tt.Find("Bots")
	rin, _ := tt.Find("Rin")
	projects, _ := tt.Find("Projects")
	if !(bots.Y < rin.Y && rin.Y < projects.Y) {
		t.Fatalf("Bots at %v, Rin at %v, Projects at %v: Bots should precede Projects", bots.Y, rin.Y, projects.Y)
	}

	// The row carries the state of the standing conversation.
	if _, ok := tt.Find("Unread: Rin"); ok {
		t.Fatal("a read conversation shows the unread dot")
	}
	c.DesktopUnread[appmodel.DesktopUnreadKey(standing)] = 100
	tt.Frame()
	if _, ok := tt.Find("Unread: Rin"); !ok {
		t.Fatalf("an unread reply has no dot on the Bot: %v", tt.Texts())
	}
	c.Update(5, func(t *model.Thread) {
		t.Busy = true
		t.Messages = []model.Message{{ID: 1, Role: "user", Content: "q"}, {ID: 2, Role: "assistant", Content: "All  done\nhere"}}
	})
	c.Update(5, func(t *model.Thread) { t.Busy = false })
	tt.Frame()
	if !tt.HasText("All done here") || tt.HasText("Earlier reply") {
		t.Fatalf("the row does not show the last reply: %v", tt.Texts())
	}

	// A Bot opens its standing conversation, like any other conversation.
	if err := tt.Click("Rin"); err != nil {
		t.Fatal(err)
	}
	if c.Active() != 5 {
		t.Fatalf("active conversation = %d, want Rin's 5", c.Active())
	}
	if b, ok := c.CurrentBot(); !ok || b.Name != "Rin" {
		t.Fatalf("current Bot = %+v, %v", b, ok)
	}
	full.Frame()
	if _, ok := full.Find("Message Rin"); !ok {
		t.Fatalf("the composer does not name the Bot: %v", full.Texts())
	}

	// A Bot without one opens the empty conversation its first message starts.
	if err := tt.Click("Kai"); err != nil {
		t.Fatal(err)
	}
	if c.Active() != 0 {
		t.Fatalf("active conversation = %d, want a new one", c.Active())
	}
	if b, ok := c.CurrentBot(); !ok || b.Name != "Kai" {
		t.Fatalf("current Bot = %+v, %v", b, ok)
	}
	full.Frame()
	if _, ok := full.Find("Message Kai"); !ok {
		t.Fatalf("the composer does not name the Bot of a new conversation: %v", full.Texts())
	}

	// An ordinary conversation belongs to no Bot.
	if err := tt.Click("Ordinary conversation"); err != nil {
		t.Fatal(err)
	}
	if b, ok := c.CurrentBot(); ok {
		t.Fatalf("an ordinary conversation belongs to %q", b.Name)
	}
	full.Frame()
	if _, ok := full.Find("Message Kai"); ok {
		t.Fatal("the composer still names the Bot after leaving its conversation")
	}
}
