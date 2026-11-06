//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	dg "miya/internal/delegation"
	"miya/internal/remote"

	"github.com/egoist/mygo/ui"
)

func collaborationPhoneCall(t *testing.T, f *serviceFixture, operation, from string, args map[string]any) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw := collaborationArgsJSON(collaborationRequest{Operation: operation, From: from, Arguments: collaborationArgsJSON(args)})
	frame := f.srv.c.phoneCollaboration(ctx, remote.Request{Op: "collaboration", RequestID: "phone-correlation", Collaboration: raw})
	var response struct {
		RequestID string         `json:"request_id"`
		Result    map[string]any `json:"result"`
	}
	if err := json.Unmarshal(frame.Data, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "phone-correlation" {
		t.Fatal("phone response lost its request correlation")
	}
	if frame.Error != "" {
		return response.Result, fmt.Errorf("%s", frame.Error)
	}
	return response.Result, nil
}

func TestCollaborationPhoneDispatchAndDesktopCardsShareFixedTaskData(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "source")
	var from string
	f.serv.Do(func() { from = f.srv.c.threadRef(f.srv.c.threadWithID(caller)).URL() })
	created, err := collaborationPhoneCall(t, f, "start_task", from, map[string]any{"request_id": "phone-dispatch", "agent": agentClaude, "task": "Review compatibility", "title": "Compatibility review", "notify": "none"})
	if err != nil {
		t.Fatal(err)
	}
	task := waitCollaborationTask(t, f, created["task_id"].(string))
	desktop, err := collaborationUserCall(t, f, "read_task", from, map[string]any{"task_id": task.ID})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := collaborationPhoneCall(t, f, "read_task", from, map[string]any{"task_id": task.ID})
	if err != nil || !reflect.DeepEqual(desktop, phone) || desktop["agent"] != agentClaude {
		t.Fatal("desktop and phone disagree about the same execution", desktop, phone, err)
	}
	call := func(r collaborationRequest, done func(json.RawMessage, error)) {
		var args map[string]any
		if err := json.Unmarshal(r.Arguments, &args); err != nil {
			done(nil, err)
			return
		}
		result, err := collaborationUserCall(t, f, r.Operation, r.From, args)
		done(collaborationArgsJSON(result), err)
	}
	for _, width := range []int{1120, 320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			p := (&collaborationUI{}).pane(from)
			p.open = true
			var capability *collaborationStatus
			f.serv.Do(func() { capability = f.srv.c.collaborationCapabilities() })
			view := collaborationPanelView{palette: taskPalette{panel, line, ink, muted}, capability: capability, connected: true, call: call, openThread: func(string) {}}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).FillWidth().Padding(12).Children(func() { p.draw(c, view) }) }, width, 750)
			tt.Frame()
			if !tt.HasText(task.Title) || !tt.HasText("Claude Code · Completed") || !tt.HasText(task.Result.Text) {
				t.Fatal("service task data did not reach task cards", tt.Texts())
			}
			if err := tt.Click(task.Title); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !tt.HasText(task.Result.Text) || p.details[task.ID].Thread != task.Target {
				t.Fatal("task detail lost the fixed result/target", tt.Texts())
			}
			saveCommandSnapshot(t, tt, fmt.Sprintf("service-task-result-%d.png", width))
		})
	}
}

func TestCollaborationPhoneAndDesktopCanResolveUnknownNotification(t *testing.T) {
	for _, width := range []int{320, 390, 1120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			f := newServiceFixture(t)
			caller := collaborationCaller(t, f, agentClaude, "source")
			f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = true }) })
			q := collaborationStoredFixtureTask(t, f, caller)
			s := f.srv.c.collaboration
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
			p := (&collaborationUI{}).pane(q.From)
			p.open = true
			call := func(r collaborationRequest, done func(json.RawMessage, error)) {
				var args map[string]any
				_ = json.Unmarshal(r.Arguments, &args)
				res, err := collaborationPhoneCall(t, f, r.Operation, r.From, args)
				done(collaborationArgsJSON(res), err)
			}
			p.read(q.ID, call)
			var cap *collaborationStatus
			f.serv.Do(func() { cap = f.srv.c.collaborationCapabilities() })
			v := collaborationPanelView{palette: taskPalette{panel, line, ink, muted}, capability: cap, connected: true, call: call}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Padding(12).Justify(ui.End).Children(func() { p.draw(c, v) }) }, width, 750)
			tt.Frame()
			if err := tt.Click("Stop tracking this notification"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			saveCommandSnapshot(t, tt, fmt.Sprintf("notification-resolution-%d.png", width))
			if err := tt.Click("Allow later results"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if p.pending != nil || p.err != "" || !tt.HasText("Notification tracking stopped") {
				t.Fatal("resolution failed", p.err, tt.Texts())
			}
			old, _ := s.Manager.Delivery(d.ID)
			if old.State != "needsAttention" || old.Resolution != "abandoned" {
				t.Fatal("native outcome fabricated", old)
			}
			f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = false }) })
		})
	}
}
