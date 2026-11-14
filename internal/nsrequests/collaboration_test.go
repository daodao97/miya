package nsrequests

import (
	"encoding/json"
	"fmt"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/nswidgets"
	"strings"
	"testing"
)

type taskHost struct{ opened string }

func (*taskHost) CollaborationCapabilities() *model.CollaborationStatus {
	return &model.CollaborationStatus{Version: 1, Operations: []string{"resolve_delivery"}}
}
func (*taskHost) ReadOnly() bool             { return false }
func (h *taskHost) OpenReference(raw string) { h.opened = raw }
func TestMigratedTaskCardsKeepFixedResultAtDesktopAndPhoneWidths(t *testing.T) {
	for _, width := range []core.Points{320, 390, 1120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			row := CollaborationTaskView{ID: "task", From: "source", Thread: "target", Agent: model.AgentClaude, Title: "Review compatibility", State: dg.Completed, ResultSummary: "Two compatibility issues", Result: dg.Result{Text: "Fixed review result: two compatibility issues."}, Notification: "needsAttention", DeliveryID: "delivery"}
			p := (&CollaborationUI{}).Pane("source")
			p.Loaded = true
			p.Rows = []CollaborationTaskView{row}
			h := new(taskHost)
			resolved := false
			call := func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
				switch r.Operation {
				case "read_task":
					done(CollaborationArgsJSON(row), nil)
				case "resolve_delivery":
					var a map[string]any
					_ = json.Unmarshal(r.Arguments, &a)
					if a["delivery_id"] != "delivery" {
						t.Fatal(a)
					}
					resolved = true
					row.Notification = "abandoned"
					done(json.RawMessage(`{"accepted":true}`), nil)
				default:
					t.Fatalf("unexpected action %s", r.Operation)
				}
			}
			app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				cx.SetTheme(nswidgets.Theme())
				rev := core.NewSignal(cx, 0)
				return CollaborationContent(cx, h, p, call, func() { rev.Update(func(n int) int { return n + 1 }) }, func() { rev.Get() })
			}, nsgotest.Size(width, 750))
			if !strings.Contains(strings.Join(app.Texts(), " "), row.ResultSummary) {
				t.Fatal(app.Texts())
			}
			app.Press("id:collaboration-task-task")
			if !strings.Contains(strings.Join(app.Texts(), " "), row.Result.Text) || p.Details["task"].Thread != "target" {
				t.Fatal("fixed result or stable target lost")
			}
			app.Press("id:collaboration-open-target")
			if h.opened != "target" {
				t.Fatal("target navigation lost identity")
			}
			app.Press("id:collaboration-resolve-delivery")
			if resolved {
				t.Fatal("notification resolved without confirmation")
			}
			app.Press("id:collaboration-confirm-delivery")
			if !resolved || p.Pending != nil || p.Err != "" {
				t.Fatal("delivery resolution failed", p.Err)
			}
		})
	}
}
