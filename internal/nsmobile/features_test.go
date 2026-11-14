package nsmobile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	dg "miya/internal/delegation"
	"miya/internal/interaction"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/nsrequests"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func phoneJSON(v any) json.RawMessage     { raw, _ := json.Marshal(v); return raw }
func phoneTexts(app *nsgotest.App) string { return strings.Join(app.Texts(), "\n") }
func showPhoneRequest(t *testing.T, raw string) (*nsgotest.App, *App, chan remote.Request, interaction.Request) {
	t.Helper()
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	var a interaction.Request
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	changePhone(app, m, func() { m.State.Approvals = []interaction.Request{a} })
	app.Press("id:review-" + string(a.ID))
	app.Settle()
	return app, m, sent, a
}
func TestPhoneApprovalAcknowledgementAndVersion(t *testing.T) {
	app, m, sent, a := showPhoneRequest(t, `{"ID":"1","Method":"item/commandExecution/requestApproval","command":"go test ./..."}`)
	if !strings.Contains(phoneTexts(app), "go test ./...") {
		t.Fatal(phoneTexts(app))
	}
	if h := app.Find("id:allow").Frame.Size.Height; h < 44 {
		t.Fatalf("allow target %v", h)
	}
	app.Press("id:allow")
	r := awaitRequest(t, app, sent, "respond")
	if r.Response == nil || r.Response.Action != interaction.Allow || r.ApprovalVersion != phoneproto.ApprovalFingerprint(a) {
		t.Fatalf("reply %#v", r)
	}
	if app.Enabled("id:allow") {
		t.Fatal("duplicate approval enabled")
	}
	changePhone(app, m, func() { m.ApprovalAnswered(a.ID, "approval failed") })
	if !app.Enabled("id:allow") || !strings.Contains(phoneTexts(app), "approval failed") {
		t.Fatal(phoneTexts(app))
	}
	app.Press("id:decline")
	r = awaitRequest(t, app, sent, "respond")
	if r.Response.Action != interaction.Decline {
		t.Fatal(r)
	}
	changePhone(app, m, func() { m.ApprovalAnswered(a.ID, "") })
	if app.Exists("id:allow") {
		t.Fatal("resolved card stayed actionable")
	}
}
func TestPhoneQuestionsAndFormValidation(t *testing.T) {
	t.Run("questions", func(t *testing.T) {
		app, _, sent, _ := showPhoneRequest(t, `{"ID":"2","kind":"questions","questions":[{"id":"lang","question":"Language?","options":[{"label":"Go"}]},{"id":"name","question":"Project name?"}]}`)
		if app.Enabled("id:submit") {
			t.Fatal("empty answers accepted")
		}
		app.Press("label:Go")
		app.Type("label:Project name?", "Miya")
		app.Settle()
		app.Press("id:submit")
		r := awaitRequest(t, app, sent, "respond")
		if r.Response.Answers["lang"] != "Go" || r.Response.Answers["name"] != "Miya" {
			t.Fatal(r.Response)
		}
	})
	t.Run("form", func(t *testing.T) {
		app, m, sent, _ := showPhoneRequest(t, `{"ID":"3","kind":"form","requestedSchema":{"type":"object","required":["repo"],"properties":{"repo":{"type":"string","title":"Repository"}}}}`)
		app.Press("id:confirm")
		app.Settle()
		if m.Client.RequestPending(m.State.Approvals[0]) {
			t.Fatal("invalid form sent")
		}
		app.Type("label:Repository", "miya")
		app.Press("id:request-back")
		app.Settle()
		app.Press("id:review-" + string(m.State.Approvals[0].ID))
		app.Settle()
		app.Press("id:confirm")
		r := awaitRequest(t, app, sent, "respond")
		if r.Response.Content["repo"] != "miya" {
			t.Fatal(r.Response)
		}
	})
}
func TestPhoneMemoryScopeRevisionAndSource(t *testing.T) {
	app, m, sent := startPhone(t)
	changePhone(app, m, func() { m.State.Memory = true; m.State.MemoryRevision = 3 })
	app.Press("id:memory")
	r := awaitRequest(t, app, sent, "memory")
	if r.Project != "/work" || r.MemoryScope != "project" {
		t.Fatal(r)
	}
	ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: "codex", SessionID: "session"}.URL()
	changePhone(app, m, func() {
		m.ReceiveMemory(phoneJSON(phoneproto.MemoryResult{Project: "/work", Scope: "project", Revision: 2, List: manager.List{Entries: []memory.Entry{{ID: "stale", Content: "stale memory"}}}}), "")
	})
	if app.Exists("id:memory-entry-stale") {
		t.Fatal("stale revision displayed")
	}
	changePhone(app, m, func() {
		m.ReceiveMemory(phoneJSON(phoneproto.MemoryResult{Project: "/work", Scope: "project", Revision: 3, List: manager.List{Entries: []memory.Entry{{ID: "m1", Content: "Use Go", Kind: "preference", State: "active", Sources: []memory.Source{{Thread: ref, Quote: "Please use Go"}}}}}}), "")
	})
	if !strings.Contains(phoneTexts(app), "Use Go") {
		t.Fatal(phoneTexts(app))
	}
	app.Press("id:memory-user")
	r = awaitRequest(t, app, sent, "memory")
	if r.MemoryScope != "user" {
		t.Fatal(r)
	}
	changePhone(app, m, func() {
		m.ReceiveMemory(phoneJSON(phoneproto.MemoryResult{Project: "/work", Scope: "project", Revision: 3, List: manager.List{Entries: []memory.Entry{{ID: "wrong", Content: "Wrong scope"}}}}), "")
	})
	if app.Exists("id:memory-entry-wrong") {
		t.Fatal("late scope response accepted")
	}
	app.Press("id:memory-project")
	awaitRequest(t, app, sent, "memory")
	changePhone(app, m, func() {
		m.ReceiveMemory(phoneJSON(phoneproto.MemoryResult{Project: "/work", Scope: "project", Revision: 3, List: manager.List{Entries: []memory.Entry{{ID: "m1", Content: "Use Go", Sources: []memory.Source{{Thread: ref}}}}}}), "")
	})
	app.Press("id:memory-source-m1-0")
	app.Settle()
	if m.Open != 7 || m.sheet.Get() != "" {
		t.Fatal("source did not navigate")
	}
}
func TestPhoneAutomationListDetailRunAndCreateRetry(t *testing.T) {
	app, m, sent := startPhone(t)
	changePhone(app, m, func() { m.State.Automations = true; m.State.AutomationRun = true; m.State.AutomationCreate = true })
	app.Press("id:automations")
	awaitRequest(t, app, sent, "automations")
	v := automation.View{Automation: automation.Automation{ID: "a1", Name: "Review issues", Agent: "codex", Project: "/work", Schedule: "0 9 * * *", Instructions: "Review all issues", Enabled: true}, Runs: []automation.Run{{ServerID: "session", Thread: 999, Summary: "Checked", Result: automation.Passed}}}
	changePhone(app, m, func() { m.ReceiveAutomations(phoneJSON([]automation.View{v}), "") })
	app.Press("id:automation-a1")
	app.Settle()
	if !strings.Contains(phoneTexts(app), "Review all issues") || !app.Exists("id:automation-conversation-0") {
		t.Fatal(phoneTexts(app))
	}
	app.Press("id:automation-run")
	run := awaitRequest(t, app, sent, "automation-run")
	if run.AutomationID != "a1" || app.Enabled("id:automation-run") {
		t.Fatal("run not guarded")
	}
	changePhone(app, m, func() {
		m.ReceiveAutomationRun(phoneJSON(phoneproto.AutomationRunResult{RequestID: run.RequestID, List: []automation.View{v}}), "")
	})
	app.Press("id:automation-new")
	app.Settle()
	app.Type("id:automation-request", "Review issues daily")
	before := app.Find("id:automation-request").ID
	app.Press("id:automation-generate")
	draft := awaitRequest(t, app, sent, "automation-draft")
	if draft.Project != "/work" || draft.Agent != "codex" {
		t.Fatal(draft)
	}
	changePhone(app, m, func() {
		m.ReceiveAutomationForm("automation-draft", phoneJSON(phoneproto.AutomationFormResult{RequestID: draft.RequestID, View: &v}), "")
	})
	if app.Find("id:automation-request").ID != before {
		t.Fatal("request editor rebuilt")
	}
	app.Type("id:automation-name", "Nightly review")
	app.Press("id:automation-create")
	create := awaitRequest(t, app, sent, "automation-create")
	changePhone(app, m, func() { m.Autos.Form.RequestedAt = time.Now().Add(-time.Minute); m.TickFeatures() })
	app.Press("id:automation-create")
	retry := awaitRequest(t, app, sent, "automation-create")
	if create.RequestID != retry.RequestID || string(create.Automation) != string(retry.Automation) {
		t.Fatal("uncertain create changed identity")
	}
	changePhone(app, m, func() {
		m.ReceiveAutomationForm("automation-create", phoneJSON(phoneproto.AutomationFormResult{RequestID: create.RequestID, View: &v}), "")
	})
	if m.Autos.Form.CreateKey != "" || m.Autos.Detail != "a1" {
		t.Fatal("creation not reconciled")
	}
}
func TestPhoneDelegationsTagsListDetailAndRetry(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() { m.State.Collaboration = &model.CollaborationStatus{Version: 1, Revision: 1} })
	app.Type("id:mobile-composer", "#cod")
	app.Settle()
	app.Press("id:agent-reference-codex")
	app.Settle()
	if !strings.Contains(m.Draft, "codex") {
		t.Fatalf("agent reference %q", m.Draft)
	}
	app.Press("id:conversation-actions")
	app.Settle()
	app.Press("id:menu-delegations")
	list := awaitRequest(t, app, sent, "collaboration")
	var req appmodel.CollaborationRequest
	_ = json.Unmarshal(list.Collaboration, &req)
	from := req.From
	reply := func(id string, result any, message string) {
		changePhone(app, m, func() { m.ReceiveCollaboration(phoneJSON(map[string]any{"request_id": id, "result": result}), message) })
	}
	row := nsrequests.CollaborationTaskView{ID: "t1", From: from, Title: "Check code", Agent: "codex", State: dg.Running, Revision: 1}
	reply(list.RequestID, nsrequests.CollaborationListView{Tasks: []nsrequests.CollaborationTaskView{row}, Revision: 1}, "")
	app.Press("id:collaboration-task-t1")
	read := awaitRequest(t, app, sent, "collaboration")
	reply(read.RequestID, row, "")
	app.Press("id:collaboration-cancel")
	cancel := awaitRequest(t, app, sent, "collaboration")
	reply(cancel.RequestID, nil, "transport uncertain")
	app.Press("id:collaboration-retry")
	retry := awaitRequest(t, app, sent, "collaboration")
	if string(cancel.Collaboration) != string(retry.Collaboration) {
		t.Fatal("task retry changed mutation identity")
	}
	reply(retry.RequestID, map[string]any{"accepted": true, "task_id": "t1"}, "")
	changePhone(app, m, func() { m.Disconnect() })
	if app.Enabled("id:collaboration-cancel") {
		t.Fatal("disconnected task mutation enabled")
	}
}
