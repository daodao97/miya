package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	dg "miya/internal/delegation"
	"miya/internal/mobile"
	"miya/internal/model"
	"miya/internal/widgets"
)

func TestCollaborationPanelRetriesExactRequestAfterAmbiguousResponse(t *testing.T) {
	p := (&widgets.CollaborationUI{}).Pane("source")
	var requests []appmodel.CollaborationRequest
	var complete func(json.RawMessage, error)
	call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		if r.Operation == "read_task" {
			done(widgets.CollaborationArgsJSON(widgets.CollaborationTaskView{ID: "accepted", State: dg.Running}), nil)
			return
		}
		requests = append(requests, r)
		complete = done
	}
	p.Submit("cancel_task", map[string]any{"task_id": "accepted"}, call)
	original := append([]byte(nil), p.Pending.Arguments...)
	complete(nil, errors.New("connection closed before acknowledgement"))
	if p.Pending == nil || p.Busy {
		t.Fatal("ambiguous request discarded")
	}
	p.Retry(call)
	if len(requests) != 2 || !bytes.Equal(requests[1].Arguments, original) {
		t.Fatal("retry created another request ID")
	}
	complete(json.RawMessage(`{}`), nil)
	if p.Pending == nil || !strings.Contains(p.Err, "incomplete") {
		t.Fatal("malformed response acknowledged work")
	}
	p.Retry(call)
	complete(json.RawMessage(`{"accepted":true}`), nil)
	if p.Pending != nil || p.Err != "" {
		t.Fatal("accepted request stayed pending")
	}
}

func TestCollaborationPanelLateResponsesCannotReplaceAnotherTaskOrSource(t *testing.T) {
	var cache widgets.CollaborationUI
	a, b := cache.Pane("source-a"), cache.Pane("source-b")
	callbacks := map[string]func(json.RawMessage, error){}
	call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		var args struct {
			ID string `json:"task_id"`
		}
		_ = json.Unmarshal(r.Arguments, &args)
		callbacks[r.From+args.ID] = done
	}
	a.Read("old", call)
	a.Read("new", call)
	b.Read("other", call)
	callbacks["source-anew"](widgets.CollaborationArgsJSON(widgets.CollaborationTaskView{ID: "new", Result: dg.Result{Text: "new fixed result"}}), nil)
	callbacks["source-aold"](widgets.CollaborationArgsJSON(widgets.CollaborationTaskView{ID: "old", Result: dg.Result{Text: "late old result"}}), nil)
	callbacks["source-bother"](widgets.CollaborationArgsJSON(widgets.CollaborationTaskView{ID: "other", Result: dg.Result{Text: "other source"}}), nil)
	if a.Selected != "new" || a.Details["new"].Result.Text != "new fixed result" || b.Details["other"].Result.Text != "other source" {
		t.Fatal("late response crossed task/source identity")
	}
}

func TestCollaborationPanelRefreshesSelectedNotificationWithoutTaskRevisionChange(t *testing.T) {
	for _, offPage := range []bool{false, true} {
		t.Run(fmt.Sprint(offPage), func(t *testing.T) {
			p := (&widgets.CollaborationUI{}).Pane("source")
			p.Selected, p.Revision = "task-selected", 7
			p.Details[p.Selected] = widgets.CollaborationTaskView{ID: p.Selected, Revision: 3, Notification: "pending"}
			reads := 0
			call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
				switch r.Operation {
				case "list_tasks":
					row := p.Details[p.Selected]
					row.Notification = "delivered"
					if offPage {
						row.ID = "newer-task"
					}
					done(widgets.CollaborationArgsJSON(widgets.CollaborationListView{Tasks: []widgets.CollaborationTaskView{row}, Revision: 8}), nil)
				case "read_task":
					reads++
					task := p.Details[p.Selected]
					task.Notification = "delivered"
					done(widgets.CollaborationArgsJSON(task), nil)
				}
			}
			p.Refresh(&model.CollaborationStatus{Version: 1, Revision: 8}, call, false)
			if reads != 1 || p.Details[p.Selected].Notification != "delivered" {
				t.Fatal("selected delivery state stayed stale", p.Details[p.Selected])
			}
		})
	}
}

func TestCollaborationRefreshDoesNotOpenPopover(t *testing.T) {
	pane := (&widgets.CollaborationUI{}).Pane("source")
	pane.Refresh(&model.CollaborationStatus{Version: 1}, func(_ appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		done(widgets.CollaborationArgsJSON(widgets.CollaborationListView{Tasks: []widgets.CollaborationTaskView{{ID: "review", Title: "Review", State: dg.Completed}}}), nil)
	}, false)
	if pane.Hidden() || pane.Open {
		t.Fatal("refresh should reveal the task entry without opening its popover")
	}
}

func TestCollaborationTaskCardsUseSameLayoutOnBothClients(t *testing.T) {
	for _, width := range []int{1120, 320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			old := widgets.CollaborationTaskView{ID: "task-old", From: "source", Thread: "target", Title: "Review compatibility", Agent: appmodel.AgentClaude, Workspace: "/workspace/project", State: dg.Completed, Revision: 7, ResultSummary: "Two compatibility issues", Result: dg.Result{Availability: "available", Text: "Fixed review result: two compatibility issues."}, Notification: "delivered"}
			pane := (&widgets.CollaborationUI{}).Pane("source")
			pane.Open = true
			pane.Loaded = true
			pane.Revision = 7
			pane.LoadedAt = time.Now()
			pane.Rows = []widgets.CollaborationTaskView{old}
			call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
				switch r.Operation {
				case "read_task":
					var args struct {
						ID string `json:"task_id"`
					}
					_ = json.Unmarshal(r.Arguments, &args)
					if args.ID == old.ID {
						done(widgets.CollaborationArgsJSON(old), nil)
					} else {
						done(widgets.CollaborationArgsJSON(widgets.CollaborationTaskView{ID: args.ID, State: dg.Running}), nil)
					}
				case "list_tasks":
					done(widgets.CollaborationArgsJSON(widgets.CollaborationListView{Tasks: []widgets.CollaborationTaskView{old}, Revision: 7}), nil)
				default:
					t.Errorf("unexpected action %s", r.Operation)
				}
			}
			view := widgets.CollaborationPanelView{Palette: widgets.TaskPalette{Surface: widgets.Panel, Border: widgets.Line, Ink: widgets.Ink, Muted: widgets.Muted}, Capability: &model.CollaborationStatus{Version: 1, Revision: 7}, Connected: true, Call: call, OpenThread: func(string) {}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(12).Justify(ui.End).Children(func() { pane.Draw(c, view) })
			}, width, 750)
			tt.Frame()
			if !tt.HasText("Two compatibility issues") || !tt.HasText("Results returned") {
				t.Fatal("card omitted fixed summary or delivery state", tt.Texts())
			}
			if err := tt.Click(old.Title); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !tt.HasText(old.Result.Text) {
				t.Fatal("fixed result missing", tt.Texts())
			}
			saveCommandSnapshot(t, tt, fmt.Sprintf("task-result-%d.png", width))
		})
	}
}

func TestMobileCollaborationResponsesAreCorrelatedAndCancelledOnDisconnect(t *testing.T) {
	m := testMobileApp()
	var first, second int
	timerA := time.AfterFunc(time.Hour, func() {})
	timerB := time.AfterFunc(time.Hour, func() {})
	m.CollaborationCalls = map[string]*mobile.MobileCollaborationCall{
		"first":  {Timer: timerA, Done: func(json.RawMessage, error) { first++ }},
		"second": {Timer: timerB, Done: func(json.RawMessage, error) { second++ }},
	}
	m.ReceiveCollaboration(json.RawMessage(`{"request_id":"second","result":{"accepted":true}}`), "")
	if first != 0 || second != 1 || timerB.Stop() {
		t.Fatal("response matched wrong request or retained timeout")
	}
	m.InterruptCollaborationRequests()
	if first != 1 || second != 1 || len(m.CollaborationCalls) != 0 || timerA.Stop() {
		t.Fatal("disconnect retained pending callback/timeout")
	}
}
