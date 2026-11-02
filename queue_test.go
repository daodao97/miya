//go:build !windows

package main

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// testDispatch is a controller's own queue of dispatched work, so that a
// run still ending after its test never touches the shared dispatch state.
type testDispatch struct {
	mu sync.Mutex
	q  []func()
}

func (d *testDispatch) push(f func()) { d.mu.Lock(); d.q = append(d.q, f); d.mu.Unlock() }

func (d *testDispatch) take() []func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	q := d.q
	d.q = nil
	return q
}

// drain runs what is dispatched, and what that dispatches, on the test.
func (d *testDispatch) drain() {
	for q := d.take(); len(q) > 0; q = d.take() {
		for _, f := range q {
			f()
		}
	}
}

func queueController(t *testing.T) (*chatController, *testDispatch) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true command")
	}
	d := &testDispatch{}
	c := testController()
	c.run = d.push
	c.ctx, c.claudeBinary, c.cwd = context.Background(), bin, t.TempDir()
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Busy: true, Resumed: true, HistoryLoaded: true}})
	return c, d
}

func userTexts(th Thread) []string {
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
	c.submit(sendRequest{ThreadID: 1, Text: "first", Attachments: []string{"/tmp/a.txt"}})
	c.submit(sendRequest{ThreadID: 1, Text: "second"})
	c.submit(sendRequest{ThreadID: 1, Text: "third", Attachments: []string{"/tmp/b.txt"}})
	th := c.threadWithID(1)
	if len(th.Queue) != 3 || len(th.Messages) != 0 {
		t.Fatalf("queued %+v", th)
	}
	c.removeQueued(1, th.Queue[1].ID)
	c.update(1, func(t *Thread) { t.Busy = false })
	d.drain()
	th = c.threadWithID(1)
	if got := userTexts(th); len(got) != 1 || got[0] != "first\n\nthird\n📎 a.txt\n📎 b.txt" || !th.Busy || len(th.Queue) != 0 {
		t.Fatalf("after the turn: %q busy %v queue %+v", got, th.Busy, th.Queue)
	}
}

func TestQueuedReferencesKeepEachSendTimeSnapshot(t *testing.T) {
	c, d := queueController(t)
	src := handoffSource()
	c.setThreads(append(c.threads(), src))
	raw := referenceText(testReference(src), src.Title)
	if c.submit(sendRequest{ThreadID: 1, Text: "第一次 " + raw}) != 1 {
		t.Fatal(c.lastSubmitError)
	}
	c.update(src.ID, func(t *Thread) {
		t.Messages = append(t.Messages, Message{Role: "user", Content: "第二次请求"}, Message{Role: "assistant", Content: "新的结论"})
	})
	if c.submit(sendRequest{ThreadID: 1, Text: "第二次 " + raw}) != 1 {
		t.Fatal(c.lastSubmitError)
	}
	c.update(1, func(t *Thread) { t.Busy = false })
	d.drain()
	texts := userTexts(c.threadWithID(1))
	if len(texts) != 1 {
		t.Fatal(texts)
	}
	body, snapshots := splitReferenceContext(texts[0])
	if body != "第一次 "+raw+"\n\n第二次 "+raw || len(snapshots) != 2 {
		t.Fatalf("queue lost references: %q %+v", body, snapshots)
	}
	if strings.Contains(snapshots[0].Context, "新的结论") || !strings.Contains(snapshots[1].Context, "新的结论") {
		t.Fatal("queued references were refreshed instead of preserving snapshots")
	}
}

func TestStoppedTurnPausesTheQueue(t *testing.T) {
	c, d := queueController(t)
	c.submit(sendRequest{ThreadID: 1, Text: "later"})
	c.update(1, func(t *Thread) { t.Busy, t.StopRequested = false, true })
	d.drain()
	if th := c.threadWithID(1); len(th.Queue) != 1 || len(th.Messages) != 0 {
		t.Fatalf("a stopped turn sent the queue: %+v", th)
	}
	c.resumeQueue(1)
	if th := c.threadWithID(1); len(th.Queue) != 0 || len(userTexts(th)) != 1 || !th.Busy {
		t.Fatalf("queue not resumed: %+v", th)
	}
}

func TestComposerQueuesWhileBusy(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.claudeBinary = "/bin/true"
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "s1", Title: "Busy", Busy: true, HistoryLoaded: true, Resumed: true}})
	c.setActive(1)
	tt.Frame()
	if _, ok := tt.Find("Stop response"); !ok {
		t.Fatal("no stop button with an empty composer")
	}
	c.rememberDraft("follow up")
	tt.Frame()
	if err := tt.Click("加入队列"); err != nil {
		t.Fatalf("no queue button: %v", err)
	}
	if c.draft() != "" || len(c.current().Queue) != 1 || !tt.HasText("待发送 1 条 · 当前回合结束后一起发送") || !tt.HasText("follow up") {
		t.Fatalf("not queued: draft %q queue %+v texts %q", c.draft(), c.current().Queue, tt.Texts())
	}
	if err := tt.Click("编辑排队消息"); err != nil {
		t.Fatal(err)
	}
	if c.draft() != "follow up" || len(c.current().Queue) != 0 {
		t.Fatalf("edit did not return the message: %q %+v", c.draft(), c.current().Queue)
	}
	c.send()
	tt.Frame()
	if err := tt.Click("删除排队消息"); err != nil {
		t.Fatal(err)
	}
	if len(c.current().Queue) != 0 || tt.HasText("待发送") {
		t.Fatal("queued message not removed")
	}
}
