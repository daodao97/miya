//go:build !mygo_ui

package integration

import (
	"encoding/json"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	"testing"
)

func TestDesktopUnreadCompletionPersistenceAndReadVersions(t *testing.T) {
	c := isolatedController(t)
	c.LoadDesktopUnread()
	c.InstallNotifications()
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "session", Busy: true}, {ID: 2, Busy: true, Loading: true}})
	c.Update(1, func(t *model.Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	c.Update(2, func(t *model.Thread) { t.Busy, t.Loading, t.CompletedAtMs = false, false, 100 })
	finished := c.ThreadWithID(1)
	if !c.DesktopThreadUnread(finished) || c.DesktopThreadUnread(c.ThreadWithID(2)) {
		t.Fatal("completion should be unread; loaded history should not")
	}
	c.ProjectCache = map[string][]model.Thread{"/other": {{ID: 3, ServerID: "background", Busy: true}}}
	c.Update(3, func(t *model.Thread) { t.Busy, t.CompletedAtMs = false, 150 })
	if !c.DesktopThreadUnread(c.ThreadWithID(3)) {
		t.Fatal("background completion was not recorded")
	}
	restored := &controller.Controller{}
	restored.LoadDesktopUnread()
	remapped := finished
	remapped.ID = 77
	if !restored.DesktopThreadUnread(remapped) {
		t.Fatal("restart or reassigned sidebar ID lost unread state")
	}
	otherAgent := remapped
	otherAgent.Agent = appmodel.AgentClaude
	if restored.DesktopThreadUnread(otherAgent) {
		t.Fatal("unread state leaked across backends")
	}
	c.Update(1, func(t *model.Thread) { t.Busy = true })
	c.Update(1, func(t *model.Thread) { t.Busy, t.CompletedAtMs = false, 200 })
	c.MarkDesktopRead(appmodel.DesktopRead{Key: appmodel.DesktopUnreadKey(finished), Version: 100})
	if !c.DesktopThreadUnread(finished) {
		t.Fatal("stale read acknowledgement cleared a newer completion")
	}
	c.ReadDesktopThread(finished)
	if c.DesktopThreadUnread(finished) {
		t.Fatal("opening the completed conversation did not clear unread state")
	}
	restored.LoadDesktopUnread()
	if restored.DesktopThreadUnread(remapped) {
		t.Fatal("read state did not persist")
	}
}

func TestDesktopUnreadServiceSnapshotAndAcknowledgement(t *testing.T) {
	c := isolatedController(t)
	c.LoadDesktopUnread()
	thread := model.Thread{ID: 2, ServerID: "background", Agent: appmodel.AgentClaude}
	c.SetProjects(append(c.Projects(), projectmodel.Project{Name: "other", Path: "/other"}))
	c.ProjectCache = map[string][]model.Thread{"/other": {thread}}
	c.RecordDesktopCompletion(model.Thread{Busy: true}, model.Thread{ID: 2, ServerID: thread.ServerID, Agent: thread.Agent, CompletedAtMs: 100})
	data, err := json.Marshal(c.SnapshotState())
	if err != nil {
		t.Fatal(err)
	}
	var state svcproto.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	mirror := controller.NewMirrorController()
	t.Cleanup(mirror.Cancel)
	mirror.ApplyState(state)
	if !mirror.DesktopThreadUnread(thread) {
		t.Fatal("service did not send background unread state to the window")
	}
	key := appmodel.DesktopUnreadKey(thread)
	mirror.DesktopReadPending = map[string]int64{key: 100}
	mirror.ApplyState(state)
	if mirror.DesktopThreadUnread(thread) {
		t.Fatal("in-flight acknowledgement reappeared after an older snapshot")
	}
	args, _ := json.Marshal(appmodel.DesktopRead{Key: key, Version: 100})
	s := serviceserver.New(c, nil)
	if _, err := s.Handle(nil, svcproto.Request{Op: "desktop-read", Args: args}); err != nil {
		t.Fatal(err)
	}
	mirror.ApplyState(c.SnapshotState())
	if mirror.DesktopThreadUnread(thread) || len(mirror.DesktopReadPending) != 0 {
		t.Fatal("service did not acknowledge read state")
	}
	state.DesktopUnread[key] = 200
	mirror.ApplyState(state)
	if !mirror.DesktopThreadUnread(thread) {
		t.Fatal("a newer completion should become unread again")
	}
}
