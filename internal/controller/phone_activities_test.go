package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/apns"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type activityTestSender struct{ notices []apns.Notification }

func (s *activityTestSender) Send(_ context.Context, n apns.Notification) (apns.Response, error) {
	s.notices = append(s.notices, n)
	return apns.Response{}, nil
}

func TestPhoneActivityProtocolRotationNamespacePublishAndEnd(t *testing.T) {
	sender := &activityTestSender{}
	manager, err := apns.NewActivityManager(sender, apns.ActivityManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	threads := []model.Thread{{ID: 7, ServerID: "session", Agent: "codex", Title: "Task", Busy: true}}
	c := &Controller{Activities: manager, WindowState: appmodel.WindowState{Threads: func() []model.Thread { return threads }}}
	c.ReferenceSource = strings.Repeat("a", 32)
	c.Run = func(fn func()) { fn() }
	ref := model.ThreadRef{Source: c.ReferenceSource, Backend: "codex", SessionID: "session"}
	reg := phoneproto.ActivityRegistration{DeviceID: strings.Repeat("b", 32), Desktop: strings.Repeat("b", 24), ID: "activity", Token: strings.Repeat("c", 64), Reference: ref.URL()}
	var reply remote.Frame
	ps := &phoneSession{c: c, ctx: context.Background(), send: func(f remote.Frame) bool { reply = f; return true }}
	send := func(r phoneproto.ActivityRegistration) {
		t.Helper()
		raw, _ := json.Marshal(r)
		if !phoneRequests["activity"](ps, remote.Request{Op: "activity", Activity: raw}) || reply.Type != "activity" || reply.Error != "" {
			t.Fatal("activity protocol failed", reply)
		}
	}
	send(reg)
	old := manager.Targets()[0]
	reg.Token = strings.Repeat("d", 64)
	send(reg)
	if manager.Publish(context.Background(), old, apns.ActivityUpdate{ContentState: json.RawMessage(`{}`)}) != apns.ErrActivityChanged {
		t.Fatal("rotation kept stale target")
	}
	deliveries := c.phoneActivityUpdates(manager)
	if len(deliveries) != 1 || deliveries[0].update.End {
		t.Fatal("missing running update")
	}
	if err := manager.Publish(context.Background(), deliveries[0].target, deliveries[0].update); err != nil {
		t.Fatal(err)
	}
	n := sender.notices[0]
	if n.DeviceToken != reg.Token || n.Payload.LiveActivity == nil || n.Payload.Title != "" {
		t.Fatal("activity token mixed with alert push", n)
	}
	var state map[string]any
	if json.Unmarshal(n.Payload.LiveActivity.ContentState, &state) != nil || state["title"] != "Task" || state["url"] != phoneproto.ActivityURL(ref.URL(), reg.Desktop) {
		t.Fatal("wrong NSGo content schema", string(n.Payload.LiveActivity.ContentState))
	}
	// A second installation may use the same native ID without replacing ours.
	other := reg
	other.DeviceID = strings.Repeat("e", 32)
	send(other)
	if len(manager.Targets()) != 2 {
		t.Fatal("IDs not scoped per device")
	}
	other.Token = ""
	send(other)
	if len(manager.Targets()) != 1 {
		t.Fatal("unregister removed wrong device")
	}
	threads[0].Busy = false
	deliveries = c.phoneActivityUpdates(manager)
	if len(deliveries) != 1 || !deliveries[0].update.End {
		t.Fatal("completed turn did not end activity")
	}
	if err := manager.Publish(context.Background(), deliveries[0].target, deliveries[0].update); err != nil {
		t.Fatal(err)
	}
	if len(manager.Targets()) != 0 || sender.notices[1].Payload.LiveActivity.Event != "end" {
		t.Fatal("ended activity retained subscription")
	}
	for _, bad := range []phoneproto.ActivityRegistration{
		{ID: "activity", Token: reg.Token, Reference: reg.Reference},
		{DeviceID: reg.DeviceID, ID: "activity", Token: "invalid", Reference: reg.Reference},
		{DeviceID: reg.DeviceID, ID: "activity", Token: reg.Token, Reference: "miya://" + strings.Repeat("f", 32) + "/thread/codex/session"},
		{DeviceID: reg.DeviceID, ID: "activity", Token: reg.Token, Reference: "miya://" + c.ReferenceSource + "/thread/codex/missing"},
	} {
		if c.registerPhoneActivity(bad) == nil {
			t.Fatal("invalid registration accepted", bad)
		}
	}
}
