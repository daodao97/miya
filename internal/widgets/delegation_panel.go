package widgets

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/textutil"
)

type CollaborationTaskView struct {
	ID              string    `json:"task_id"`
	From            string    `json:"from"`
	Thread          string    `json:"thread"`
	Agent           string    `json:"agent"`
	Title           string    `json:"title"`
	Workspace       string    `json:"workspace"`
	State           dg.State  `json:"state"`
	Revision        uint64    `json:"revision"`
	Progress        string    `json:"progress"`
	ResultSummary   string    `json:"result_summary"`
	WaitCause       string    `json:"wait_cause"`
	RecheckError    string    `json:"recheck_error"`
	Resolution      string    `json:"resolution"`
	CancelRequested bool      `json:"cancel_requested"`
	Notification    string    `json:"notification"`
	DeliveryReason  string    `json:"delivery_reason"`
	DeliveryID      string    `json:"delivery_id"`
	Result          dg.Result `json:"result"`
}

type CollaborationListView struct {
	Tasks        []CollaborationTaskView `json:"tasks"`
	Next         string                  `json:"next_cursor"`
	Revision     uint64                  `json:"revision"`
	Paused       bool                    `json:"paused"`
	StorageError string                  `json:"storage_error"`
	Stale        bool                    `json:"stale"`
}

type collaborationCall func(appmodel.CollaborationRequest, func(json.RawMessage, error))

// Each stable source reference owns its own cache and immutable pending
// request. Navigation and disconnects must not reuse another conversation's ID.
type CollaborationUI struct {
	panes map[string]*CollaborationPanel
}

type CollaborationPanel struct {
	From                           string
	Open                           bool
	Loaded, loading, loadingDetail bool
	Rows                           []CollaborationTaskView
	Details                        map[string]CollaborationTaskView
	next, Selected                 string
	paused                         bool
	Revision                       uint64
	LoadedAt                       time.Time
	loadGeneration                 uint64
	detailGeneration               uint64
	Err, storageError              string
	abandon, reason                string
	Pending                        *appmodel.CollaborationRequest
	Busy                           bool
	actionGeneration               uint64
}

// Navigation dismisses task overlays on both the outgoing and destination
// conversations; cached task details may remain, but must not cover the page.
func (u *CollaborationUI) ClosePopovers() {
	for _, pane := range u.panes {
		pane.Open = false
	}
}

func (u *CollaborationUI) Pane(from string) *CollaborationPanel {
	if u.panes == nil {
		u.panes = map[string]*CollaborationPanel{}
	}
	if u.panes[from] == nil {
		u.panes[from] = &CollaborationPanel{From: from, Details: map[string]CollaborationTaskView{}}
	}
	return u.panes[from]
}

func CollaborationArgsJSON(v any) json.RawMessage { data, _ := json.Marshal(v); return data }

func (p *CollaborationPanel) Refresh(cap *model.CollaborationStatus, call collaborationCall, more bool) {
	if p.From == "" || p.loading {
		return
	}
	if !more && p.Loaded && cap != nil && p.Revision == cap.Revision && time.Since(p.LoadedAt) < 5*time.Second {
		return
	}
	if !more && !p.LoadedAt.IsZero() && !p.Loaded && time.Since(p.LoadedAt) < 3*time.Second {
		return
	}
	p.loading = true
	p.loadGeneration++
	gen := p.loadGeneration
	args := map[string]any{"limit": 25}
	if more {
		args["cursor"] = p.next
	}
	call(appmodel.CollaborationRequest{Operation: "list_tasks", From: p.From, Arguments: CollaborationArgsJSON(args)}, func(raw json.RawMessage, err error) {
		if p.loadGeneration != gen {
			return
		}
		p.loading = false
		p.LoadedAt = time.Now()
		if err != nil {
			p.Err = err.Error()
			return
		}
		var result CollaborationListView
		if err := json.Unmarshal(raw, &result); err != nil {
			p.Err = err.Error()
			return
		}
		if more {
			p.Rows = appendUniqueTasks(p.Rows, result.Tasks)
		} else {
			p.Rows = result.Tasks
		}
		changed := p.Revision != result.Revision
		p.next, p.paused, p.Revision, p.storageError = result.Next, result.Paused, result.Revision, result.StorageError
		p.Loaded = true
		if p.Pending == nil {
			p.Err = ""
		}
		if p.Selected != "" && !p.loadingDetail {
			// Watches and deliveries can change without a task revision. Use
			// their row metadata to avoid refetching a large completed result
			// for every unrelated task's progress update.
			found := false
			for _, row := range p.Rows {
				if row.ID == p.Selected {
					found = true
					detail := p.Details[p.Selected]
					if detail.Revision != row.Revision || detail.Notification != row.Notification || detail.DeliveryReason != row.DeliveryReason {
						p.Read(row.ID, call)
					}
					break
				}
			}
			// A selected task on a later page still needs to track revisions.
			if !found && changed {
				p.Read(p.Selected, call)
			}
		}
	})
}

func appendUniqueTasks(old, next []CollaborationTaskView) []CollaborationTaskView {
	seen := map[string]bool{}
	for _, row := range old {
		seen[row.ID] = true
	}
	for _, row := range next {
		if !seen[row.ID] {
			old = append(old, row)
			seen[row.ID] = true
		}
	}
	return old
}

func (p *CollaborationPanel) Read(id string, call collaborationCall) {
	if p.Details == nil {
		p.Details = map[string]CollaborationTaskView{}
	}
	p.Selected = id
	p.detailGeneration++
	gen := p.detailGeneration
	p.loadingDetail = true
	call(appmodel.CollaborationRequest{Operation: "read_task", From: p.From, Arguments: CollaborationArgsJSON(map[string]any{"task_id": id})}, func(raw json.RawMessage, err error) {
		if p.detailGeneration != gen {
			return
		}
		p.loadingDetail = false
		if err != nil {
			p.Err = err.Error()
			return
		}
		var task CollaborationTaskView
		if err := json.Unmarshal(raw, &task); err != nil {
			p.Err = err.Error()
			return
		}
		if task.ID != id {
			p.Err = tr("Task response does not match the selected task")
			return
		}
		p.Details[id] = task
	})
}

func (p *CollaborationPanel) Submit(op string, args map[string]any, call collaborationCall) {
	if p.Pending != nil || p.Busy {
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	if op != "resume" {
		args["request_id"] = textutil.NewID()
	}
	p.Pending = &appmodel.CollaborationRequest{Operation: op, From: p.From, Arguments: CollaborationArgsJSON(args)}
	p.Retry(call)
}

func (p *CollaborationPanel) Retry(call collaborationCall) {
	if p.Pending == nil || p.Busy {
		return
	}
	p.Busy = true
	p.Err = ""
	p.actionGeneration++
	gen := p.actionGeneration
	request := *p.Pending
	call(request, func(raw json.RawMessage, err error) {
		if p.actionGeneration != gen {
			return
		}
		p.Busy = false
		if err != nil {
			p.Err = err.Error()
			// These errors prove admission failed. Ambiguous transport/storage
			// failures retain the same request, so retry cannot duplicate work.
			for _, code := range []string{"INVALID_ARGUMENT", "INVALID_STATE", "DELIVERY_NOT_FOUND", "REQUEST_CONFLICT", "THREAD_BUSY", "THREAD_NOT_FOUND", "QUOTA_EXHAUSTED", "ACCESS_DENIED", "DELEGATION_DEPTH", "DELEGATION_CYCLE", "AGENT_UNAVAILABLE", "PARENT_STOPPING"} {
				if strings.Contains(err.Error(), code+":") {
					p.Pending = nil
					break
				}
			}
			return
		}
		var accepted struct {
			ID       string `json:"task_id"`
			Paused   *bool  `json:"paused"`
			Accepted bool   `json:"accepted"`
		}
		if err := json.Unmarshal(raw, &accepted); err != nil {
			p.Err = err.Error()
			return
		}
		creating := request.Operation == "start_task" || request.Operation == "send_thread"
		valid := accepted.Accepted
		if creating {
			valid = accepted.ID != ""
		}
		if request.Operation == "resume" {
			valid = accepted.Paused != nil
		}
		if !valid {
			p.Err = tr("Task response is incomplete; retry the original request")
			return
		}
		p.Pending = nil
		p.LoadedAt = time.Time{}
		p.Loaded = false
		p.Err = ""
		p.abandon = ""
		p.reason = ""
		if request.Operation == "resume" {
			p.paused = *accepted.Paused
		}
		if accepted.ID != "" && p.Selected == accepted.ID {
			p.Read(accepted.ID, call)
		}
	})
}

func collaborationStateLabel(state dg.State) string {
	switch state {
	case dg.Queued:
		return tr("Queued")
	case dg.Starting:
		return tr("Starting")
	case dg.Running:
		return tr("Running")
	case dg.Completed:
		return tr("Completed")
	case dg.Failed:
		return tr("Failed")
	case dg.Interrupted:
		return tr("Stopped")
	case dg.NeedsAttention:
		return tr("Needs attention")
	}
	return string(state)
}

func collaborationNotificationLabel(state string) string {
	switch state {
	case "watching":
		return tr("Results will be returned automatically")
	case "pending":
		return tr("Result notification pending")
	case "submitting":
		return tr("Returning results")
	case "delivered":
		return tr("Results returned")
	case "expired":
		return tr("Result notification expired")
	case "needsAttention":
		return tr("Result delivery needs verification")
	case "abandoned":
		return tr("Notification tracking stopped")
	}
	return ""
}

func (p *CollaborationPanel) title() string {
	if len(p.Rows) == 0 {
		return tr("Agent tasks")
	}
	return fmt.Sprintf(tr("Agent tasks (%d)"), len(p.Rows))
}
