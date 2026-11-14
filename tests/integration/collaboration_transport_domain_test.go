//go:build !mygo_ui && !windows

package integration

import (
	"encoding/json"
	"fmt"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	widgets "miya/internal/nsrequests"
	"reflect"
	"testing"
)

func TestNSGoCollaborationPhoneDispatchAndDesktopCardsShareFixedTaskData(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	var from string
	f.serv.Do(func() {
		from = f.C.ThreadRef(f.C.ThreadWithID(caller)).URL()
	})
	created, err := collaborationPhoneCall(t, f, "start_task", from, map[string]any{"request_id": "phone-dispatch", "agent": appmodel.AgentClaude, "task": "Review compatibility", "title": "Compatibility review", "notify": "none"})
	if err != nil {
		t.Fatal(err)
	}
	task := waitCollaborationTask(t, f, created["task_id"].(string))
	desktop, err := collaborationUserCall(t, f, "read_task", from, map[string]any{"task_id": task.ID})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := collaborationPhoneCall(t, f, "read_task", from, map[string]any{"task_id": task.ID})
	if err != nil || !reflect.DeepEqual(desktop, phone) || desktop["agent"] != appmodel.AgentClaude {
		t.Fatal("desktop and phone disagree about the same execution", desktop, phone, err)
	}
	call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		var args map[string]any
		if err := json.Unmarshal(r.Arguments, &args); err != nil {
			done(nil, err)
			return
		}
		result, err := collaborationUserCall(t, f, r.Operation, r.From, args)
		done(widgets.CollaborationArgsJSON(result), err)
	}
	p := (&widgets.CollaborationUI{}).Pane(from)
	p.Read(task.ID, call)
	if p.Err != "" || p.Details[task.ID].Thread != task.Target || p.Details[task.ID].Result.Text != task.Result.Text {
		t.Fatal("fixed result/target changed between phone and desktop state")
	}
}

func TestNSGoCollaborationPhoneAndDesktopCanResolveUnknownNotification(t *testing.T) {
	for _, width := range []int{320} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			f := newServiceFixture(t)
			caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
			f.serv.Do(func() { f.C.Update(caller, func(th *model.Thread) { th.Busy = true }) })
			q := collaborationStoredFixtureTask(t, f, caller)
			s := f.C.Collaboration
			value, err := s.Call(func() (any, error) {
				if _, err := s.Manager.Watch(q.From, q.ID); err != nil {
					return nil, err
				}
				if err := s.Manager.Observe(dg.Event{TaskID: q.ID, State: dg.Completed, Result: dg.Result{Text: "saved fixed result"}}); err != nil {
					return nil, err
				}
				d, err := s.Manager.ClaimDelivery(q.From)
				if err != nil {
					return nil, err
				}
				if err = s.Manager.BeginDeliveryInput(d.ID); err != nil {
					return nil, err
				}
				return d, s.Manager.Recover()
			})
			if err != nil {
				t.Fatal(err)
			}
			d := value.(dg.Delivery)
			result, err := collaborationPhoneCall(t, f, "read_task", q.From, map[string]any{"task_id": q.ID})
			if err != nil || result["notification"] != "needsAttention" || result["delivery_id"] != d.ID {
				t.Fatal(result, err)
			}
			p := (&widgets.CollaborationUI{}).Pane(q.From)
			p.Open = true
			call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
				var args map[string]any
				_ = json.Unmarshal(r.Arguments, &args)
				res, err := collaborationPhoneCall(t, f, r.Operation, r.From, args)
				done(widgets.CollaborationArgsJSON(res), err)
			}
			p.Read(q.ID, call)
			p.Submit("resolve_delivery", map[string]any{"task_id": q.ID, "delivery_id": d.ID, "reason": "User stopped tracking this uncertain notification to allow later results"}, call)
			if p.Pending != nil || p.Err != "" {
				t.Fatal("resolution failed", p.Err)
			}

			old, _ := s.Manager.Delivery(d.ID)
			if old.State != "needsAttention" || old.Resolution != "abandoned" {
				t.Fatal("native outcome fabricated", old)
			}
			f.serv.Do(func() { f.C.Update(caller, func(th *model.Thread) { th.Busy = false }) })
		})
	}
}
