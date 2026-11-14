package nsui

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/uiruntime"
)

type automationCall struct {
	op  string
	req automation.Request
}
type automationFake struct {
	*fakeHost
	mu         sync.Mutex
	views      []automation.View
	inbox      automation.WebhookView
	calls      []automationCall
	fail       map[string]error
	draft      automation.View
	draftReply async.Reply
	listGate   <-chan struct{}
}

func newAutomationFake() *automationFake {
	v := automation.ViewOf(automation.Automation{ID: "daily", Name: "Daily checks", Project: "/project", Agent: model.AgentCodex, Instructions: "Check the project", Request: "Every morning check the project", Schedule: "0 9 * * *", Enabled: true, Rounds: 1}, nil, false, time.Now())
	h := &automationFake{fakeHost: newFakeHost(model.Thread{ID: 42, Title: "Automation run"}), views: []automation.View{v}, inbox: automation.WebhookView{Enabled: true, URL: "https://example.test/inbox", Messages: []automation.WebhookMessage{{ID: "message", At: time.Now(), Data: "Issue opened"}}}, fail: map[string]error{}, draft: v}
	h.state.Cwd = "/project"
	return h
}
func (h *automationFake) AutomationOp(op string, req automation.Request) (any, error) {
	h.mu.Lock()
	h.calls = append(h.calls, automationCall{op, req})
	err := h.fail[op]
	gate := h.listGate
	if op == "automationList" && gate != nil {
		h.mu.Unlock()
		<-gate
		h.mu.Lock()
	}
	defer h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	switch op {
	case "automationList":
		raw, err := json.Marshal(h.views)
		return json.RawMessage(raw), err
	case "automationWebhook":
		if req.Action != "" {
			h.inbox.Enabled = req.Action == "enable"
		}
		raw, err := json.Marshal(h.inbox)
		return json.RawMessage(raw), err
	case "automationToggle":
		for i := range h.views {
			if h.views[i].ID == req.ID {
				h.views[i].Enabled = req.Enabled
			}
		}
		return nil, nil
	case "automationDraft":
		if h.draftReply != nil {
			return h.draftReply, nil
		}
		return h.draft, nil
	case "automationSave":
		v := automation.ViewOf(*req.Automation, nil, false, time.Now())
		if v.ID == "" {
			v.ID = "created"
		}
		found := false
		for i := range h.views {
			if h.views[i].ID == v.ID {
				h.views[i] = v
				found = true
			}
		}
		if !found {
			h.views = append(h.views, v)
		}
		return v, nil
	case "automationDelete":
		h.views = nil
		return nil, nil
	case "automationRunNow", "automationWake", "automationWorktree":
		return nil, nil
	}
	return nil, errors.New("unexpected operation: " + op)
}
func (h *automationFake) called(op string, match func(automation.Request) bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c.op == op && (match == nil || match(c.req)) {
			return true
		}
	}
	return false
}
func startAutomations(t *testing.T, h *automationFake) *nsgotest.App {
	t.Helper()
	t.Cleanup(func() { uiruntime.SetAppWindow(nil); current = nil })
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		return automationsPage(cx, h)
	}, nsgotest.Size(1100, 900))
}
func waitAutomationCall(app *nsgotest.App, h *automationFake, op string, match func(automation.Request) bool) {
	app.WaitUntil(2*time.Second, func() bool { return h.called(op, match) })
}

func TestAutomationsListAndInbox(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.WaitFor("id:automation-webhook", 2*time.Second)
	texts := strings.Join(app.Texts(), "\n")
	for _, want := range []string{"Daily checks", "Issue opened", "https://example.test/inbox"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in %s", want, texts)
		}
	}
	app.Press("id:automation-webhook-copy")
	if app.Clipboard != "https://example.test/inbox" {
		t.Fatalf("clipboard = %q", app.Clipboard)
	}
}
func TestAutomationsToggle(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-toggle-daily", 2*time.Second)
	app.Press("id:automation-toggle-daily")
	waitAutomationCall(app, h, "automationToggle", func(r automation.Request) bool { return r.ID == "daily" && !r.Enabled })
	app.WaitUntil(2*time.Second, func() bool { return strings.Contains(strings.Join(app.Texts(), "\n"), tr("Paused")) })
}
func TestAutomationsWebhookSwitch(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-webhook", 2*time.Second)
	app.Press("id:automation-webhook")
	waitAutomationCall(app, h, "automationWebhook", func(r automation.Request) bool { return r.Action == "disable" })
	app.WaitUntil(2*time.Second, func() bool { return strings.Contains(strings.Join(app.Texts(), "\n"), tr("Off")) })
	app.Press("id:automation-webhook")
	waitAutomationCall(app, h, "automationWebhook", func(r automation.Request) bool { return r.Action == "enable" })
}
func TestAutomationsDetailAndActions(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.Press("id:automation-open-daily")
	app.WaitFor("id:automation-detail", time.Second)
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Check the project") {
		t.Fatal("detail lacks instructions")
	}
	app.Press("id:automation-detail-run")
	waitAutomationCall(app, h, "automationRunNow", func(r automation.Request) bool { return r.ID == "daily" })
	app.WaitFor("id:automation-delete", time.Second)
	app.Press("id:automation-delete")
	if h.called("automationDelete", nil) {
		t.Fatal("deleted before confirmation")
	}
	app.Press("id:automation-delete-confirm")
	waitAutomationCall(app, h, "automationDelete", nil)
	app.WaitForGone("id:automation-detail", time.Second)
}
func TestAutomationsCallsDoNotBlockEngine(t *testing.T) {
	gate := make(chan struct{})
	h := newAutomationFake()
	h.listGate = gate
	app := startAutomations(t, h)
	// This press and its navigation must complete while the list RPC waits.
	app.Press("id:automation-new")
	app.WaitFor("id:automation-request", time.Second)
	close(gate)
	app.Press("id:automation-form-cancel")
	app.WaitFor("id:automation-open-daily", 2*time.Second)
}
func TestAutomationsDraftEditAndSave(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.Press("id:automation-new")
	app.Type("id:automation-request", "Check daily")
	app.Press("id:automation-generate")
	app.WaitFor("id:automation-change-name", 2*time.Second)
	app.Press("id:automation-change-schedule")
	app.Type("id:automation-field-schedule", "invalid")
	app.Press("id:automation-field-done-schedule")
	if !app.Exists("id:automation-field-schedule") {
		t.Fatal("invalid cron was accepted")
	}
	app.Press("id:automation-field-cancel-schedule")
	app.Press("id:automation-change-name")
	app.Clear("id:automation-field-name")
	app.Type("id:automation-field-name", "Updated daily")
	app.Press("id:automation-field-done-name")
	app.Press("id:automation-save")
	waitAutomationCall(app, h, "automationSave", func(r automation.Request) bool {
		return r.Automation != nil && r.Automation.ID == "" && r.Automation.Name == "Updated daily" && r.Automation.Project == "/project" && r.Automation.Request == "Check daily"
	})
	app.WaitFor("id:automation-detail", 2*time.Second)
	if h.called("automationRunNow", nil) {
		t.Fatal("Create unexpectedly ran the automation")
	}
}
func TestAutomationsRegeneratesAfterRequestChanges(t *testing.T) {
	gate := make(chan struct{})
	h := newAutomationFake()
	h.draftReply = func() (any, error) { <-gate; return h.draft, nil }
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.Press("id:automation-new")
	app.Type("id:automation-request", "First request")
	app.Press("id:automation-generate")
	waitAutomationCall(app, h, "automationDraft", nil)
	app.Type("id:automation-request", "Changed request")
	close(gate)
	// Editing the request releases the Generate button even while the old
	// request is in flight. A new draft can then be generated and cancelled.
	app.WaitUntil(2*time.Second, func() bool { return h.called("automationDraft", nil) })
	app.Press("id:automation-generate")
	app.WaitFor("id:automation-change-name", 2*time.Second)
	app.Press("id:automation-form-cancel")
	app.Press("id:automation-new")
	if app.Exists("id:automation-change-name") {
		t.Fatal("new form kept old draft")
	}
}
func TestAutomationsErrorAndRefresh(t *testing.T) {
	h := newAutomationFake()
	h.fail["automationList"] = errors.New("Service unavailable")
	app := startAutomations(t, h)
	app.WaitFor("id:automations-error", 2*time.Second)
	if !strings.Contains(strings.Join(app.Texts(), "\n"), "Service unavailable") {
		t.Fatal("service error not shown")
	}
	h.mu.Lock()
	delete(h.fail, "automationList")
	h.mu.Unlock()
	app.Press("id:automations-refresh")
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.WaitForGone("id:automations-error", time.Second)
}
func TestAutomationsTriggersWakeAndConversation(t *testing.T) {
	h := newAutomationFake()
	h.views[0].Triggers = []automation.Trigger{{ID: "hook", Kind: automation.TriggerIngress}}
	h.views[0].Runs = []automation.Run{{Thread: 42, Started: time.Now(), Result: automation.Passed, Summary: "All good"}}
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", 2*time.Second)
	app.Press("id:automation-open-daily")
	app.WaitFor("id:automation-detail", time.Second)
	app.Type("id:automation-wake-data-hook", "new issue")
	app.Press("id:automation-wake-hook")
	waitAutomationCall(app, h, "automationWake", func(r automation.Request) bool {
		return r.ID == "daily" && r.Trigger == "hook" && r.Data == "new issue"
	})
	app.WaitFor("id:automation-conversation-42", time.Second)
	app.Press("id:automation-conversation-42")
	app.WaitFor("id:automation-conversation", time.Second)
	if h.current != 42 {
		t.Fatalf("current = %d", h.current)
	}
	app.Press("id:automation-conversation-back")
	app.WaitFor("id:automation-detail", time.Second)
}

// Keep JSON protocol coverage independent of the controller's concrete type.
var _ Host = (*automationFake)(nil)

func TestAutomationsEmptyListStillShowsInbox(t *testing.T) {
	h := newAutomationFake()
	h.views = nil
	app := startAutomations(t, h)
	app.WaitFor("id:automation-webhook", time.Second)
	app.WaitUntil(time.Second, func() bool { return strings.Contains(strings.Join(app.Texts(), "\n"), tr("No automations yet")) })
}

func TestAutomationsRefreshRetainsFormInput(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", time.Second)
	app.Press("id:automation-new")
	app.Type("id:automation-request", "Draft in progress")
	input := app.Find("id:automation-request").ID
	h.mu.Lock()
	count := len(h.calls)
	h.mu.Unlock()
	app.Advance(5 * time.Second)
	app.WaitUntil(time.Second, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.calls) >= count+2 })
	app.Type("id:automation-request", " more")
	if v := app.Find("id:automation-request"); v.ID != input || v.Text != "Draft in progress more" {
		t.Fatalf("refresh replaced or changed the input: %v, %q", v.ID, v.Text)
	}
}

func TestAutomationsSaveAndRunAndEditingGuard(t *testing.T) {
	h := newAutomationFake()
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", time.Second)
	app.Press("id:automation-open-daily")
	app.Press("id:automation-edit")
	app.Press("id:automation-change-name")
	if !app.Find("id:automation-save").Props.Disabled {
		t.Fatal("Save enabled with an unfinished field edit")
	}
	app.Press("id:automation-field-cancel-name")
	app.Press("id:automation-save-run")
	waitAutomationCall(app, h, "automationSave", func(r automation.Request) bool { return r.Automation.ID == "daily" })
	waitAutomationCall(app, h, "automationRunNow", func(r automation.Request) bool { return r.ID == "daily" })
}

func TestAutomationsMutationFailureKeepsDetail(t *testing.T) {
	h := newAutomationFake()
	h.fail["automationDelete"] = errors.New("Delete refused")
	app := startAutomations(t, h)
	app.WaitFor("id:automation-open-daily", time.Second)
	app.Press("id:automation-open-daily")
	app.Press("id:automation-delete")
	app.Press("id:automation-delete-confirm")
	app.WaitFor("id:automations-error", time.Second)
	if !app.Exists("id:automation-detail") || !strings.Contains(strings.Join(app.Texts(), "\n"), "Delete refused") {
		t.Fatal("failed delete hid the detail or its error")
	}
}
