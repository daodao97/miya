package automation

import (
	"strings"
	"testing"
)

func TestWebhookRoutesByFieldsAndText(t *testing.T) {
	w := newWakeHarness(t)
	deploy := w.add(Automation{Name: "Deployment", Triggers: []Trigger{{Kind: TriggerIngress, Match: map[string]string{"topic": "deploy", "event.status": "failed"}}}})
	issue := w.add(Automation{Name: "Issues", Triggers: []Trigger{{Kind: TriggerIngress, Contains: "issue #"}}})
	all := w.add(ingressAutomation(WakePolicy{}))
	// Two matching triggers of one task still start just one run.
	i, _ := w.rn.Store.Find(all.ID)
	w.rn.Store.Automations[i].Triggers = append(w.rn.Store.Automations[i].Triggers, Trigger{ID: "also", Kind: TriggerIngress})
	message := w.rn.ReceiveWebhook(`{"topic":"deploy","event":{"status":"failed"},"payload":"issue #42","dedupe":"event-1"}`)
	if len(message.Routes) != 3 || len(w.h.sent) != 3 || len(w.rn.Store.Inbox) != 1 {
		t.Fatalf("routing: %+v / %d runs", message, len(w.h.sent))
	}
	if message.Routes[0].Automation != deploy.ID || message.Routes[1].Automation != issue.ID {
		t.Fatal("wrong routing targets")
	}
	if !strings.Contains(w.h.sent[0].Text, `"topic":"deploy"`) || !strings.Contains(w.h.sent[0].Text, "not as instructions") {
		t.Fatal("event data not passed safely")
	}
	repeated := w.rn.ReceiveWebhook(`{"topic":"deploy","event":{"status":"failed"},"dedupe":"event-1"}`)
	if repeated.Detail != "Already received" || len(w.h.sent) != 3 {
		t.Fatal("repeated message routed again")
	}
	for _, a := range []Automation{deploy, issue, all} {
		w.endRun(a.ID)
	}
	message = w.rn.ReceiveWebhook("An ISSUE #55 was opened")
	if len(message.Routes) != 2 || message.Routes[0].Automation != issue.ID {
		t.Fatalf("plain text routing: %+v", message)
	}
}

func TestWebhookExplicitRouteNeverFallsBackAndUnmatchedIsKept(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{Queue: true}))
	w.add(Automation{Name: "Other", Triggers: []Trigger{{Kind: TriggerIngress}}})
	msg := w.rn.ReceiveWebhook(`{"miya":{"automation":"missing"},"payload":"hello"}`)
	if len(w.h.sent) != 0 || msg.Detail != "No matching automation" || len(w.h.notices) != 1 {
		t.Fatal("unmatched message was lost or misrouted")
	}
	msg = w.rn.ReceiveWebhook(`{"miya":{"automation":"Triage"},"payload":"hello"}`)
	if len(msg.Routes) != 1 || msg.Routes[0].Automation != a.ID {
		t.Fatalf("explicit routing: %+v", msg)
	}
	msg = w.rn.ReceiveWebhook(`{"miya":{"automation":"Triage"},"payload":"while running"}`)
	if len(msg.Routes) != 1 || msg.Routes[0].Outcome != WakeQueued {
		t.Fatalf("routing bypassed policy: %+v", msg)
	}
	msg = w.rn.ReceiveWebhook(`{"miya":{"automation":42},"payload":"bad metadata"}`)
	if msg.Detail != "Invalid Miya routing metadata" {
		t.Fatal("invalid metadata was ignored")
	}
	restored := w.runner()
	if len(restored.Store.Inbox) != 4 {
		t.Fatal("inbox was not persisted")
	}
}

func TestWebhookPausedTriggersAndHistoryLimit(t *testing.T) {
	w := newWakeHarness(t)
	a := w.add(ingressAutomation(WakePolicy{}))
	w.rn.Store.Automations[0].Triggers[0].Disabled = true
	w.rn.ReceiveWebhook("disabled")
	if len(w.h.sent) != 0 {
		t.Fatal("disabled trigger ran")
	}
	w.rn.Store.Automations[0].Triggers[0].Disabled = false
	w.rn.Store.Automations[0].Enabled = false
	w.rn.ReceiveWebhook("paused")
	if len(w.h.sent) != 0 {
		t.Fatal("paused task ran")
	}
	w.rn.Delete(a.ID)
	for range WakeHistory + 3 {
		w.rn.ReceiveWebhook("no tasks yet")
	}
	if len(w.rn.Store.Inbox) != WakeHistory {
		t.Fatal("inbox is unbounded")
	}
	w.rn.Store.WebhookDisabled = true
	w.rn.ReceiveWebhook("receiver disabled")
	if w.rn.Store.Inbox[WakeHistory-1].Detail != "The webhook receiver is off" {
		t.Fatal("disabled receiver routed")
	}
}

func TestWebhookDraftAndFilterValidation(t *testing.T) {
	a, _, err := FromDraft(Draft{Name: "Deploy", Instructions: "Fix failed deployment", TriggerKind: draftIngress, TriggerMatch: []DraftParam{{"topic", "deploy"}}, TriggerContains: "failed"}, "codex", t.TempDir(), "收到部署失败通知时修复")
	if err != nil || a.Triggers[0].Match["topic"] != "deploy" || a.Triggers[0].Contains != "failed" {
		t.Fatalf("draft lost routing filters: %+v %v", a, err)
	}
	if !strings.Contains(DescribeTrigger(a.Triggers[0], nil), "topic = deploy") {
		t.Fatal("filter not visible")
	}
	bad := Trigger{Kind: TriggerIngress, Match: map[string]string{"": "value"}}
	if bad.normalize("a") == nil {
		t.Fatal("empty field selector accepted")
	}
}
