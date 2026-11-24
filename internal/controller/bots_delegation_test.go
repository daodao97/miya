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

// list_agents shows a conversation the Bots of its own project: the name,
// the role and the agent. Every task starts a new conversation of the Bot,
// so no conversation state is listed.
func TestBotListShowsTheProjectsBots(t *testing.T) {
	c, _ := botTestController(t)
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	save := func(name, project, note string) bot.Bot {
		return saveTestBot(t, c, bot.Bot{Name: name, Agent: model.AgentCodex, Project: project, Note: note})
	}
	save("Review", c.Cwd, "Reviews pull requests")
	busy := save("Busy", c.Cwd, "")
	save("Elsewhere", other, "")
	c.bots.store.SetSession(busy.ID, "srv-busy")
	c.SetThreads([]model.Thread{{ID: 2, ServerID: "srv-busy", Agent: model.AgentCodex, Busy: true}})

	listed := map[string]map[string]any{}
	for _, entry := range c.botList(c.Cwd) {
		listed[entry["name"].(string)] = entry
	}
	if len(listed) != 2 {
		t.Fatalf("listed %v, want the 2 Bots of this project", listed)
	}
	if listed["Review"]["role"] != "Reviews pull requests" || listed["Review"]["agent"] != model.AgentCodex {
		t.Errorf("Review = %v", listed["Review"])
	}
	if _, has := listed["Busy"]["role"]; has {
		t.Error("a Bot without a charter got a role")
	}
	if _, has := listed["Busy"]["conversation"]; has {
		t.Error("a conversation state is listed")
	}
	if got := c.botList(other); len(got) != 1 || got[0]["name"] != "Elsewhere" {
		t.Errorf("the other project lists %v", got)
	}
	if got := c.botList(""); got != nil {
		t.Errorf("no project lists %v", got)
	}
	if got := c.botList(t.TempDir()); got != nil {
		t.Errorf("a project without Bots lists %v", got)
	}
}

// start_task with bot always starts a new conversation of the Bot: whether
// the person's conversation with it is busy, starting or not loaded does not
// matter.
func TestDelegationBotStartsANewConversation(t *testing.T) {
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
		{Bot: "Rin", Access: "edit"},
		{Bot: "Ren"},
	} {
		if _, _, err := c.delegationBot(here, a); !botErrorIs(err, "INVALID_ARGUMENT") {
			t.Errorf("%+v: %v, want INVALID_ARGUMENT", a, err)
		}
	}
	if _, _, err := c.delegationBot(there, coordinator.Args{Bot: "Rin"}); !botErrorIs(err, "ACCESS_DENIED") {
		t.Errorf("a conversation of another project: %v, want ACCESS_DENIED", err)
	}

	for name, setup := range map[string]func(){
		"no conversation": func() {},
		"starting": func() {
			c.bots.pending[4] = rin.ID
			c.SetThreads([]model.Thread{here, {ID: 4, Agent: model.AgentClaude}})
		},
		"not loaded": func() { c.bots.store.SetSession(rin.ID, "srv1"); c.SetThreads([]model.Thread{here}) },
		"busy": func() {
			c.SetThreads([]model.Thread{here, {ID: 5, ServerID: "srv1", Agent: model.AgentClaude, Busy: true}})
		},
	} {
		setup()
		b, ref, err := c.delegationBot(here, coordinator.Args{Bot: " rin "})
		if err != nil || b.ID != rin.ID || ref != "" {
			t.Errorf("%s: Bot %q, target %q, %v; want a new conversation of Rin", name, b.ID, ref, err)
		}
	}
}

// A task starts a Bot's conversation only with access its own conversation
// has, even where the person's conversation with the Bot has more.
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
	if _, _, err := c.delegationBot(caller, coordinator.Args{Bot: "Wide"}); !botErrorIs(err, "ACCESS_DENIED") {
		t.Fatalf("with the person's full-access conversation: %v, want ACCESS_DENIED", err)
	}
}

// A Bot that also works in another project is listed there and takes its
// tasks.
func TestBotOfSeveralProjects(t *testing.T) {
	c, _ := botTestController(t)
	c.ReferenceSource = botTestSource
	other := t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "other", Path: other}})
	saveTestBot(t, c, bot.Bot{Name: "Ops", Agent: model.AgentCodex, Project: c.Cwd, Projects: []string{other}})
	if got := c.botList(other); len(got) != 1 || got[0]["name"] != "Ops" {
		t.Fatalf("the other project lists %v", got)
	}
	there := model.Thread{ID: 11, ServerID: "elsewhere", Agent: model.AgentCodex, PermissionPolicy: "on-request"}
	c.ProjectCache[other] = []model.Thread{there}
	if _, _, err := c.delegationBot(there, coordinator.Args{Bot: "Ops"}); err != nil {
		t.Fatalf("a task from the other project: %v", err)
	}
	if _, err := c.BotOp("botSave", bot.Request{Bot: bot.Bot{Name: "Lost", Agent: model.AgentCodex, Project: c.Cwd, Projects: []string{t.TempDir()}}}); err == nil {
		t.Fatal("a Bot mounting an unknown project was saved")
	}
}
