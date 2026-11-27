package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	mcpproto "miya/internal/mcp/protocol"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func addFollowUpCall(t *testing.T, f *serviceFixture, id int, item string, at time.Time, note string) mcpproto.Request {
	t.Helper()
	raw, err := json.Marshal(mcpproto.FollowUpArgs{At: at.Format(time.RFC3339Nano), Note: note})
	if err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		f.C.Update(id, func(th *model.Thread) {
			th.Messages = append(th.Messages, model.Message{
				ID: f.C.NextID(), ItemID: item, Role: "tool", Kind: "claudeTool",
				Status: "inProgress", Content: "mcp__miya__follow_up\n" + string(raw),
			})
		})
	})
	return mcpproto.Request{Tool: "follow_up", Arguments: raw}
}

func TestFollowUpWakesOnlyItsIdleConversation(t *testing.T) {
	f := newServiceFixture(t)
	id := collaborationCaller(t, f, appmodel.AgentClaude, "follow-up-caller")
	other := collaborationCaller(t, f, appmodel.AgentClaude, "other-conversation")
	f.serv.Do(func() {
		f.C.Update(id, func(th *model.Thread) { th.Busy = true })
	})
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	const note = "检查 CI 是否跑完"
	due := time.Now().Add(2 * time.Second)
	req := addFollowUpCall(t, f, id, "timer-call", due, note)
	var scheduled mcpproto.FollowUpResult
	if err := client.Call("mcpFollowUp", req, &scheduled); err != nil || scheduled.State != string(dg.NotifyPending) || !scheduled.At.Equal(due) {
		t.Fatalf("schedule = %+v %v", scheduled, err)
	}
	f.serv.Do(func() {
		if !f.C.ServiceKeepAlive() {
			t.Error("pending timer does not keep the background service alive")
		}
	})
	window := f.window()
	t.Cleanup(func() { window.Cancel(); window.Remote.Close() })
	f.waitWindow("the timer in the desktop task bar", func() bool {
		tasks := window.ThreadWithID(id).Tasks
		return len(tasks) == 1 && tasks[0].Kind == "follow_up" && tasks[0].Title == note
	})
	var phoneTask model.ThreadTask
	f.serv.Do(func() {
		for _, project := range f.C.PhoneSnapshot().Projects {
			for _, th := range project.Threads {
				if th.ID == id && len(th.Tasks) == 1 {
					phoneTask = th.Tasks[0]
				}
			}
		}
	})
	if phoneTask.ID != scheduled.ID || phoneTask.NextRunAtMs != due.UnixMilli() {
		t.Fatalf("wrong timer on the phone: %+v", phoneTask)
	}

	time.Sleep(time.Until(due.Add(1100 * time.Millisecond)))
	if timer, _ := f.C.Collaboration.Manager.Delivery(scheduled.ID); timer.State != dg.NotifyPending || timer.Attempts != 0 {
		t.Fatalf("busy conversation attempted to wake: %+v", timer)
	}
	f.serv.Do(func() {
		f.C.Update(id, func(th *model.Thread) { th.Busy = false })
	})
	f.waitWindow("delivery of the follow-up", func() bool {
		timer, _ := f.C.Collaboration.Manager.Delivery(scheduled.ID)
		return timer.State == dg.NotifyDelivered && len(window.ThreadWithID(id).Tasks) == 0
	})
	var received, others int
	f.serv.Do(func() {
		for _, m := range f.C.ThreadWithID(id).Messages {
			if m.Role == "user" && strings.Contains(m.Content, note) {
				received++
			}
		}
		others = len(f.C.ThreadWithID(other).Messages)
	})
	if received != 1 || others != 0 {
		t.Fatalf("message delivered to caller %d times, other received %d messages", received, others)
	}
}

func TestFollowUpVerifiesCallerAndCancelsOnStop(t *testing.T) {
	f := newServiceFixture(t)
	id := collaborationCaller(t, f, appmodel.AgentClaude, "timer-caller")
	other := collaborationCaller(t, f, appmodel.AgentClaude, "timer-neighbor")
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result mcpproto.FollowUpResult
	due := time.Now().Add(time.Hour)
	raw, _ := json.Marshal(mcpproto.FollowUpArgs{At: due.Format(time.RFC3339Nano), Note: "No native call"})
	if err := client.Call("mcpFollowUp", mcpproto.Request{Arguments: raw}, &result); err == nil || !strings.Contains(err.Error(), mcpproto.CallerUnknown) {
		t.Fatalf("unverified caller accepted: %v", err)
	}
	first := addFollowUpCall(t, f, id, "first", due, "检查发布")
	second := addFollowUpCall(t, f, id, "second", due, "复核构建")
	for _, req := range []mcpproto.Request{first, second} {
		if err := client.Call("mcpFollowUp", req, &result); err != nil {
			t.Fatal(err)
		}
	}
	var message string
	if err := client.Call("task", svcproto.TaskAction{Thread: other, Task: result.ID, Action: "cancel"}, &message); err != nil || !strings.Contains(message, "cannot manage") {
		t.Fatalf("another conversation could cancel: %q %v", message, err)
	}
	if err := client.Call("task", svcproto.TaskAction{Thread: id, Task: result.ID, Action: "cancel"}, &message); err != nil || message != "" {
		t.Fatalf("cancel the follow-up = %q %v", message, err)
	}
	f.serv.Do(func() { f.C.StopThread(id) })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(f.C.Collaboration.Manager.FollowUps()) == 0 {
			return
		}
	}
	t.Fatal("stopping the conversation left a pending timer")
}
