package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proc(pid, parent int, start string) Process {
	return Process{Identity: Identity{Host: "desktop", Boot: "boot", PID: pid, Start: start}, ParentPID: parent, Name: "server"}
}
func key(agent, id string) SessionKey {
	return SessionKey{Source: "desktop", Backend: agent, SessionID: id}
}
func live(v View) map[int]Process {
	out := map[int]Process{}
	for _, p := range v.Processes {
		if p.Status == "running" {
			out[p.Identity.PID] = p
		}
	}
	return out
}
func TestOwnershipIsSessionScopedAndSurvivesReparenting(t *testing.T) {
	s := New()
	a, b := key("codex", "a"), key("claude", "b")
	s.Begin(a, "a1", Capabilities{})
	s.Begin(b, "b1", Capabilities{})
	all := []Process{proc(10, 1, "10"), proc(11, 10, "11"), proc(20, 1, "20"), proc(21, 20, "21")}
	endpoints := []Endpoint{{Process: all[1].Identity, Host: "127.0.0.1", Port: 3000}, {Process: all[3].Identity, Host: "127.0.0.1", Port: 8080}}
	s.Observe(a, "a1", all, []Root{{PID: 10, TaskID: "native-a"}}, endpoints)
	s.Observe(b, "b1", all, []Root{{PID: 20, TaskID: "native-b"}}, endpoints)
	if len(live(s.Read(a))) != 2 || live(s.Read(a))[20].Identity.PID != 0 || len(s.Read(a).Endpoints) != 1 || s.Read(a).Endpoints[0].Port != 3000 {
		t.Fatalf("session A leaked: %+v", s.Read(a))
	}
	if len(live(s.Read(b))) != 2 || len(s.Read(b).Endpoints) != 1 || s.Read(b).Endpoints[0].Port != 8080 {
		t.Fatalf("session B leaked: %+v", s.Read(b))
	}
	all[1].ParentPID = 1
	all = all[1:]
	s.Observe(a, "a1", all, nil, endpoints)
	if len(live(s.Read(a))) != 1 || live(s.Read(a))[11].Evidence != "descendant" {
		t.Fatal("verified reparented child lost ownership")
	}
	reused := []Process{proc(10, 1, "reused-root"), proc(12, 10, "new-child"), proc(11, 1, "reused-child")}
	s.Observe(a, "a1", reused, []Root{{PID: 10, TaskID: "native-a"}}, nil)
	if len(live(s.Read(a))) != 0 {
		t.Fatal("reused PID inherited old ownership")
	}
}
func TestEpochsAndTaskRunsRejectStaleUpdates(t *testing.T) {
	s := New()
	k := key("claude", "session")
	s.Begin(k, "old", Capabilities{})
	s.PartialTask(k, "old", Task{ID: "123", RunID: "run2", RunOrder: "run2", Status: "running"})
	s.PartialTask(k, "old", Task{ID: "123", RunID: "run1", RunOrder: "run1", Status: "completed"})
	if s.Read(k).Tasks[0].Status != "running" {
		t.Fatal("late task result stopped new run")
	}
	s.PartialTask(k, "old", Task{ID: "123", RunID: "run2", RunOrder: "run2", Status: "completed"})
	s.PartialTask(k, "old", Task{ID: "123", RunID: "run2", RunOrder: "run2", Status: "running"})
	if s.Read(k).Tasks[0].Status != "completed" {
		t.Fatal("late start resurrected completed task")
	}
	s.Begin(k, "new", Capabilities{})
	if s.Tasks(k, "old", []Task{{ID: "wrong"}}, true, "") {
		t.Fatal("old snapshot accepted")
	}
	s.Tasks(k, "new", nil, false, "unavailable")
	if s.Read(k).TasksKnown {
		t.Fatal("incomplete snapshot became known empty")
	}
	s.Tasks(k, "new", []Task{}, true, "")
	if !s.Read(k).TasksKnown || len(s.Read(k).Tasks) != 0 {
		t.Fatal("complete empty snapshot failed")
	}
}
func TestPersistenceRestoresUnknownAndVerifiesFullIdentity(t *testing.T) {
	s := New()
	k := key("codex", "session")
	s.Begin(k, "epoch", Capabilities{})
	s.Tasks(k, "epoch", []Task{{ID: "task", Description: "TOKEN=private node --password hunter2", Status: "running"}}, true, "")
	p := proc(99, 1, "start")
	s.Observe(k, "epoch", []Process{p}, []Root{{PID: 99, TaskID: "task"}}, []Endpoint{{Process: p.Identity, Host: "::1", Port: 5173}})
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "hunter2") {
		t.Fatal("credential persisted")
	}
	restored := Load(path)
	v := restored.Read(k)
	if v.TasksKnown || len(v.Endpoints) != 0 || v.Processes[0].Status != "unknown" {
		t.Fatal("cache was presented as live")
	}
	restored.Begin(k, "new-connection", Capabilities{})
	restored.Observe(k, "new-connection", []Process{p}, nil, nil)
	if len(live(restored.Read(k))) != 1 {
		t.Fatal("same process identity did not recover")
	}
	p.Identity.Boot = "new-boot"
	restored.Observe(k, "new-connection", []Process{p}, nil, nil)
	if len(live(restored.Read(k))) != 0 {
		t.Fatal("process from another boot inherited ownership")
	}
}
func TestNumericTaskIDDoesNotCreateProcess(t *testing.T) {
	s := New()
	k := key("agent", "session")
	s.Begin(k, "epoch", Capabilities{})
	s.Tasks(k, "epoch", []Task{{ID: "1234", Status: "running"}}, true, "")
	s.Observe(k, "epoch", []Process{proc(1234, 1, "start")}, nil, nil)
	if len(live(s.Read(k))) != 0 {
		t.Fatal("native identifier treated as OS PID")
	}
	s.TasksUnknown(k, "epoch")
	if s.Read(k).Tasks[0].Status != "unknown" {
		t.Fatal("disconnected tasks still claimed running")
	}
}
func TestSystemProcessIdentityIncludesCurrentProcess(t *testing.T) {
	all, err := SystemProcesses()
	if err != nil {
		t.Skip(err)
	}
	for _, p := range all {
		if p.Identity.PID == os.Getpid() {
			if p.Identity.Boot == "" || p.Identity.Start == "" {
				t.Fatal("missing process start identity")
			}
			return
		}
	}
	t.Fatal("current process absent from complete observation")
}

func TestRemovedTaskCannotBeResurrectedByPartialEvent(t *testing.T) {
	s := New()
	k := key("agent", "session")
	s.Begin(k, "epoch", Capabilities{})
	s.PartialTask(k, "epoch", Task{ID: "task", RunID: "one", Status: "running"})
	s.Tasks(k, "epoch", nil, true, "")
	s.PartialTask(k, "epoch", Task{ID: "task", RunID: "one", Status: "running"})
	if len(s.Read(k).Tasks) != 0 {
		t.Fatal("late partial event resurrected removed task")
	}
	s.PartialTask(k, "epoch", Task{ID: "task", RunID: "two", Status: "running"})
	if len(s.Read(k).Tasks) != 1 {
		t.Fatal("new run did not start")
	}
}
