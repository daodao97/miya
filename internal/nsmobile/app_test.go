package nsmobile

import (
	"strings"
	"testing"
	"time"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func startPhone(t *testing.T) (*nsgotest.App, *App, chan remote.Request) {
	t.Helper()
	return startConfiguredPhone(t, core.OSIOS, nil)
}

func startConfiguredPhone(t *testing.T, os core.OS, configure func(*App)) (*nsgotest.App, *App, chan remote.Request) {
	t.Helper()
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	m := NewMobileApp()
	m.Loaded, m.Link, m.Connected = true, "paired-desktop", true
	m.ExpandedProject = "/work"
	m.State = phoneproto.State{Agent: "codex", ReferenceSource: "0123456789abcdef0123456789abcdef", Projects: []model.ProjectSnapshot{{Name: "Work", Path: "/work", Current: true, Threads: []model.Thread{{ID: 7, ServerID: "session", Agent: "codex", Title: "Fix the phone"}}}}}
	sent := make(chan remote.Request, 30)
	m.SendRequest = func(r remote.Request) error { sent <- r; return nil }
	if configure != nil {
		configure(m)
	}
	app := nsgotest.Start(t, m.Render, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: os, Idiom: core.IdiomPhone, Locale: "en", SafeArea: core.Edges{Top: 47, Bottom: 34}}))
	return app, m, sent
}
func awaitRequest(t *testing.T, app *nsgotest.App, sent <-chan remote.Request, op string) remote.Request {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		app.Settle()
		select {
		case r := <-sent:
			if r.Op == op {
				return r
			}
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no %s request; texts %q", op, app.Texts())
	return remote.Request{}
}
func changePhone(app *nsgotest.App, m *App, f func()) { app.Post(m.act(f)); app.Settle() }
func openPhoneThread(t *testing.T, app *nsgotest.App, m *App, sent <-chan remote.Request) {
	t.Helper()
	app.Press("id:thread-7")
	app.Settle()
	r := awaitRequest(t, app, sent, "subscribe")
	if r.Thread != 7 || r.ServerID != "session" || r.Agent != "codex" || r.Project != "/work" {
		t.Fatalf("subscribe identity: %#v", r)
	}
	changePhone(app, m, func() {
		m.ReceiveThread(7, phoneproto.ThreadDelta{Reset: true, Thread: model.Thread{ID: 7, Agent: "codex", ServerID: "session", Title: "Fix the phone", HistoryLoaded: true}, Order: []int{1, 2}, Changed: []model.Message{{ID: 1, Role: "user", Content: "Hello", TurnID: "turn"}, {ID: 2, Role: "assistant", Content: "Ready", Status: "completed", TurnID: "turn"}}})
	})
}
func TestPhoneHomeListsAndFiltersProjects(t *testing.T) {
	app, m, _ := startPhone(t)
	for _, id := range []string{"mobile-home", "project-/work", "thread-7", "new-conversation"} {
		if !app.Exists("id:" + id) {
			t.Fatalf("missing %s: %q", id, app.Texts())
		}
	}
	if h := app.Find("id:thread-7").Frame.Size.Height; h < 44 {
		t.Fatalf("thread touch target = %v", h)
	}
	app.Type("id:home-search", "missing")
	app.Settle()
	if app.Exists("id:thread-7") {
		t.Fatal("search did not filter")
	}
	app.Clear("id:home-search")
	app.Press("id:project-/work")
	app.Settle()
	if m.ExpandedProject != "" || app.Exists("id:thread-7") {
		t.Fatal("project did not collapse")
	}
}
func TestPhoneOpensConversationAndRetainsEditorWhileStreaming(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	if !app.Exists("id:prompt-1") || !strings.Contains(strings.Join(app.Texts(), " "), "Ready") {
		t.Fatalf("conversation missing %q", app.Texts())
	}
	before := app.Find("id:mobile-composer").ID
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		th.Busy = true
		th.Messages[1].Content = "Ready to help"
		m.ShowThread(th)
	})
	if after := app.Find("id:mobile-composer").ID; after != before {
		t.Fatal("streaming rebuilt the native editor")
	}
	app.Press("id:back")
	app.Settle()
	if m.Open != 0 || !app.Exists("id:mobile-home") {
		t.Fatal("back did not return home")
	}
}
func TestPhoneSendAcknowledgementKeepsNextDraft(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	app.Type("id:mobile-composer", "Fix it")
	app.Press("id:send")
	app.Settle()
	r := awaitRequest(t, app, sent, "send")
	if r.Text != "Fix it" || r.ServerID != "session" || r.Steer {
		t.Fatalf("send %#v", r)
	}
	if m.Draft != "" || len(m.Outbox) != 1 || !app.Exists("id:prompt--1") {
		t.Fatalf("pending send missing %q", app.Texts())
	}
	app.Type("id:mobile-composer", "next draft")
	changePhone(app, m, func() { m.Sent(7, "") })
	if m.Draft != "next draft" || m.Sending {
		t.Fatal("acknowledgement consumed the next draft")
	}
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		th.Messages = append(th.Messages, model.Message{ID: 3, Role: "user", Content: "Fix it", TurnID: "next"})
		m.ReceiveThread(7, phoneproto.ThreadDelta{Reset: true, Thread: th, Order: []int{1, 2, 3}, Changed: th.Messages})
	})
	if len(m.Outbox) != 0 || app.Exists("id:prompt--1") || !app.Exists("id:prompt-3") {
		t.Fatal("live echo did not replace pending projection")
	}
}
func TestPhoneNewConversationSend(t *testing.T) {
	app, m, sent := startPhone(t)
	app.Press("id:new-conversation")
	app.Settle()
	app.Type("id:mobile-composer", "Build this")
	app.Press("id:send")
	app.Settle()
	r := awaitRequest(t, app, sent, "send")
	if r.Thread != 0 || r.Project != "/work" || r.Agent != "codex" {
		t.Fatalf("new send %#v", r)
	}
	app.Type("id:mobile-composer", "keep me")
	changePhone(app, m, func() { m.Sent(8, "") })
	if m.Open != 8 || m.Draft != "keep me" || m.Path != "/thread" {
		t.Fatal("new conversation acknowledgement lost ownership")
	}
}
func TestPhoneQueueSteerDeleteDrainAndStop(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		th := m.Viewer.Current()
		th.Busy = true
		th.Queue = []model.QueuedMessage{{ID: 9, Text: "Queued task"}}
		m.ShowThread(th)
	})
	if !app.Exists("id:queued-9") {
		t.Fatalf("queue missing %q", app.Texts())
	}
	app.Press("id:steer-queued-9")
	r := awaitRequest(t, app, sent, "steer-queued")
	if r.QueuedID != 9 || r.ServerID != "session" {
		t.Fatalf("steer %#v", r)
	}
	app.Press("id:dequeue-9")
	r = awaitRequest(t, app, sent, "dequeue")
	if r.QueuedID != 9 {
		t.Fatal("wrong queue ID")
	}
	app.Type("id:mobile-composer", "Steer this")
	app.Press("id:steer")
	r = awaitRequest(t, app, sent, "send")
	if !r.Steer {
		t.Fatal("steer sent as queue")
	}
	changePhone(app, m, func() { m.Sent(7, "") })
	app.Press("id:stop")
	awaitRequest(t, app, sent, "stop")
	changePhone(app, m, func() { th := m.Viewer.Current(); th.Busy = false; th.StopRequested = true; m.ShowThread(th) })
	app.Press("id:drain-queue")
	awaitRequest(t, app, sent, "drain-queue")
}

func TestPhoneBusySendQueuesWithoutSteering(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() { th := m.Viewer.Current(); th.Busy = true; m.ShowThread(th) })
	app.Type("id:mobile-composer", "later")
	app.Press("id:send")
	r := awaitRequest(t, app, sent, "send")
	if r.Steer {
		t.Fatal("ordinary busy send unexpectedly steered")
	}
	changePhone(app, m, func() {
		m.Sent(7, "")
		th := m.Viewer.Current()
		th.Queue = []model.QueuedMessage{{ID: 22, Text: "later"}}
		m.ReceiveThread(7, phoneproto.ThreadDelta{Thread: th, Order: []int{1, 2}})
	})
	if len(m.Outbox) != 0 || !app.Exists("id:queued-22") {
		t.Fatal("queue echo did not retire pending send")
	}
}
func TestPhoneModelEffortAndPolicySheets(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		m.State.Agents = []phoneproto.AgentOptions{{Agent: "codex", Models: []phoneproto.ModelOption{{Value: "phone-model", Label: "Phone model", DefaultEffort: "high", Efforts: []string{"low", "high"}}}}}
	})
	app.Press("id:model-options")
	app.Settle()
	app.Press("id:pick-model")
	app.Settle()
	app.Press("id:model-phone-model")
	app.Settle()
	if o := m.SendOptions("codex"); o.Model != "phone-model" || o.Effort != "high" {
		t.Fatalf("model defaults: %#v", o)
	}
	app.Press("id:pick-effort")
	app.Settle()
	app.Press("id:effort-1")
	app.Settle()
	app.Press("id:pick-policy")
	app.Settle()
	app.Press("id:policy-never")
	app.Settle()
	o := m.SendOptions("codex")
	if o.Effort != "low" || o.Policy != "never" {
		t.Fatalf("message options: %#v", o)
	}
	app.Press("id:options-close")
	app.Settle()
	app.Type("id:mobile-composer", "options")
	app.Press("id:send")
	r := awaitRequest(t, app, sent, "send")
	if r.Options == nil || *r.Options != o {
		t.Fatalf("options not sent: %#v", r.Options)
	}
}
func TestPhoneKeyboardKeepsEditorMountedAndVisible(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	before := app.Find("id:mobile-composer").ID
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Locale: "en", SafeArea: core.Edges{Top: 47, Bottom: 34}, KeyboardHeight: 300})
	app.Settle()
	v := app.Find("id:mobile-composer")
	if v.ID != before {
		t.Fatal("keyboard rebuilt editor")
	}
	y := v.Frame.Origin.Y
	for p := v.Parent; p != nil; p = p.Parent {
		y += p.Frame.Origin.Y - p.Offset.Y
	}
	if y < 47 || y+v.Frame.Size.Height > 544 {
		t.Fatalf("editor outside usable screen: y=%v height=%v", y, v.Frame.Size.Height)
	}
}
func TestPhoneHandoffCarriesDraftAndDurableReference(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	app.Type("id:mobile-composer", "continue this")
	app.Press("id:handoff")
	app.Settle()
	if m.Open != 0 || m.Path != "/thread" || !strings.Contains(m.Draft, "continue this") || len(model.ThreadMentions(m.Draft)) == 0 {
		t.Fatalf("handoff: %q", m.Draft)
	}
	awaitRequest(t, app, sent, "prefetch")
}

func TestPhoneThreadReferenceSuggestionsUseDurableSource(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		m.State.Projects[0].Threads = append(m.State.Projects[0].Threads, model.Thread{ID: 8, ServerID: "other-session", Agent: "codex", Title: "Other task"})
	})
	app.Type("id:mobile-composer", "Use @Other")
	app.Settle()
	app.Press("id:reference-8")
	app.Settle()
	mentions := model.ThreadMentions(m.Draft)
	if len(mentions) != 1 || mentions[0].Ref.SessionID != "other-session" || mentions[0].Ref.Source != m.State.ReferenceSource {
		t.Fatalf("reference draft %q", m.Draft)
	}
	r := awaitRequest(t, app, sent, "prefetch")
	if r.Reference != mentions[0].Ref.URL() {
		t.Fatal("prefetch used a different identity")
	}
}
