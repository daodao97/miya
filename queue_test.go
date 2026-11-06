//go:build !windows

package main

import (
	context "context"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	model "miya/internal/model"
	exec "os/exec"
	strings "strings"
	testing "testing"
)

func queueController(t *testing.T) (*controller.Controller, *testDispatch) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true command")
	}
	d := &testDispatch{}
	c := testController()
	c.Run = d.push
	c.Ctx, c.ClaudeBinary, c.Cwd = context.Background(), bin, t.TempDir()
	c.SetThreads([]appmodel.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Busy: true, Resumed: true, HistoryLoaded: true}})
	return c, d
}

func userTexts(th appmodel.Thread) []string {
	var out []string
	for _, m := range th.Messages {
		if m.Role == "user" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestQueuedMessagesSendTogetherAfterTheTurn(t *testing.T) {
	c, d := queueController(t)
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "first", Attachments: []string{"/tmp/a.txt"}})
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "second"})
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "third", Attachments: []string{"/tmp/b.txt"}})
	th := c.ThreadWithID(1)
	if len(th.Queue) != 3 || len(th.Messages) != 0 {
		t.Fatalf("queued %+v", th)
	}
	c.RemoveQueued(1, th.Queue[1].ID)
	c.Update(1, func(t *appmodel.Thread) { t.Busy = false })
	d.drain()
	th = c.ThreadWithID(1)
	if got := userTexts(th); len(got) != 1 || got[0] != "first\n\nthird\n📎 a.txt\n📎 b.txt" || !th.Busy || len(th.Queue) != 0 {
		t.Fatalf("after the turn: %q busy %v queue %+v", got, th.Busy, th.Queue)
	}
}

func TestQueuedReferencesKeepEachSendTimeSnapshot(t *testing.T) {
	c, d := queueController(t)
	src := handoffSource()
	c.SetThreads(append(c.Threads(), src))
	raw := model.ReferenceText(testReference(src), src.Title)
	if c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "第一次 " + raw}) != 1 {
		t.Fatal(c.LastSubmitError)
	}
	c.Update(src.ID, func(t *appmodel.Thread) {
		t.Messages = append(t.Messages, appmodel.Message{Role: "user", Content: "第二次请求"}, appmodel.Message{Role: "assistant", Content: "新的结论"})
	})
	if c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "第二次 " + raw}) != 1 {
		t.Fatal(c.LastSubmitError)
	}
	c.Update(1, func(t *appmodel.Thread) { t.Busy = false })
	d.drain()
	texts := userTexts(c.ThreadWithID(1))
	if len(texts) != 1 {
		t.Fatal(texts)
	}
	body, snapshots := model.SplitReferenceContext(texts[0])
	if body != "第一次 "+raw+"\n\n第二次 "+raw || len(snapshots) != 2 {
		t.Fatalf("queue lost references: %q %+v", body, snapshots)
	}
	if strings.Contains(snapshots[0].Context, "新的结论") || !strings.Contains(snapshots[1].Context, "新的结论") {
		t.Fatal("queued references were refreshed instead of preserving snapshots")
	}
}

func TestStoppedTurnPausesTheQueue(t *testing.T) {
	c, d := queueController(t)
	c.Submit(appmodel.SendRequest{ThreadID: 1, Text: "later"})
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.StopRequested = false, true })
	d.drain()
	if th := c.ThreadWithID(1); len(th.Queue) != 1 || len(th.Messages) != 0 {
		t.Fatalf("a stopped turn sent the queue: %+v", th)
	}
	c.ResumeQueue(1)
	if th := c.ThreadWithID(1); len(th.Queue) != 0 || len(userTexts(th)) != 1 || !th.Busy {
		t.Fatalf("queue not resumed: %+v", th)
	}
}

func TestComposerQueuesWhileBusy(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.ClaudeBinary = "/bin/true"
	c.SetThreads([]appmodel.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Title: "Busy", Busy: true, HistoryLoaded: true, Resumed: true}})
	c.SetActive(1)
	tt.Frame()
	if _, ok := tt.Find("Stop response"); !ok {
		t.Fatal("no stop button with an empty composer")
	}
	c.RememberDraft("follow up")
	tt.Frame()
	if err := tt.Click("Add to queue"); err != nil {
		t.Fatalf("no queue button: %v", err)
	}
	if c.Draft() != "" || len(c.Current().Queue) != 1 || !tt.HasText("1 queued · Sent together when the current turn ends") || !tt.HasText("follow up") {
		t.Fatalf("not queued: draft %q queue %+v texts %q", c.Draft(), c.Current().Queue, tt.Texts())
	}
	if err := tt.Click("Edit queued message"); err != nil {
		t.Fatal(err)
	}
	if c.Draft() != "follow up" || len(c.Current().Queue) != 0 {
		t.Fatalf("edit did not return the message: %q %+v", c.Draft(), c.Current().Queue)
	}
	c.Send()
	tt.Frame()
	if err := tt.Click("Delete queued message"); err != nil {
		t.Fatal(err)
	}
	if len(c.Current().Queue) != 0 || tt.HasText("待发送") {
		t.Fatal("queued message not removed")
	}
}
