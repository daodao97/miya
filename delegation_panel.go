package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"miya/internal/claude"
	dg "miya/internal/delegation"
)

type collaborationTaskView struct {
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

type collaborationListView struct {
	Tasks        []collaborationTaskView `json:"tasks"`
	Next         string                  `json:"next_cursor"`
	Revision     uint64                  `json:"revision"`
	Paused       bool                    `json:"paused"`
	StorageError string                  `json:"storage_error"`
	Stale        bool                    `json:"stale"`
}

type collaborationCall func(collaborationRequest, func(json.RawMessage, error))

// Each stable source reference owns its own cache and immutable pending
// request. Navigation and disconnects must not reuse another conversation's ID.
type collaborationUI struct {
	panes map[string]*collaborationPanel
}
type collaborationPanel struct {
	from                           string
	open                           bool
	loaded, loading, loadingDetail bool
	rows                           []collaborationTaskView
	details                        map[string]collaborationTaskView
	next, selected                 string
	paused                         bool
	revision                       uint64
	loadedAt                       time.Time
	loadGeneration                 uint64
	detailGeneration               uint64
	err, storageError              string
	abandon, reason                string
	pending                        *collaborationRequest
	busy                           bool
	actionGeneration               uint64
}

// Navigation dismisses task overlays on both the outgoing and destination
// conversations; cached task details may remain, but must not cover the page.
func (u *collaborationUI) closePopovers() {
	for _, pane := range u.panes {
		pane.open = false
	}
}

func (u *collaborationUI) pane(from string) *collaborationPanel {
	if u.panes == nil {
		u.panes = map[string]*collaborationPanel{}
	}
	if u.panes[from] == nil {
		u.panes[from] = &collaborationPanel{from: from, details: map[string]collaborationTaskView{}}
	}
	return u.panes[from]
}
func collaborationArgsJSON(v any) json.RawMessage { data, _ := json.Marshal(v); return data }

func (p *collaborationPanel) refresh(cap *collaborationStatus, call collaborationCall, more bool) {
	if p.from == "" || p.loading {
		return
	}
	if !more && p.loaded && cap != nil && p.revision == cap.Revision && time.Since(p.loadedAt) < 5*time.Second {
		return
	}
	if !more && !p.loadedAt.IsZero() && !p.loaded && time.Since(p.loadedAt) < 3*time.Second {
		return
	}
	p.loading = true
	p.loadGeneration++
	gen := p.loadGeneration
	args := map[string]any{"limit": 25}
	if more {
		args["cursor"] = p.next
	}
	call(collaborationRequest{Operation: "list_tasks", From: p.from, Arguments: collaborationArgsJSON(args)}, func(raw json.RawMessage, err error) {
		if p.loadGeneration != gen {
			return
		}
		p.loading = false
		p.loadedAt = time.Now()
		if err != nil {
			p.err = err.Error()
			return
		}
		var result collaborationListView
		if err := json.Unmarshal(raw, &result); err != nil {
			p.err = err.Error()
			return
		}
		if more {
			p.rows = appendUniqueTasks(p.rows, result.Tasks)
		} else {
			p.rows = result.Tasks
		}
		changed := p.revision != result.Revision
		p.next, p.paused, p.revision, p.storageError = result.Next, result.Paused, result.Revision, result.StorageError
		p.loaded = true
		if p.pending == nil {
			p.err = ""
		}
		if p.selected != "" && !p.loadingDetail {
			// Watches and deliveries can change without a task revision. Use
			// their row metadata to avoid refetching a large completed result
			// for every unrelated task's progress update.
			found := false
			for _, row := range p.rows {
				if row.ID == p.selected {
					found = true
					detail := p.details[p.selected]
					if detail.Revision != row.Revision || detail.Notification != row.Notification || detail.DeliveryReason != row.DeliveryReason {
						p.read(row.ID, call)
					}
					break
				}
			}
			// A selected task on a later page still needs to track revisions.
			if !found && changed {
				p.read(p.selected, call)
			}
		}
	})
}
func appendUniqueTasks(old, next []collaborationTaskView) []collaborationTaskView {
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
func (p *collaborationPanel) read(id string, call collaborationCall) {
	if p.details == nil {
		p.details = map[string]collaborationTaskView{}
	}
	p.selected = id
	p.detailGeneration++
	gen := p.detailGeneration
	p.loadingDetail = true
	call(collaborationRequest{Operation: "read_task", From: p.from, Arguments: collaborationArgsJSON(map[string]any{"task_id": id})}, func(raw json.RawMessage, err error) {
		if p.detailGeneration != gen {
			return
		}
		p.loadingDetail = false
		if err != nil {
			p.err = err.Error()
			return
		}
		var task collaborationTaskView
		if err := json.Unmarshal(raw, &task); err != nil {
			p.err = err.Error()
			return
		}
		if task.ID != id {
			p.err = tr("Task response does not match the selected task")
			return
		}
		p.details[id] = task
	})
}
func (p *collaborationPanel) submit(op string, args map[string]any, call collaborationCall) {
	if p.pending != nil || p.busy {
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	if op != "resume" {
		args["request_id"] = claude.NewSessionID()
	}
	p.pending = &collaborationRequest{Operation: op, From: p.from, Arguments: collaborationArgsJSON(args)}
	p.retry(call)
}
func (p *collaborationPanel) retry(call collaborationCall) {
	if p.pending == nil || p.busy {
		return
	}
	p.busy = true
	p.err = ""
	p.actionGeneration++
	gen := p.actionGeneration
	request := *p.pending
	call(request, func(raw json.RawMessage, err error) {
		if p.actionGeneration != gen {
			return
		}
		p.busy = false
		if err != nil {
			p.err = err.Error()
			// These errors prove admission failed. Ambiguous transport/storage
			// failures retain the same request, so retry cannot duplicate work.
			for _, code := range []string{"INVALID_ARGUMENT", "INVALID_STATE", "DELIVERY_NOT_FOUND", "REQUEST_CONFLICT", "THREAD_BUSY", "THREAD_NOT_FOUND", "QUOTA_EXHAUSTED", "ACCESS_DENIED", "DELEGATION_DEPTH", "DELEGATION_CYCLE", "AGENT_UNAVAILABLE", "PARENT_STOPPING"} {
				if strings.Contains(err.Error(), code+":") {
					p.pending = nil
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
			p.err = err.Error()
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
			p.err = tr("Task response is incomplete; retry the original request")
			return
		}
		p.pending = nil
		p.loadedAt = time.Time{}
		p.loaded = false
		p.err = ""
		p.abandon = ""
		p.reason = ""
		if request.Operation == "resume" {
			p.paused = *accepted.Paused
		}
		if accepted.ID != "" && p.selected == accepted.ID {
			p.read(accepted.ID, call)
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

func (p *collaborationPanel) title() string {
	if len(p.rows) == 0 {
		return tr("Agent tasks")
	}
	return fmt.Sprintf(tr("Agent tasks (%d)"), len(p.rows))
}
