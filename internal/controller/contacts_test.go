package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/contacts"
	"miya/internal/uiruntime"
)

func contactRecoveryController(project string) *Controller {
	c := &Controller{WindowState: appmodel.WindowState{Cwd: project, ProjectCache: map[string][]model.Thread{}}, Serial: 100}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	return c
}

func TestContactControlsUseTheConversationsModelCapabilities(t *testing.T) {
	c := NewMirrorController()
	defer c.Cancel()
	var models []codex.Model
	if err := json.Unmarshal([]byte(`[{"model":"global","supportedReasoningEfforts":[{"reasoningEffort":"low"}]},{"model":"contact","supportedReasoningEfforts":[{"reasoningEffort":"high"},{"reasoningEffort":"xhigh"}]}]`), &models); err != nil {
		t.Fatal(err)
	}
	c.SetReady(true)
	c.SetModels(models)
	c.SetModel("global")
	c.SetThreads([]model.Thread{{ID: 1, Contact: &contacts.Session{ID: "role"}, LaunchModel: "contact"}})
	c.Mirror.SetActive(1)
	controls := c.CurrentAgentControls()
	choices := controls.EffortChoices()
	if len(choices) != 2 || choices[0] != "high" || choices[1] != "xhigh" || c.Model() != "global" {
		t.Fatal("contact model inherited global reasoning capabilities", choices)
	}
}

func TestContactRecoveryWithoutSidebarCacheOrOriginalNumericIDs(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	s := contacts.New(filepath.Join(appdir.Dir(), "contacts"))
	out, _, err := s.Apply(contacts.Request{ID: "recover-contact", Op: "save", Profile: contacts.Profile{Name: "Friend", Persona: "Frozen role", Agent: "claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.Apply(contacts.Request{ID: "recover-session", Op: "open", BotID: out.Profiles[0].ID, Context: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(session.ID, "native-history", true, "Past conversation", "Last answer", 1000, false); err != nil {
		t.Fatal(err)
	}
	c := contactRecoveryController(t.TempDir())
	c.restoreContactThreads()
	threads := c.AllThreads()
	if len(threads) != 1 {
		t.Fatal("lost conversation without cache", threads)
	}
	thread := threads[0]
	if thread.ID <= 100 || thread.ServerID != "native-history" || !thread.Resumed || thread.HistoryLoaded || thread.Contact.Persona != "Frozen role" || c.ThreadWorkspace(thread.ID) != session.Workspace {
		t.Fatalf("bad restored session: %+v", thread)
	}
	c.restoreContactThreads()
	if len(c.AllThreads()) != 1 {
		t.Fatal("recovery duplicated conversation")
	}
	if err := c.contactStore().SetChoices(session.ID, "chosen-model", "high"); err != nil {
		t.Fatal(err)
	}
	fresh := contactRecoveryController(c.Cwd)
	fresh.restoreContactThreads()
	if got := fresh.AllThreads()[0]; got.LaunchModel != "chosen-model" || got.LaunchEffort != "high" {
		t.Fatal("explicit session settings were lost")
	}
}

func TestContactRecoveryOfUnsentAndUnacknowledgedClaudeDrafts(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	c := contactRecoveryController(t.TempDir())
	saved, err := c.ContactOp(contacts.Request{ID: "unsent-contact", Op: "save", Profile: contacts.Profile{Name: "Draft", Agent: "claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := c.ContactOp(contacts.Request{ID: "unsent-session", Op: "open", BotID: saved.ProfileID, Context: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	c.Update(opened.Thread.ID, func(t *model.Thread) { t.ServerID = "reserved-native-id"; t.Resumed = false })
	fresh := contactRecoveryController(c.Cwd)
	fresh.restoreContactThreads()
	thread := fresh.AllThreads()[0]
	if thread.Resumed || !thread.HistoryLoaded || thread.ServerID != "reserved-native-id" {
		t.Fatalf("an unacknowledged native ID became a missing-history resume: %+v", thread)
	}
}
