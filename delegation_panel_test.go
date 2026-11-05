package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	dg "miya/internal/delegation"
)

func TestCollaborationPanelRetriesExactRequestAfterAmbiguousResponse(t *testing.T) {
	p := (&collaborationUI{}).pane("source")
	var requests []collaborationRequest
	var complete func(json.RawMessage, error)
	call := func(r collaborationRequest, done func(json.RawMessage, error)) {
		if r.Operation == "read_task" {
			done(collaborationArgsJSON(collaborationTaskView{ID: "accepted", State: dg.Running}), nil)
			return
		}
		requests = append(requests, r)
		complete = done
	}
	p.submit("cancel_task", map[string]any{"task_id": "accepted"}, call)
	original := append([]byte(nil), p.pending.Arguments...)
	complete(nil, errors.New("connection closed before acknowledgement"))
	if p.pending == nil || p.busy {
		t.Fatal("ambiguous request discarded")
	}
	p.retry(call)
	if len(requests) != 2 || !bytes.Equal(requests[1].Arguments, original) {
		t.Fatal("retry created another request ID")
	}
	complete(json.RawMessage(`{}`), nil)
	if p.pending == nil || !strings.Contains(p.err, "incomplete") {
		t.Fatal("malformed response acknowledged work")
	}
	p.retry(call)
	complete(json.RawMessage(`{"accepted":true}`), nil)
	if p.pending != nil || p.err != "" {
		t.Fatal("accepted request stayed pending")
	}
}

func TestCollaborationPanelLateResponsesCannotReplaceAnotherTaskOrSource(t *testing.T) {
	var cache collaborationUI
	a, b := cache.pane("source-a"), cache.pane("source-b")
	callbacks := map[string]func(json.RawMessage, error){}
	call := func(r collaborationRequest, done func(json.RawMessage, error)) {
		var args struct {
			ID string `json:"task_id"`
		}
		_ = json.Unmarshal(r.Arguments, &args)
		callbacks[r.From+args.ID] = done
	}
	a.read("old", call)
	a.read("new", call)
	b.read("other", call)
	callbacks["source-anew"](collaborationArgsJSON(collaborationTaskView{ID: "new", Result: dg.Result{Text: "new fixed result"}}), nil)
	callbacks["source-aold"](collaborationArgsJSON(collaborationTaskView{ID: "old", Result: dg.Result{Text: "late old result"}}), nil)
	callbacks["source-bother"](collaborationArgsJSON(collaborationTaskView{ID: "other", Result: dg.Result{Text: "other source"}}), nil)
	if a.selected != "new" || a.details["new"].Result.Text != "new fixed result" || b.details["other"].Result.Text != "other source" {
		t.Fatal("late response crossed task/source identity")
	}
}

func TestCollaborationPanelRefreshesSelectedNotificationWithoutTaskRevisionChange(t *testing.T) {
	for _, offPage := range []bool{false, true} {
		t.Run(fmt.Sprint(offPage), func(t *testing.T) {
			p := (&collaborationUI{}).pane("source")
			p.selected, p.revision = "task-selected", 7
			p.details[p.selected] = collaborationTaskView{ID: p.selected, Revision: 3, Notification: "pending"}
			reads := 0
			call := func(r collaborationRequest, done func(json.RawMessage, error)) {
				switch r.Operation {
				case "list_tasks":
					row := p.details[p.selected]
					row.Notification = "delivered"
					if offPage {
						row.ID = "newer-task"
					}
					done(collaborationArgsJSON(collaborationListView{Tasks: []collaborationTaskView{row}, Revision: 8}), nil)
				case "read_task":
					reads++
					task := p.details[p.selected]
					task.Notification = "delivered"
					done(collaborationArgsJSON(task), nil)
				}
			}
			p.refresh(&collaborationStatus{Version: 1, Revision: 8}, call, false)
			if reads != 1 || p.details[p.selected].Notification != "delivered" {
				t.Fatal("selected delivery state stayed stale", p.details[p.selected])
			}
		})
	}
}

func TestCollaborationRefreshDoesNotOpenPopover(t *testing.T) {
	pane := (&collaborationUI{}).pane("source")
	pane.refresh(&collaborationStatus{Version: 1}, func(_ collaborationRequest, done func(json.RawMessage, error)) {
		done(collaborationArgsJSON(collaborationListView{Tasks: []collaborationTaskView{{ID: "review", Title: "Review", State: dg.Completed}}}), nil)
	}, false)
	if pane.hidden() || pane.open {
		t.Fatal("refresh should reveal the task entry without opening its popover")
	}
}

func TestCollaborationTaskCardsUseSameLayoutOnBothClients(t *testing.T) {
	for _, width := range []int{1120, 320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			old := collaborationTaskView{ID: "task-old", From: "source", Thread: "target", Title: "Review compatibility", Agent: agentClaude, Workspace: "/workspace/project", State: dg.Completed, Revision: 7, ResultSummary: "Two compatibility issues", Result: dg.Result{Availability: "available", Text: "Fixed review result: two compatibility issues."}, Notification: "delivered"}
			pane := (&collaborationUI{}).pane("source")
			pane.open = true
			pane.loaded = true
			pane.revision = 7
			pane.loadedAt = time.Now()
			pane.rows = []collaborationTaskView{old}
			call := func(r collaborationRequest, done func(json.RawMessage, error)) {
				switch r.Operation {
				case "read_task":
					var args struct {
						ID string `json:"task_id"`
					}
					_ = json.Unmarshal(r.Arguments, &args)
					if args.ID == old.ID {
						done(collaborationArgsJSON(old), nil)
					} else {
						done(collaborationArgsJSON(collaborationTaskView{ID: args.ID, State: dg.Running}), nil)
					}
				case "list_tasks":
					done(collaborationArgsJSON(collaborationListView{Tasks: []collaborationTaskView{old}, Revision: 7}), nil)
				default:
					t.Errorf("unexpected action %s", r.Operation)
				}
			}
			view := collaborationPanelView{palette: taskPalette{panel, line, ink, muted}, capability: &collaborationStatus{Version: 1, Revision: 7}, connected: true, call: call, openThread: func(string) {}}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(12).Justify(ui.End).Children(func() { pane.draw(c, view) })
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
	m.collaborationCalls = map[string]*mobileCollaborationCall{
		"first":  {timer: timerA, done: func(json.RawMessage, error) { first++ }},
		"second": {timer: timerB, done: func(json.RawMessage, error) { second++ }},
	}
	m.receiveCollaboration(json.RawMessage(`{"request_id":"second","result":{"accepted":true}}`), "")
	if first != 0 || second != 1 || timerB.Stop() {
		t.Fatal("response matched wrong request or retained timeout")
	}
	m.interruptCollaborationRequests()
	if first != 1 || second != 1 || len(m.collaborationCalls) != 0 || timerA.Stop() {
		t.Fatal("disconnect retained pending callback/timeout")
	}
}
