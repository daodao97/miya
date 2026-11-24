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

// The Bots page lists the Bots like contacts and opens one as a chat, with
// the window's composer; opening an ordinary conversation leaves the page.
func TestBotsPage(t *testing.T) {
	app, full := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()

	side := ui.NewTester(app.SidebarView, 260, 1000)
	side.Frame()
	if err := side.Click("Bots"); err != nil {
		t.Fatalf("no Bots entry in the sidebar: %v (%v)", err, side.Texts())
	}
	if app.Section != "Bots" {
		t.Fatalf("section = %q, want Bots", app.Section)
	}
	full.Frame()
	if !full.HasText("Pick a Bot to chat with, or create one.") {
		t.Fatalf("no empty state before the first Bot: %v", full.Texts())
	}
	if _, ok := full.Find("New Bot"); !ok {
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
	full.Frame()

	// Each contact is the name over the last reply, else the charter.
	for _, label := range []string{"Rin", "Earlier reply", "Kai", "Writes release notes", "Mio", "No messages yet"} {
		if !full.HasText(label) {
			t.Fatalf("missing %q in %v", label, full.Texts())
		}
	}
	if full.HasText("Reviews pull requests") {
		t.Fatal("the charter is shown beside the last reply")
	}
	side.Frame()
	if side.HasText("Rin") {
		t.Fatal("the sidebar still lists the Bots")
	}

	// The contact carries the state of the Bot's conversation.
	if _, ok := full.Find("Unread: Rin"); ok {
		t.Fatal("a read conversation shows the unread dot")
	}
	c.DesktopUnread[appmodel.DesktopUnreadKey(standing)] = 100
	full.Frame()
	if _, ok := full.Find("Unread: Rin"); !ok {
		t.Fatalf("an unread reply has no dot on the Bot: %v", full.Texts())
	}
	c.Update(5, func(t *model.Thread) {
		t.Busy = true
		t.Messages = []model.Message{{ID: 1, Role: "user", Content: "q"}, {ID: 2, Role: "assistant", Content: "All  done\nhere"}}
	})
	c.Update(5, func(t *model.Thread) { t.Busy = false })
	full.Frame()
	if !full.HasText("All done here") || full.HasText("Earlier reply") {
		t.Fatalf("the contact does not show the last reply: %v", full.Texts())
	}

	// A Bot opens as a chat on the page, with the window's composer.
	if err := full.Click("Rin"); err != nil {
		t.Fatal(err)
	}
	if c.Active() != 5 || app.Section != "Bots" {
		t.Fatalf("active conversation = %d in section %q, want Rin's 5 on the Bots page", c.Active(), app.Section)
	}
	if b, ok := c.CurrentBot(); !ok || b.Name != "Rin" {
		t.Fatalf("current Bot = %+v, %v", b, ok)
	}
	full.Frame()
	for _, want := range []string{"Message Rin", "Bot actions"} {
		if _, ok := full.Find(want); !ok {
			t.Fatalf("the chat has no %q: %v", want, full.Texts())
		}
	}
	if !full.HasText("q") || !full.HasText("All  done\nhere") && !full.HasText("All done here") {
		t.Fatalf("the chat does not show the conversation: %v", full.Texts())
	}

	// A Bot without a conversation shows who it is until the first message.
	if err := full.Click("Kai"); err != nil {
		t.Fatal(err)
	}
	if c.Active() != 0 || app.Section != "Bots" {
		t.Fatalf("active conversation = %d in section %q, want a new one on the Bots page", c.Active(), app.Section)
	}
	if b, ok := c.CurrentBot(); !ok || b.Name != "Kai" {
		t.Fatalf("current Bot = %+v, %v", b, ok)
	}
	full.Frame()
	if _, ok := full.Find("Message Kai"); !ok {
		t.Fatalf("the composer does not name the Bot of a new conversation: %v", full.Texts())
	}
	if !full.HasText("Your first message starts a conversation with this Bot.") {
		t.Fatalf("no introduction before the first message: %v", full.Texts())
	}

	// The search filters the contacts.
	app.Bots.Query = "release"
	full.Frame()
	if full.HasText("Mio") || !full.HasText("Writes release notes") {
		t.Fatalf("searching for release lists %v", full.Texts())
	}
	app.Bots.Query = ""

	// An ordinary conversation belongs to no Bot and leaves the page.
	side.Frame()
	if err := side.Click("Ordinary conversation"); err != nil {
		t.Fatal(err)
	}
	if b, ok := c.CurrentBot(); ok || app.Section != "" {
		t.Fatalf("an ordinary conversation: Bot %q, section %q", b.Name, app.Section)
	}
	full.Frame()
	if _, ok := full.Find("Message Kai"); ok {
		t.Fatal("the composer still names the Bot after leaving its conversation")
	}
}
