package controller

import (
	"path/filepath"
	"testing"

	agentpolicy "miya/internal/agent/policy"
	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	projectmodel "miya/internal/project/model"
	svcclient "miya/internal/service/client"
	"miya/internal/uiruntime"
)

// botTestController is a controller that owns its Bots, as the service does,
// with the open project at Cwd and the store at the returned path.
func botTestController(t *testing.T) (*Controller, string) {
	t.Helper()
	c := &Controller{}
	c.Cwd = t.TempDir()
	c.ProjectCache = map[string][]model.Thread{}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.Projects, c.SetProjects = uiruntime.CreateSignal([]projectmodel.Project{})
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.Revision, c.SetRevision = uiruntime.CreateSignal(0)
	path := filepath.Join(t.TempDir(), "bots.json")
	c.StartBots(path)
	return c, path
}

func saveTestBot(t *testing.T, c *Controller, b bot.Bot) bot.Bot {
	t.Helper()
	saved, err := c.BotOp("botSave", bot.Request{Bot: b})
	if err != nil {
		t.Fatalf("save %q: %v", b.Name, err)
	}
	return saved
}

func storedBot(t *testing.T, c *Controller, id string) bot.Bot {
	t.Helper()
	i, ok := c.bots.store.Find(id)
	if !ok {
		t.Fatalf("Bot %s is not in the store", id)
	}
	return c.bots.store.Bots[i]
}

func TestBotSaveChecksAgentAndProject(t *testing.T) {
	c, path := botTestController(t)
	other := t.TempDir()

	_, err := c.BotOp("botSave", bot.Request{Bot: bot.Bot{Name: "Rin", Agent: "no-such-agent", Project: c.Cwd}})
	if err == nil || err.Error() != tr("Choose an agent for the Bot") {
		t.Fatalf("unknown agent: %v", err)
	}
	_, err = c.BotOp("botSave", bot.Request{Bot: bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: other}})
	if err == nil || err.Error() != tr("Add the project to Miya before giving it a Bot") {
		t.Fatalf("unknown project: %v", err)
	}
	if len(c.bots.store.Bots) != 0 || c.Revision() != 0 {
		t.Fatalf("a rejected Bot changed the store: %+v, revision %d", c.bots.store.Bots, c.Revision())
	}

	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	saved := saveTestBot(t, c, bot.Bot{Name: "  Rin  ", Agent: model.AgentCodex, Project: other})
	if saved.ID == "" || saved.Name != "Rin" || saved.Project != other {
		t.Fatalf("saved Bot = %+v", saved)
	}
	if c.Revision() != 1 {
		t.Fatalf("revision = %d, want 1 so that windows get the new list", c.Revision())
	}
	if kept := bot.Load(path).Bots; len(kept) != 1 || kept[0].ID != saved.ID {
		t.Fatalf("file holds %+v", kept)
	}

	if _, err := c.BotOp("botSave", bot.Request{Bot: bot.Bot{Name: "rin", Agent: model.AgentCodex, Project: c.Cwd}}); err == nil {
		t.Fatal("a second Bot took a name in use")
	}
}

func TestBotEditKeepsConversationUnlessAgentOrProjectChanges(t *testing.T) {
	c, _ := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	c.bots.store.SetSession(b.ID, "srv1")
	c.bots.store.SetPreview(b.ID, "Done.")
	c.bots.pending[4] = b.ID

	edit := b
	edit.Note = "Reviews pull requests"
	edit.Session = "from-a-window" // the conversation is the service's to record
	saved := saveTestBot(t, c, edit)
	if saved.Note != "Reviews pull requests" || saved.Session != "srv1" || saved.Preview != "Done." {
		t.Fatalf("edited Bot = %+v", saved)
	}
	if c.bots.pending[4] != b.ID {
		t.Fatal("editing the note forgot the pending conversation")
	}

	edit.Agent = model.AgentClaude
	saved = saveTestBot(t, c, edit)
	if saved.Session != "" || saved.Preview != "" {
		t.Fatalf("another agent kept the old conversation: %+v", saved)
	}
	if len(c.bots.pending) != 0 {
		t.Fatalf("another agent kept the pending conversation: %v", c.bots.pending)
	}
}

func TestBotDeleteAndReset(t *testing.T) {
	c, path := botTestController(t)
	for _, op := range []string{"botDelete", "botReset"} {
		if _, err := c.BotOp(op, bot.Request{ID: "missing"}); err == nil || err.Error() != tr("This Bot no longer exists") {
			t.Fatalf("%s of a missing Bot: %v", op, err)
		}
	}
	if _, err := c.BotOp("botRename", bot.Request{}); err == nil {
		t.Fatal("an unknown operation was accepted")
	}

	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	c.bots.store.SetSession(b.ID, "srv1")
	c.bots.store.SetPreview(b.ID, "Done.")
	c.bots.pending[4] = b.ID
	if _, err := c.BotOp("botReset", bot.Request{ID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := storedBot(t, c, b.ID); got.Session != "" || got.Preview != "" || len(got.Others) != 1 || got.Others[0] != "srv1" {
		t.Fatalf("reset Bot = %+v, want srv1 among its other conversations", got)
	}
	// The conversation still waiting for its session stays the Bot's too.
	if len(c.bots.pending) != 0 || c.bots.tasks[4] != b.ID {
		t.Fatalf("after reset pending %v, other conversations waiting %v", c.bots.pending, c.bots.tasks)
	}

	c.bots.pending[5] = b.ID
	if _, err := c.BotOp("botDelete", bot.Request{ID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if len(c.bots.store.Bots) != 0 || len(c.bots.pending) != 0 || len(c.bots.tasks) != 0 {
		t.Fatalf("after delete: %+v, pending %v", c.bots.store.Bots, c.bots.pending)
	}
	if kept := bot.Load(path).Bots; len(kept) != 0 {
		t.Fatalf("file still holds %+v", kept)
	}
}

func TestBotsCarryPendingThreadAndWindowsUseTheServiceList(t *testing.T) {
	c, _ := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	c.bots.pending[7] = b.ID
	if got := c.Bots(); len(got) != 1 || got[0].Thread != 7 {
		t.Fatalf("Bots() = %+v, want the pending thread 7", got)
	}
	if storedBot(t, c, b.ID).Thread != 0 {
		t.Fatal("the store took the thread ID")
	}

	c.Remote = svcclient.NewClosedClient()
	c.bots.mirror = []bot.Bot{{ID: "m1", Name: "Mirror"}}
	if got := c.Bots(); len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("a window lists %+v, want the service's list", got)
	}
}

func TestBotThread(t *testing.T) {
	c, _ := botTestController(t)
	other := t.TempDir()
	c.SetThreads([]model.Thread{
		{ID: 1, ServerID: "srv1", Agent: model.AgentCodex},
		{ID: 2, ServerID: "srv1", Agent: model.AgentClaude},
		{ID: 3, Agent: model.AgentCodex},
	})
	c.ProjectCache[other] = []model.Thread{{ID: 9, ServerID: "srv9", Agent: model.AgentClaude}}

	for _, tc := range []struct {
		name string
		bot  bot.Bot
		want int
	}{
		{"no conversation", bot.Bot{Agent: model.AgentCodex, Project: c.Cwd}, 0},
		{"open project", bot.Bot{Agent: model.AgentCodex, Project: c.Cwd, Session: "srv1"}, 1},
		{"same session ID at another agent", bot.Bot{Agent: model.AgentClaude, Project: c.Cwd, Session: "srv1"}, 2},
		{"background project", bot.Bot{Agent: model.AgentClaude, Project: other, Session: "srv9"}, 9},
		{"session of another project", bot.Bot{Agent: model.AgentClaude, Project: c.Cwd, Session: "srv9"}, 0},
		{"pending thread", bot.Bot{Agent: model.AgentCodex, Project: c.Cwd, Thread: 3}, 3},
		{"pending thread gone", bot.Bot{Agent: model.AgentCodex, Project: c.Cwd, Thread: 8}, 0},
	} {
		if got := c.BotThread(tc.bot).ID; got != tc.want {
			t.Errorf("%s: thread %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBotRequestStartsWithTheBotsSettings(t *testing.T) {
	for _, tc := range []struct{ access, policy string }{
		{"", "on-request"}, // follows the permission picked in the window
		{"read", string(agentpolicy.Read)},
		{"full", "never"},
	} {
		c, _ := botTestController(t)
		other := t.TempDir()
		c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
		b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentClaude, Project: other, Model: "m1", Effort: "high", Access: tc.access})

		req, start, err := c.botRequest(model.SendRequest{Bot: b.ID, Text: "hi", Agent: model.AgentCodex, Model: "window", Effort: "low", ExplicitSettings: true, Policy: "on-request"})
		if err != nil {
			t.Fatalf("access %q: %v", tc.access, err)
		}
		if start != b.ID || req.ThreadID != 0 {
			t.Errorf("access %q: start %q, thread %d; want a new conversation for %s", tc.access, start, req.ThreadID, b.ID)
		}
		if req.Agent != model.AgentClaude || req.Project != other {
			t.Errorf("access %q: agent %q in %q, want the Bot's", tc.access, req.Agent, req.Project)
		}
		if req.Model != "m1" || req.Effort != "high" || req.ExplicitSettings {
			t.Errorf("access %q: model %q, effort %q, explicit %v; want the Bot's settings", tc.access, req.Model, req.Effort, req.ExplicitSettings)
		}
		if req.Policy != tc.policy {
			t.Errorf("access %q: policy %q, want %q", tc.access, req.Policy, tc.policy)
		}
		if req.Text != "hi" {
			t.Errorf("access %q: text %q changed before the conversation starts", tc.access, req.Text)
		}
	}
}

func TestBotRequestUsesTheStandingConversation(t *testing.T) {
	c, _ := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd, Access: "full"})

	if _, _, err := c.botRequest(model.SendRequest{Bot: "missing", Text: "hi"}); err == nil || err.Error() != tr("This Bot no longer exists") {
		t.Fatalf("missing Bot: %v", err)
	}

	// A recorded session that is not loaded must not get a second conversation.
	c.bots.store.SetSession(b.ID, "srv1")
	_, start, err := c.botRequest(model.SendRequest{Bot: b.ID, Text: "hi"})
	if err == nil || start != "" {
		t.Fatalf("unloaded conversation: start %q, err %v", start, err)
	}

	c.SetThreads([]model.Thread{{ID: 4, ServerID: "srv1", Agent: model.AgentCodex}})
	req, start, err := c.botRequest(model.SendRequest{Bot: b.ID, Text: "hi", Model: "window", ExplicitSettings: true, Policy: "on-request"})
	if err != nil || start != "" {
		t.Fatalf("loaded conversation: start %q, err %v", start, err)
	}
	if req.ThreadID != 4 || req.Policy != "" || req.ExplicitSettings {
		t.Fatalf("request = thread %d, policy %q, explicit %v; want thread 4 keeping its own permission", req.ThreadID, req.Policy, req.ExplicitSettings)
	}

	// Before the agent names the session, the Bot is found by its thread.
	c.bots.store.Reset(b.ID)
	c.bots.pending[6] = b.ID
	c.SetThreads([]model.Thread{{ID: 6, Agent: model.AgentCodex}})
	req, start, err = c.botRequest(model.SendRequest{Bot: b.ID, Text: "again"})
	if err != nil || start != "" || req.ThreadID != 6 {
		t.Fatalf("pending conversation: thread %d, start %q, err %v", req.ThreadID, start, err)
	}
}

func TestSubmitToMissingBotReportsIt(t *testing.T) {
	c, _ := botTestController(t)
	if id := c.Submit(model.SendRequest{Bot: "missing", Text: "hi"}); id != 0 {
		t.Fatalf("Submit = %d, want 0", id)
	}
	if want := tr("This Bot no longer exists"); c.LastSubmitError != want || c.Status() != want {
		t.Fatalf("error %q, status %q, want %q", c.LastSubmitError, c.Status(), want)
	}
}

func TestCurrentBot(t *testing.T) {
	c, _ := botTestController(t)
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	here := saveTestBot(t, c, bot.Bot{Name: "Here", Agent: model.AgentCodex, Project: c.Cwd})
	there := saveTestBot(t, c, bot.Bot{Name: "There", Agent: model.AgentCodex, Project: other})

	if b, ok := c.CurrentBot(); ok {
		t.Fatalf("an empty conversation belongs to %q", b.Name)
	}
	c.SetDraftBot(here.ID)
	if b, ok := c.CurrentBot(); !ok || b.ID != here.ID {
		t.Fatalf("draft Bot = %+v, %v", b, ok)
	}
	// The first message starts the conversation in the Bot's own project.
	c.SetDraftBot(there.ID)
	if b, ok := c.CurrentBot(); ok {
		t.Fatalf("the empty conversation of this project belongs to %q of another", b.Name)
	}

	c.SetDraftBot("")
	c.bots.store.SetSession(here.ID, "srv1")
	c.SetThreads([]model.Thread{
		{ID: 1, ServerID: "srv1", Agent: model.AgentCodex},
		{ID: 2, ServerID: "srv2", Agent: model.AgentCodex},
	})
	c.SetActive(1)
	if b, ok := c.CurrentBot(); !ok || b.ID != here.ID {
		t.Fatalf("standing conversation open: %+v, %v", b, ok)
	}
	c.SetActive(2)
	if b, ok := c.CurrentBot(); ok {
		t.Fatalf("an ordinary conversation belongs to %q", b.Name)
	}
}

func TestBotThreadUpdatedRecordsSessionAndPreview(t *testing.T) {
	// Without Bots there is nothing to keep, whatever the controller has.
	(&Controller{}).botThreadUpdated(model.Thread{ID: 1}, model.Thread{ID: 1, ServerID: "srv"}, "/p")

	c, path := botTestController(t)
	other := t.TempDir()
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})

	// The agent names the session of the conversation the Bot started.
	c.bots.pending[5] = b.ID
	c.botThreadUpdated(model.Thread{ID: 5, Agent: model.AgentCodex}, model.Thread{ID: 5, Agent: model.AgentCodex}, c.Cwd)
	if c.bots.pending[5] != b.ID {
		t.Fatal("an update without a session forgot the pending conversation")
	}
	c.botThreadUpdated(model.Thread{ID: 5, Agent: model.AgentCodex}, model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "srv5"}, c.Cwd)
	if got := storedBot(t, c, b.ID).Session; got != "srv5" {
		t.Fatalf("session = %q, want srv5", got)
	}
	if len(c.bots.pending) != 0 {
		t.Fatalf("pending = %v after the session was named", c.bots.pending)
	}
	if kept := bot.Load(path).Bots; len(kept) != 1 || kept[0].Session != "srv5" {
		t.Fatalf("file holds %+v", kept)
	}

	// The agent moves the conversation to another session.
	c.botThreadUpdated(model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "srv5"}, model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "srv6"}, c.Cwd)
	if got := storedBot(t, c, b.ID).Session; got != "srv6" {
		t.Fatalf("session = %q, want srv6", got)
	}

	// The same session ID in another project is another conversation.
	c.botThreadUpdated(model.Thread{ID: 8, Agent: model.AgentCodex, ServerID: "srv6"}, model.Thread{ID: 8, Agent: model.AgentCodex, ServerID: "srv7"}, other)
	if got := storedBot(t, c, b.ID).Session; got != "srv6" {
		t.Fatalf("another project's conversation moved the session to %q", got)
	}

	reply := []model.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "  Done.\nAll   good  "}}
	busy := model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "srv6", Busy: true, Messages: reply}
	c.botThreadUpdated(busy, busy, c.Cwd)
	if got := storedBot(t, c, b.ID).Preview; got != "" {
		t.Fatalf("preview %q taken from a turn still running", got)
	}
	done := busy
	done.Busy = false
	c.botThreadUpdated(busy, done, c.Cwd)
	if got := storedBot(t, c, b.ID).Preview; got != "Done. All good" {
		t.Fatalf("preview = %q", got)
	}
	if kept := bot.Load(path).Bots; len(kept) != 1 || kept[0].Preview != "Done. All good" {
		t.Fatalf("file holds %+v", kept)
	}
}

// A delegated task's conversation becomes another of the Bot's once its agent
// names the session, and every conversation of the Bot is found as the
// Bot's, so that its messages carry the charter.
func TestBotTaskConversationsAndBotOf(t *testing.T) {
	c, path := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd, Note: "Reviews"})
	c.bots.tasks[5] = b.ID
	if got, ok := c.botOf(model.Thread{ID: 5}, ""); !ok || got.ID != b.ID {
		t.Fatal("a task conversation still starting is not the Bot's")
	}
	c.botThreadUpdated(model.Thread{ID: 5, Agent: model.AgentCodex}, model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "task1"}, c.Cwd)
	if got := storedBot(t, c, b.ID); got.Session != "" || len(got.Others) != 1 || got.Others[0] != "task1" || len(c.bots.tasks) != 0 {
		t.Fatalf("Bot = %+v, waiting %v; want task1 among its other conversations", got, c.bots.tasks)
	}
	if kept := bot.Load(path).Bots; len(kept) != 1 || len(kept[0].Others) != 1 {
		t.Fatalf("file holds %+v", kept)
	}

	// A reply in a task conversation is not the Bot's preview.
	busy := model.Thread{ID: 5, Agent: model.AgentCodex, ServerID: "task1", Busy: true, Messages: []model.Message{{Role: "assistant", Content: "Reviewed."}}}
	done := busy
	done.Busy = false
	c.botThreadUpdated(busy, done, c.Cwd)
	if got := storedBot(t, c, b.ID).Preview; got != "" {
		t.Fatalf("preview %q taken from a task conversation", got)
	}

	c.SetThreads([]model.Thread{{ID: 5, ServerID: "task1", Agent: model.AgentCodex}, {ID: 6, ServerID: "plain", Agent: model.AgentCodex}})
	if got, ok := c.botOf(c.ThreadWithID(5), ""); !ok || got.ID != b.ID {
		t.Fatal("a recorded task conversation is not the Bot's")
	}
	if got, ok := c.botOf(c.ThreadWithID(6), ""); ok {
		t.Fatalf("an ordinary conversation belongs to %q", got.Name)
	}
	if got, ok := c.botOf(model.Thread{}, b.ID); !ok || got.ID != b.ID {
		t.Fatal("a message naming the Bot is not the Bot's")
	}
	if _, ok := c.botOf(model.Thread{}, "missing"); ok {
		t.Fatal("a deleted Bot was found")
	}

	// A Bot deleted while a task conversation starts leaves it ordinary.
	c.bots.tasks[7] = "missing"
	c.botThreadUpdated(model.Thread{ID: 7, Agent: model.AgentCodex}, model.Thread{ID: 7, Agent: model.AgentCodex}, c.Cwd)
	if _, waiting := c.bots.tasks[7]; waiting {
		t.Fatal("a deleted Bot's conversation is still waiting")
	}
}

// MCP tools find the Bot of the calling conversation, for its memory.
func TestThreadBot(t *testing.T) {
	c, _ := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	c.bots.store.SetSession(b.ID, "talk")
	c.bots.store.AddOther(b.ID, "task")
	c.SetThreads([]model.Thread{
		{ID: 1, ServerID: "talk", Agent: model.AgentCodex},
		{ID: 2, ServerID: "task", Agent: model.AgentCodex},
		{ID: 3, ServerID: "plain", Agent: model.AgentCodex},
		{ID: 4, ServerID: "talk", Agent: model.AgentClaude},
	})
	for id, want := range map[int]string{1: b.ID, 2: b.ID, 3: "", 4: ""} {
		got, home := c.ThreadBot(c.ThreadWithID(id))
		if got != want || (want != "") != (home == c.Cwd) {
			t.Errorf("conversation %d belongs to Bot %q of %q, want %q", id, got, home, want)
		}
	}
}

// An automation given to a Bot runs in the Bot's project, with its agent.
func TestCheckAutomationBot(t *testing.T) {
	c, _ := botTestController(t)
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentClaude, Project: c.Cwd})
	if err := c.checkAutomationBot(&automation.Automation{Agent: model.AgentCodex, Project: c.Cwd}); err != nil {
		t.Fatalf("an automation without a Bot: %v", err)
	}
	if err := c.checkAutomationBot(&automation.Automation{Bot: "missing", Project: c.Cwd}); err == nil {
		t.Fatal("a missing Bot was accepted")
	}
	if err := c.checkAutomationBot(&automation.Automation{Bot: b.ID, Project: other}); err == nil {
		t.Fatal("a Bot of another project was accepted")
	}
	a := automation.Automation{Bot: b.ID, Agent: model.AgentCodex, Project: c.Cwd}
	if err := c.checkAutomationBot(&a); err != nil || a.Agent != model.AgentClaude {
		t.Fatalf("agent %q, %v; want the Bot's", a.Agent, err)
	}
	if agent, access, ok := c.automationBot(b.ID); !ok || agent != model.AgentClaude || access != "" {
		t.Fatalf("automationBot = %q, %q, %v", agent, access, ok)
	}
}

// A task the Bot finished is posted in its chat; its conversation with the
// person and automation runs are not, the runner posting those that notify.
func TestBotReports(t *testing.T) {
	c, path := botTestController(t)
	b := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	c.bots.store.SetSession(b.ID, "talk")
	c.bots.store.AddOther(b.ID, "task")
	c.bots.store.AddOther(b.ID, "run")
	reply := []model.Message{{Role: "assistant", Content: "Reviewed: two problems."}}
	finish := func(th model.Thread) {
		busy := th
		busy.Busy = true
		c.botThreadUpdated(busy, th, c.Cwd)
	}
	finish(model.Thread{ID: 1, ServerID: "talk", Agent: model.AgentCodex, Messages: reply})
	finish(model.Thread{ID: 3, ServerID: "run", Agent: model.AgentCodex, ExecutionID: "x", Automation: "a1", Messages: reply})
	if got := storedBot(t, c, b.ID).Reports; len(got) != 0 {
		t.Fatalf("reports %+v from the person's conversation or a run", got)
	}
	finish(model.Thread{ID: 2, ServerID: "task", Agent: model.AgentCodex, ExecutionID: "task-1", Title: "Review of Kai's changes", Messages: reply})
	got := storedBot(t, c, b.ID).Reports
	if len(got) != 1 || got[0].Title != "Review of Kai's changes" || got[0].Summary != "Reviewed: two problems." || got[0].Kind != "task" || got[0].Read {
		t.Fatalf("reports = %+v", got)
	}
	if kept := bot.Load(path).Bots; len(kept[0].Reports) != 1 {
		t.Fatalf("file holds %+v", kept[0].Reports)
	}
	c.SetThreads([]model.Thread{{ID: 2, ServerID: "task", Agent: model.AgentCodex}})
	if th := c.BotReportThread(got[0]); th.ID != 2 {
		t.Fatalf("the report opens conversation %d, want 2", th.ID)
	}

	c.botAutomationReport(automation.Automation{ID: "a1", Bot: b.ID, Project: c.Cwd}, automation.Run{ServerID: "run"}, "Patrol · Passed", "All clear", false)
	if got := storedBot(t, c, b.ID).Reports; len(got) != 2 || got[0].Kind != "automation" || got[0].Title != "Patrol · Passed" {
		t.Fatalf("after the run: %+v", got)
	}
	if _, err := c.BotOp("botRead", bot.Request{ID: b.ID}); err != nil || storedBot(t, c, b.ID).Unread() != 0 {
		t.Fatalf("botRead: %v, %d unread", err, storedBot(t, c, b.ID).Unread())
	}
}
