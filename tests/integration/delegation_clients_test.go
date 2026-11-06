//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/remote"
	"miya/internal/widgets"
)

func collaborationPhoneCall(t *testing.T, f *serviceFixture, operation, from string, args map[string]any) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw := widgets.CollaborationArgsJSON(appmodel.CollaborationRequest{Operation: operation, From: from, Arguments: widgets.CollaborationArgsJSON(args)})
	frame := f.srv.C.PhoneCollaboration(ctx, remote.Request{Op: "collaboration", RequestID: "phone-correlation", Collaboration: raw})
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
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	var from string
	f.serv.Do(func() {
		from = f.srv.C.ThreadRef(f.srv.C.ThreadWithID(caller)).URL()
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
	for _, width := range []int{1120, 320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			p := (&widgets.CollaborationUI{}).Pane(from)
			p.Open = true
			var capability *model.CollaborationStatus
			f.serv.Do(func() { capability = f.srv.C.CollaborationCapabilities() })
			view := widgets.CollaborationPanelView{Palette: widgets.TaskPalette{Surface: widgets.Panel, Border: widgets.Line, Ink: widgets.Ink, Muted: widgets.Muted}, Capability: capability, Connected: true, Call: call, OpenThread: func(string) {}}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).FillWidth().Padding(12).Children(func() { p.Draw(c, view) }) }, width, 750)
			tt.Frame()
			if !tt.HasText(task.Title) || !tt.HasText("Claude Code · Completed") || !tt.HasText(task.Result.Text) {
				t.Fatal("service task data did not reach task cards", tt.Texts())
			}
			if err := tt.Click(task.Title); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !tt.HasText(task.Result.Text) || p.Details[task.ID].Thread != task.Target {
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
			caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
			f.serv.Do(func() { f.srv.C.Update(caller, func(th *model.Thread) { th.Busy = true }) })
			q := collaborationStoredFixtureTask(t, f, caller)
			s := f.srv.C.Collaboration
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
			var cap *model.CollaborationStatus
			f.serv.Do(func() { cap = f.srv.C.CollaborationCapabilities() })
			v := widgets.CollaborationPanelView{Palette: widgets.TaskPalette{Surface: widgets.Panel, Border: widgets.Line, Ink: widgets.Ink, Muted: widgets.Muted}, Capability: cap, Connected: true, Call: call}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Padding(12).Justify(ui.End).Children(func() { p.Draw(c, v) }) }, width, 750)
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
			if p.Pending != nil || p.Err != "" || !tt.HasText("Notification tracking stopped") {
				t.Fatal("resolution failed", p.Err, tt.Texts())
			}
			old, _ := s.Manager.Delivery(d.ID)
			if old.State != "needsAttention" || old.Resolution != "abandoned" {
				t.Fatal("native outcome fabricated", old)
			}
			f.serv.Do(func() { f.srv.C.Update(caller, func(th *model.Thread) { th.Busy = false }) })
		})
	}
}
