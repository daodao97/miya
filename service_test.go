//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/codex"
)

// fakeClaude speaks enough stream-json for one turn: a first delta, a
// pause, the rest and the result.
const fakeClaude = `#!/bin/sh
read -r line
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read -r line
printf '%s\n' '{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial "}}}'
sleep 1
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"partial done"}'
`

// serverController is a controller with every signal the service reads.
func serverController(dir, claudeBin string) *chatController {
	c := &chatController{referenceSource: testReferenceSource, cwd: dir, ctx: context.Background(), claudeBinary: claudeBin, projectCache: map[string][]Thread{}}
	c.agent, c.setAgent = createSignal(agentCodex)
	c.draftPermission, c.setDraftPermission = createSignal("")
	c.projects, c.setProjects = createSignal([]Project{{Name: "demo", Path: dir}})
	c.threads, c.setThreads = createSignal([]Thread{})
	c.active, c.setActive = createSignal(0)
	c.draft, c.setDraft = createSignal("")
	c.model, c.setModel = createSignal("")
	c.effort, c.setEffort = createSignal("")
	c.permissionPolicy, c.setPermissionPolicy = createSignal("on-request")
	c.locale, c.setLocale = createSignal("en")
	c.models, c.setModels = createSignal([]codex.Model{})
	c.attachments, c.setAttachments = createSignal([]string{})
	c.status, c.setStatus = createSignal("")
	c.ready, c.setReady = createSignal(false)
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	c.historyStatus, c.setHistoryStatus = createSignal("")
	c.revision, c.setRevision = createSignal(0)
	return c
}

func TestCompletionNoticeSurvivesServiceSnapshots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := serverController(t.TempDir(), "")
	h := testUnreadHub(t)
	c.push = h
	c.installNotifications()
	c.setThreads([]Thread{{ID: 1, ServerID: "thread", Busy: true, TurnID: "turn", Messages: []Message{
		{Role: "user", Content: "修复按钮"},
		{Role: "assistant", TurnID: "turn", Content: "按钮已修复。测试通过。", Item: &codex.Item{Phase: "final_answer"}},
	}}})
	c.update(1, func(t *Thread) {
		t.Busy, t.CompletedAtMs, t.TurnID = false, 10, ""
	})
	delivery := <-h.queue
	push, ok := h.notificationFor(delivery)
	if !ok || push.Payload.Body != "按钮已修复。" {
		t.Fatal("phone did not receive the result excerpt", push)
	}
	state := c.snapshotState()
	if len(state.Threads[0].Messages) != 0 {
		t.Fatal("snapshot included transcript")
	}
	// A desktop that never subscribed to this thread sees metadata only.
	n, ok := completionNotice(Thread{Busy: true}, state.Threads[0])
	if !ok || n.Body != push.Payload.Body {
		t.Fatal("desktop and phone notification bodies differ", n)
	}
	c.projectCache["/background"] = c.threads()
	state = c.snapshotState()
	if state.ProjectThreads["/background"][0].CompletionNoticeBody != n.Body {
		t.Fatal("background project's snapshot lost the result")
	}
}

type serviceFixture struct {
	t    *testing.T
	sock string
	srv  *service
	serv *executor // the service's main thread
	win  *executor // the windows' main thread
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("", "miya")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	f := &serviceFixture{t: t, sock: filepath.Join(sockDir, "s.sock"), serv: newExecutor(), win: newExecutor()}
	stop := make(chan struct{})
	go f.serv.loop(stop)
	go f.win.loop(stop)
	oldHook := currentInvalidateHook()
	f.serv.do(func() {
		f.srv = newService(serverController(dir, bin), f.serv)
	})
	setInvalidateHook(f.srv.markDirty)
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	go f.srv.serve(ln)
	go f.srv.publishLoop()
	t.Cleanup(func() {
		f.srv.stop()
		ln.Close()
		// Let the Claude run report its end before the state goes away.
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			busy := true
			f.serv.do(func() { busy = f.srv.busy() })
			if !busy {
				break
			}
		}
		close(stop)
		setInvalidateHook(oldHook)
	})
	return f
}

// window connects a window's controller, as newWindowController does.
func (f *serviceFixture) window() *chatController {
	f.t.Helper()
	cl, err := dialService(f.sock)
	if err != nil {
		f.t.Fatal(err)
	}
	var st svcState
	if err := cl.call("state", nil, &st); err != nil {
		f.t.Fatal(err)
	}
	var c *chatController
	f.win.do(func() {
		c = newMirrorController()
		c.run = f.win.push
		c.attachService(cl, st)
	})
	return c
}

func TestServiceWindowWaitsInsteadOfStartingLocalAgents(t *testing.T) {
	var attempts atomic.Int32
	retrying, allow := make(chan struct{}), make(chan struct{})
	conn, peer := net.Pipe()
	cl := newServiceClient(conn)
	go io.Copy(io.Discard, peer)
	c := newServiceWindow("/workspace", func() (*serviceClient, svcState, error) {
		if attempts.Add(1) == 1 {
			return nil, svcState{}, errors.New("后台服务版本不兼容，且有对话正在运行")
		}
		close(retrying)
		<-allow
		return cl, svcState{Cwd: "/workspace", Ready: true, ProjectThreads: map[string][]Thread{}}, nil
	})
	t.Cleanup(func() { c.cancel(); cl.Close(); peer.Close() })
	if c.remote == nil || c.client != nil || len(c.claudeRuns) != 0 || c.ready() {
		t.Fatal("disconnected window started local agents")
	}
	if got := c.submit(sendRequest{Text: "不要启动本地会话"}); got != 0 || len(c.threads()) != 0 || c.lastSubmitError == "" {
		t.Fatal("offline window accepted a local send")
	}
	select {
	case <-retrying:
	case <-time.After(2 * time.Second):
		t.Fatal("window did not retry connection")
	}
	d := &testDispatch{}
	c.run = d.push
	close(allow)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		if c.ready() {
			if c.remote != cl || c.client != nil || attempts.Load() != 2 {
				t.Fatal("window did not attach to the shared service")
			}
			return
		}
	}
	t.Fatal("window did not connect after the service became available")
}

// waitWindow polls a window's state on its main thread.
func (f *serviceFixture) waitWindow(what string, ok func() bool) {
	f.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		done := false
		f.win.do(func() { done = ok() })
		if done {
			return
		}
	}
	f.t.Fatalf("timed out waiting for %s", what)
}

func assistantText(t Thread) string {
	for _, m := range t.Messages {
		if m.Role == "assistant" {
			return m.Content
		}
	}
	return ""
}

func TestServiceKeepsTurnsRunningAfterTheWindowQuits(t *testing.T) {
	f := newServiceFixture(t)
	a := f.window()
	f.waitWindow("Claude to be ready in the window", func() bool { return a.backendReady() || a.claudeBinary != "" })
	f.win.do(func() {
		a.setAgent(agentClaude)
		a.rememberDraft("hello")
		a.send()
	})
	var id int
	f.waitWindow("the reply to start streaming", func() bool {
		id = a.active()
		cur := a.current()
		return id != 0 && cur.Busy && strings.HasPrefix(assistantText(cur), "partial")
	})
	f.win.do(func() {
		if a.draft() != "" || a.current().Messages[0].Content != "hello" {
			t.Errorf("window after send: draft %q messages %+v", a.draft(), a.current().Messages)
		}
	})

	// The window quits while the reply streams.
	a.remote.Close()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var th Thread
		f.serv.do(func() { th = f.srv.c.threadWithID(id) })
		if !th.Busy && assistantText(th) == "partial done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn did not finish without a window: %+v", th)
		}
	}

	// A new window finds the finished reply.
	b := f.window()
	f.waitWindow("the conversation in the new window", func() bool { return b.threadWithID(id).ID != 0 })
	f.win.do(func() { b.selectThread(b.threadWithID(id)) })
	f.waitWindow("the whole reply in the new window", func() bool {
		cur := b.current()
		return !cur.Busy && assistantText(cur) == "partial done" && len(cur.Messages) == 2
	})
}

func TestServiceRetriesFailedResponseInSameSession(t *testing.T) {
	f := newServiceFixture(t)
	// This headless fixture has no native event loop for notification APIs.
	persistSetting("notifications-completion", "off")
	a := f.window()
	f.waitWindow("Claude to be ready", func() bool { return a.claudeBinary != "" })
	f.serv.do(func() {
		th := failedResponse()
		th.Agent = agentClaude
		f.srv.c.setThreads([]Thread{th})
	})
	f.waitWindow("the failed conversation", func() bool { return a.threadWithID(1).ID == 1 })
	f.win.do(func() { a.selectThread(a.threadWithID(1)) })
	f.waitWindow("the failed response", func() bool { return len(a.threadWithID(1).Messages) == 3 })
	f.win.do(func() {
		a.setDraft("keep this draft")
		if err := a.retryResponse(retryRequest{ThreadID: 1, MessageID: 12}); err != nil {
			t.Error(err)
		}
	})
	f.waitWindow("the continued response", func() bool {
		th := a.threadWithID(1)
		return !th.Busy && assistantText(th) == "partial done"
	})
	f.win.do(func() {
		th := a.threadWithID(1)
		if th.ServerID != "s1" || th.Messages[1].Content != "构建完成" || a.draft() != "keep this draft" {
			t.Fatalf("retry lost the session, completed work or draft: %+v", th)
		}
		count := len(th.Messages)
		if err := a.retryResponse(retryRequest{ThreadID: 1, MessageID: 12}); err == nil {
			t.Error("a stale retry from another window was accepted")
		}
		if len(a.threadWithID(1).Messages) != count {
			t.Error("a stale retry submitted another message")
		}
	})
}

func TestServiceDraftSurvivesStaleSnapshotsAndQuickSwitches(t *testing.T) {
	f := newServiceFixture(t)
	persistSetting("notifications-completion", "off")
	f.serv.do(func() {
		f.srv.c.setThreads([]Thread{
			{ID: 1, ServerID: "s1", Agent: agentClaude, Busy: true, HistoryLoaded: true},
			{ID: 2, ServerID: "s2", Agent: agentClaude, HistoryLoaded: true},
		})
	})
	t.Cleanup(func() {
		f.serv.do(func() {
			f.srv.c.update(1, func(th *Thread) { th.Busy, th.Queue = false, nil })
		})
	})
	a := f.window()
	var stale svcState
	f.serv.do(func() { stale = f.srv.c.snapshotState() })
	stale.Threads[0].Draft = "已经发送的消息"
	f.win.do(func() {
		a.selectThread(a.threadWithID(1))
		a.rememberDraft("已经发送的消息")
		a.send()
		var confirmed svcState
		if err := a.remote.call("state", nil, &confirmed); err != nil {
			t.Error(err)
		} else if confirmed.Threads[0].Draft != "" {
			t.Errorf("the service kept a sent draft: %q", confirmed.Threads[0].Draft)
		}
		a.applyState(stale)
		a.selectThread(a.threadWithID(2))
		a.selectThread(a.threadWithID(1))
		if a.draft() != "" {
			t.Errorf("a stale snapshot resurrected a sent draft: %q", a.draft())
		}
		a.rememberDraft("对话一尚未发送")
		a.selectThread(a.threadWithID(2))
		a.rememberDraft("对话二尚未发送")
		a.applyState(stale)
		a.selectThread(a.threadWithID(1))
		if a.draft() != "对话一尚未发送" {
			t.Errorf("lost the latest draft of conversation one: %q", a.draft())
		}
		a.selectThread(a.threadWithID(2))
		if a.draft() != "对话二尚未发送" {
			t.Errorf("lost the latest draft of conversation two: %q", a.draft())
		}
	})
	var confirmed svcState
	if err := a.remote.call("state", nil, &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Threads[0].Draft != "对话一尚未发送" || confirmed.Threads[1].Draft != "对话二尚未发送" {
		t.Errorf("latest drafts were not persisted at the service: %+v", confirmed.Threads)
	}
}

func TestServicePublishesOnlyChangedMessages(t *testing.T) {
	p := &svcPeer{sub: 7}
	th := Thread{ID: 7, Messages: []Message{{ID: 1, Role: "user", Content: "hi"}, {ID: 2, Role: "assistant", Content: "a"}}}
	first, ok := p.diff(th)
	if !ok || !first.Reset || len(first.Changed) != 2 {
		t.Fatalf("first update %+v", first)
	}
	th.Messages = append([]Message(nil), th.Messages...)
	th.Messages[1].Content = "ab"
	second, ok := p.diff(th)
	if !ok || second.Reset || len(second.Changed) != 1 || second.Changed[0].Content != "ab" || len(second.Order) != 2 {
		t.Fatalf("second update %+v", second)
	}
	if _, ok := p.diff(th); ok {
		t.Fatal("an unchanged conversation was sent again")
	}
	th.Messages = th.Messages[:1]
	third, ok := p.diff(th)
	if !ok || len(third.Order) != 1 || len(third.Changed) != 0 {
		t.Fatalf("a removed message was not sent: %+v", third)
	}
}

func TestServiceReturnsUnsentDraftsToTheWindow(t *testing.T) {
	f := newServiceFixture(t)
	w := f.window()
	var id int
	f.serv.do(func() {
		c := f.srv.c
		id = c.nextID()
		c.setThreads([]Thread{{ID: id, Title: "Lost", Agent: agentClaude, ServerID: "s1", HistoryLoaded: true}})
	})
	f.waitWindow("the conversation", func() bool { return w.threadWithID(id).ID != 0 })
	f.win.do(func() { w.selectThread(w.threadWithID(id)) })
	f.serv.do(func() { f.srv.c.returnDraft(id, "try again", []string{"/tmp/a.png"}) })
	f.waitWindow("the returned draft", func() bool { return w.draft() == "try again" && len(w.attachments()) == 1 })
}
