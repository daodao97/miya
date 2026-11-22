package controller

import (
	"strings"
	"testing"

	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/delegation/coordinator"
	projectmodel "miya/internal/project/model"
)

const botTestSource = "0123456789abcdef0123456789abcdef"

// botErrorIs reports whether err is a delegation error with code.
func botErrorIs(err error, code string) bool {
	return err != nil && strings.HasPrefix(err.Error(), code+":")
}

// Agents address a Bot by the name list_agents gave, however they space or
// capitalise it; Miya's own records use the ID.
func TestBotNamed(t *testing.T) {
	c, _ := botTestController(t)
	rin := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentCodex, Project: c.Cwd})
	review := saveTestBot(t, c, bot.Bot{Name: "Code Review", Agent: model.AgentClaude, Project: c.Cwd})
	for _, tc := range []struct{ key, want string }{
		{rin.ID, rin.ID},
		{"Rin", rin.ID},
		{"  rIN ", rin.ID},
		{"code \t review", review.ID},
		{"", ""},
		{"   ", ""},
		{"Ren", ""},
	} {
		got, ok := c.botNamed(tc.key)
		if ok != (tc.want != "") || got.ID != tc.want {
			t.Errorf("botNamed(%q) = %q, %v; want %q", tc.key, got.ID, ok, tc.want)
		}
	}
	if got := botKey("  Code \n Review "); got != "Code Review" {
		t.Errorf("botKey = %q", got)
	}
}

// list_agents shows a conversation the Bots of its own project, each with
// the state of its standing conversation.
func TestBotListShowsTheProjectsBotsAndTheirConversations(t *testing.T) {
	c, _ := botTestController(t)
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	save := func(name, project, note string) bot.Bot {
		return saveTestBot(t, c, bot.Bot{Name: name, Agent: model.AgentCodex, Project: project, Note: note})
	}
	fresh := save("Fresh", c.Cwd, "Reviews pull requests")
	idle := save("Idle", c.Cwd, "")
	busy := save("Busy", c.Cwd, "")
	starting := save("Starting", c.Cwd, "")
	unloaded := save("Unloaded", c.Cwd, "")
	save("Elsewhere", other, "")
	c.bots.store.SetSession(idle.ID, "srv-idle")
	c.bots.store.SetSession(busy.ID, "srv-busy")
	c.bots.store.SetSession(unloaded.ID, "srv-gone")
	c.bots.pending[3] = starting.ID
	c.SetThreads([]model.Thread{
		{ID: 1, ServerID: "srv-idle", Agent: model.AgentCodex},
		{ID: 2, ServerID: "srv-busy", Agent: model.AgentCodex, Busy: true},
		{ID: 3, Agent: model.AgentCodex},
	})

	listed := map[string]map[string]any{}
	for _, entry := range c.botList(c.Cwd) {
		listed[entry["name"].(string)] = entry
	}
	for name, want := range map[string]string{"Fresh": "new", "Idle": "idle", "Busy": "busy", "Starting": "busy", "Unloaded": "not_loaded"} {
		entry, ok := listed[name]
		if !ok || entry["conversation"] != want || entry["agent"] != fresh.Agent {
			t.Errorf("%s: %v, want conversation %q", name, entry, want)
		}
	}
	if len(listed) != 5 {
		t.Errorf("listed %d Bots, want the 5 of this project: %v", len(listed), listed)
	}
	if listed["Fresh"]["role"] != "Reviews pull requests" {
		t.Errorf("role = %v", listed["Fresh"]["role"])
	}
	if _, has := listed["Idle"]["role"]; has {
		t.Error("a Bot without a note got a role")
	}

	if got := c.botList(other); len(got) != 1 || got[0]["name"] != "Elsewhere" || got[0]["conversation"] != "new" {
		t.Errorf("the other project lists %v", got)
	}
	if got := c.botList(""); got != nil {
		t.Errorf("no project lists %v", got)
	}
	if got := c.botList(t.TempDir()); got != nil {
		t.Errorf("a project without Bots lists %v", got)
	}
}

// start_task with bot goes to the Bot's standing conversation, or starts it,
// and never starts a second one.
func TestDelegationBotResolvesTheStandingConversation(t *testing.T) {
	c, _ := botTestController(t)
	c.ReferenceSource = botTestSource
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	here := model.Thread{ID: 10, ServerID: "caller", Agent: model.AgentCodex, PermissionPolicy: "on-request"}
	there := model.Thread{ID: 11, ServerID: "elsewhere", Agent: model.AgentCodex, PermissionPolicy: "on-request"}
	c.SetThreads([]model.Thread{here})
	c.ProjectCache[other] = []model.Thread{there}
	rin := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentClaude, Project: c.Cwd})

	for _, a := range []coordinator.Args{
		{Bot: "Rin", Agent: model.AgentCodex},
		{Bot: "Rin", Model: "m1"},
		{Bot: "Rin", Effort: "high"},
		{Bot: "Rin", Access: "read"},
		{Bot: "Ren"},
	} {
		if _, _, err := c.delegationBot(here, a); !botErrorIs(err, "INVALID_ARGUMENT") {
			t.Errorf("%+v: %v, want INVALID_ARGUMENT", a, err)
		}
	}
	if _, _, err := c.delegationBot(there, coordinator.Args{Bot: "Rin"}); !botErrorIs(err, "ACCESS_DENIED") {
		t.Errorf("a conversation of another project: %v, want ACCESS_DENIED", err)
	}

	// No conversation yet: the task starts it.
	b, ref, err := c.delegationBot(here, coordinator.Args{Bot: " rin "})
	if err != nil || b.ID != rin.ID || ref != "" {
		t.Fatalf("new conversation: Bot %q, target %q, %v", b.ID, ref, err)
	}

	// Started, but its agent has not named the session.
	c.bots.pending[4] = rin.ID
	c.SetThreads([]model.Thread{here, {ID: 4, Agent: model.AgentClaude}})
	if !c.botPending(rin.ID) || c.botPending("another") {
		t.Fatalf("pending = %v", c.bots.pending)
	}
	if _, _, err := c.delegationBot(here, coordinator.Args{Bot: "Rin"}); !botErrorIs(err, "THREAD_BUSY") {
		t.Errorf("a conversation still starting: %v, want THREAD_BUSY", err)
	}

	// Recorded, but not among the loaded conversations.
	delete(c.bots.pending, 4)
	c.bots.store.SetSession(rin.ID, "srv1")
	c.SetThreads([]model.Thread{here})
	if _, _, err := c.delegationBot(here, coordinator.Args{Bot: "Rin"}); !botErrorIs(err, "CONTEXT_LOADING") {
		t.Errorf("a conversation that is not loaded: %v, want CONTEXT_LOADING", err)
	}

	standing := model.Thread{ID: 5, ServerID: "srv1", Agent: model.AgentClaude}
	c.SetThreads([]model.Thread{here, standing})
	b, ref, err = c.delegationBot(here, coordinator.Args{Bot: "Rin"})
	if err != nil || b.ID != rin.ID || ref == "" || ref != c.ThreadRef(standing).URL() {
		t.Fatalf("standing conversation: Bot %q, target %q, %v", b.ID, ref, err)
	}
}

// A task starts a Bot's conversation only with access its own conversation
// has; a conversation that exists keeps the permission it was started with.
func TestBotTaskCannotStartAConversationWithWiderAccess(t *testing.T) {
	c, _ := botTestController(t)
	c.ReferenceSource = botTestSource
	for _, tc := range []struct {
		caller, access string
		ok             bool
	}{
		{"on-request", "", true},
		{"on-request", "read", true},
		{"on-request", "edit", false},
		{"on-request", "full", false},
		{"read", "read", true},
		{"read", "edit", false},
		{"edit", "edit", true},
		{"edit", "full", false},
		{"never", "edit", true},
		{"never", "full", true},
	} {
		err := c.checkBotAccess(model.Thread{ID: 1, PermissionPolicy: tc.caller}, bot.Bot{Access: tc.access})
		if tc.ok && err != nil || !tc.ok && !botErrorIs(err, "ACCESS_DENIED") {
			t.Errorf("a %q Bot from a %q conversation: %v, want allowed %v", tc.access, tc.caller, err, tc.ok)
		}
	}

	caller := model.Thread{ID: 10, ServerID: "caller", Agent: model.AgentCodex, PermissionPolicy: "on-request"}
	c.SetThreads([]model.Thread{caller})
	wide := saveTestBot(t, c, bot.Bot{Name: "Wide", Agent: model.AgentCodex, Project: c.Cwd, Access: "full"})
	if _, _, err := c.delegationBot(caller, coordinator.Args{Bot: "Wide"}); !botErrorIs(err, "ACCESS_DENIED") {
		t.Fatalf("starting a full-access Bot's conversation: %v, want ACCESS_DENIED", err)
	}
	c.bots.store.SetSession(wide.ID, "srv1")
	c.SetThreads([]model.Thread{caller, {ID: 5, ServerID: "srv1", Agent: model.AgentCodex, PermissionPolicy: "never"}})
	if _, ref, err := c.delegationBot(caller, coordinator.Args{Bot: "Wide"}); err != nil || ref == "" {
		t.Fatalf("the conversation the person started: target %q, %v", ref, err)
	}
}
