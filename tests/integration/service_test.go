//go:build !windows

package integration

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/codex"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/notification"
	"miya/internal/prefs"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
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

func TestServiceRestartWhenIdleChecksAllWork(t *testing.T) {
	for _, work := range []string{"idle", "current-project", "background-project", "memory"} {
		t.Run(work, func(t *testing.T) {
			c := serverController(t.TempDir(), "")
			s := &controller.Service{C: c, Quit: make(chan struct{})}
			switch work {
			case "current-project":
				c.SetThreads([]model.Thread{{ID: 1, Busy: true}})
			case "background-project":
				c.ProjectCache["/other"] = []model.Thread{{ID: 2, Busy: true}}
			case "memory":
				c.Memories().Calls = 1
			}
			if _, err := s.Handle(nil, svcproto.Request{Op: "quitWhenIdle"}); err != nil {
				t.Fatal(err)
			}
			stopped := func() bool {
				select {
				case <-s.Quit:
					return true
				default:
					return false
				}
			}
			if !s.QuitWhenIdle || stopped() != (work == "idle") {
				t.Fatal("safe restart did not check all running work before stopping")
			}
			c.SetThreads(nil)
			c.ProjectCache = nil
			c.Memories().Calls = 0
			if _, err := s.Handle(nil, svcproto.Request{Op: "quitWhenIdle"}); err != nil || !stopped() {
				t.Fatal("idle service did not stop promptly")
			}
		})
	}
}

func TestServiceDeleteSelectedConversationUpdatesWindow(t *testing.T) {
	for _, last := range []bool{false, true} {
		name := "select-next"
		if last {
			name = "last-conversation"
		}
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t)
			target := model.Thread{ID: 1, ServerID: "delete-this", Title: "Delete", HistoryLoaded: true}
			keep := model.Thread{ID: 2, ServerID: "keep-this", Title: "Keep", HistoryLoaded: true, Draft: "keep draft"}
			f.serv.Do(func() {
				items := []model.Thread{target}
				if !last {
					items = append(items, keep)
				}
				f.srv.C.SetThreads(items)
				f.srv.C.ProjectCache[f.srv.C.Cwd] = items
			})
			window := f.window()
			t.Cleanup(func() { window.Cancel(); window.Remote.Close() })
			f.win.Do(func() {
				window.SetActive(target.ID)
				window.RememberDraft("old draft")
				window.SetAttachments([]string{"old-attachment.txt"})
				window.Mirror.Messages[target.ID] = []model.Message{{Role: "assistant", Content: "old response"}}
				window.DeleteThread(target)
			})
			f.waitWindow("deleted conversation to disappear", func() bool { return window.ThreadWithID(target.ID).ID == 0 })
			f.win.Do(func() {
				want := keep.ID
				if last {
					want = 0
				}
				if window.Active() != want {
					t.Errorf("selected conversation = %d, want %d", window.Active(), want)
				}
				draft := keep.Draft
				if last {
					draft = ""
				}
				if window.Draft() != draft || len(window.Attachments()) != 0 {
					t.Error("deleted conversation left its composer contents in the new selection")
				}
				if _, ok := window.Mirror.Drafts[target.ID]; ok {
					t.Error("deleted conversation retained its draft")
				}
				if _, ok := window.Mirror.Messages[target.ID]; ok {
					t.Error("deleted conversation retained its messages")
				}
			})
		})
	}
}

func TestCompletionNoticeSurvivesServiceSnapshots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := serverController(t.TempDir(), "")
	h := notification.NewQueueHub(t.TempDir(), notification.Registration{ID: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), Updated: time.Now()})
	c.Push = h
	c.InstallNotifications()
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "thread", Busy: true, TurnID: "turn", Messages: []model.Message{
		{Role: "user", Content: "修复按钮"},
		{Role: "assistant", TurnID: "turn", Content: "按钮已修复。测试通过。", Item: &codex.Item{Phase: "final_answer"}},
	}}})
	c.Update(1, func(t *model.Thread) {
		t.Busy, t.CompletedAtMs, t.TurnID = false, 10, ""
	})
	push, ok := h.NextDelivery()
	if !ok || push.Payload.Body != "按钮已修复。" {
		t.Fatal("phone did not receive the result excerpt", push)
	}
	state := c.SnapshotState()
	if len(state.Threads[0].Messages) != 0 {
		t.Fatal("snapshot included transcript")
	}
	// A desktop that never subscribed to this thread sees metadata only.
	n, ok := notification.Completion(model.Thread{Busy: true}, state.Threads[0])
	if !ok || n.Body != push.Payload.Body {
		t.Fatal("desktop and phone notification bodies differ", n)
	}
	c.ProjectCache["/background"] = c.Threads()
	state = c.SnapshotState()
	if state.ProjectThreads["/background"][0].CompletionNoticeBody != n.Body {
		t.Fatal("background project's snapshot lost the result")
	}
}

type serviceFixture struct {
	t    *testing.T
	sock string
	srv  *controller.Service
	serv *async.Executor // the service's main thread
	win  *async.Executor // the windows' main thread
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
	f := &serviceFixture{t: t, sock: filepath.Join(sockDir, "s.sock"), serv: async.NewExecutor(), win: async.NewExecutor()}
	stop := make(chan struct{})
	go f.serv.Loop(stop)
	go f.win.Loop(stop)
	oldHook := uiruntime.CurrentInvalidateHook()
	f.serv.Do(func() {
		f.srv = controller.NewService(serverController(dir, bin), f.serv)
	})
	uiruntime.SetInvalidateHook(f.srv.MarkDirty)
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	go f.srv.Serve(ln)
	go f.srv.PublishLoop()
	t.Cleanup(func() {
		f.srv.Stop()
		ln.Close()
		// Let the Claude run report its end before the state goes away.
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			busy := true
			f.serv.Do(func() { busy = f.srv.Busy() })
			if !busy {
				break
			}
		}
		// Cancellation stops new collaboration work, but its writer may still
		// be finishing a native outcome/index operation. Wait before TempDir
		// cleanup removes the isolated service data and before its executor
		// disappears; busy() intentionally ignores a cancelled worker.
		if collaboration := f.srv.C.Collaboration; collaboration != nil {
			<-collaboration.Done()
		}
		close(stop)
		uiruntime.SetInvalidateHook(oldHook)
	})
	return f
}

// window connects a window's controller, as newWindowController does.
func (f *serviceFixture) window() *controller.Controller {
	f.t.Helper()
	cl, err := svcproto.Dial(f.sock)
	if err != nil {
		f.t.Fatal(err)
	}
	var st svcproto.State
	if err := cl.Call("state", nil, &st); err != nil {
		f.t.Fatal(err)
	}
	var c *controller.Controller
	f.win.Do(func() {
		c = controller.NewMirrorController()
		c.Run = f.win.Push
		c.AttachService(cl, st)
	})
	return c
}

func TestServiceWindowWaitsInsteadOfStartingLocalAgents(t *testing.T) {
	var attempts atomic.Int32
	retrying, allow := make(chan struct{}), make(chan struct{})
	conn, peer := net.Pipe()
	cl := svcproto.NewClient(conn)
	go io.Copy(io.Discard, peer)
	c := controller.NewServiceWindow("/workspace", func() (*svcproto.Client, svcproto.State, error) {
		if attempts.Add(1) == 1 {
			return nil, svcproto.State{}, errors.New("Incompatible background service version; conversations are running")
		}
		close(retrying)
		<-allow
		return cl, svcproto.State{Cwd: "/workspace", Ready: true, ProjectThreads: map[string][]model.Thread{}}, nil
	})
	t.Cleanup(func() { c.Cancel(); cl.Close(); peer.Close() })
	if c.Remote == nil || c.Client != nil || len(c.ClaudeRuns) != 0 || c.Ready() {
		t.Fatal("disconnected window started local agents")
	}
	if got := c.Submit(model.SendRequest{Text: "不要启动本地会话"}); got != 0 || len(c.Threads()) != 0 || c.LastSubmitError == "" {
		t.Fatal("offline window accepted a local send")
	}
	select {
	case <-retrying:
	case <-time.After(2 * time.Second):
		t.Fatal("window did not retry connection")
	}
	d := &testDispatch{}
	c.Run = d.push
	close(allow)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		d.drain()
		if c.Ready() {
			if c.Remote != cl || c.Client != nil || attempts.Load() != 2 {
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
		f.win.Do(func() { done = ok() })
		if done {
			return
		}
	}
	f.t.Fatalf("timed out waiting for %s", what)
}

func assistantText(t model.Thread) string {
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
	f.waitWindow("Claude to be ready in the window", func() bool { return a.BackendReady() || a.ClaudeBinary != "" })
	f.win.Do(func() {
		a.SetAgent(appmodel.AgentClaude)
		a.RememberDraft("hello")
		a.Send()
	})
	var id int
	f.waitWindow("the reply to start streaming", func() bool {
		id = a.Active()
		cur := a.Current()
		return id != 0 && cur.Busy && strings.HasPrefix(assistantText(cur), "partial")
	})
	f.win.Do(func() {
		if a.Draft() != "" || a.Current().Messages[0].Content != "hello" {
			t.Errorf("window after send: draft %q messages %+v", a.Draft(), a.Current().Messages)
		}
	})

	// The window quits while the reply streams.
	a.Remote.Close()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var th model.Thread
		f.serv.Do(func() { th = f.srv.C.ThreadWithID(id) })
		if !th.Busy && assistantText(th) == "partial done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn did not finish without a window: %+v", th)
		}
	}

	// A new window finds the finished reply.
	b := f.window()
	f.waitWindow("the conversation in the new window", func() bool { return b.ThreadWithID(id).ID != 0 })
	f.win.Do(func() { b.SelectThread(b.ThreadWithID(id)) })
	f.waitWindow("the whole reply in the new window", func() bool {
		cur := b.Current()
		return !cur.Busy && assistantText(cur) == "partial done" && len(cur.Messages) == 2
	})
}

func TestServiceRetryContinuesFailedResponseAfterOutput(t *testing.T) {
	f := newServiceFixture(t)
	// This headless fixture has no native event loop for notification APIs.
	prefs.Save("notifications-completion", "off")
	a := f.window()
	f.waitWindow("Claude to be ready", func() bool { return a.ClaudeBinary != "" })
	f.serv.Do(func() {
		th := failedResponse()
		th.Agent = appmodel.AgentClaude
		f.srv.C.SetThreads([]model.Thread{th})
	})
	f.waitWindow("the failed conversation", func() bool { return a.ThreadWithID(1).ID == 1 })
	f.win.Do(func() { a.SelectThread(a.ThreadWithID(1)) })
	f.waitWindow("the failed response", func() bool { return len(a.ThreadWithID(1).Messages) == 3 })
	f.win.Do(func() {
		a.SetDraft("keep this draft")
		if err := a.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err != nil {
			t.Error(err)
		}
	})
	f.waitWindow("the retried response", func() bool {
		th := a.ThreadWithID(1)
		return !th.Busy && assistantText(th) == "partial done"
	})
	f.win.Do(func() {
		th := a.ThreadWithID(1)
		if th.ServerID != "s1" || len(th.Messages) != 5 || th.Messages[0].ID != 10 || th.Messages[1].ID != 11 || th.Messages[2].ID != 12 || th.Messages[3].Role != "user" || !strings.Contains(th.Messages[3].Content, msgfmt.RetryContinuePrompt) || a.Draft() != "keep this draft" {
			t.Errorf("retry did not continue the failed turn or preserve its output and draft: %+v", th)
		}
		count := len(th.Messages)
		if err := a.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err == nil {
			t.Error("a stale retry from another window was accepted")
		}
		if len(a.ThreadWithID(1).Messages) != count {
			t.Error("a stale retry submitted another message")
		}
	})
}

func TestServiceRetryRewindsOnlyTheLastUserMessage(t *testing.T) {
	f := newServiceFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	prefs.Save("notifications-completion", "off")
	source := handoffSource()
	prompt, _, err := controller.ExpandThreadRefs(model.ReferenceText(testReference(source), source.Title)+" 再审核一下", model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) {
		return source, source.Messages, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(image, []byte("test-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{image, filepath.Join(t.TempDir(), "notes.txt")}
	shown, images := msgfmt.AttachmentDisplay(prompt, paths)
	f.serv.Do(func() {
		c := f.srv.C
		writeClaudeSession(t, c.Cwd, "s1", sessionLines(c.Cwd)...)
		c.Serial = 100
		c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: []model.Message{
			{ID: 10, Role: "user", Content: "first"},
			{ID: 11, Role: "assistant", Content: "reply one"},
			{ID: 12, Role: "user", Content: shown, Images: images, Attachments: paths},
			// The failed turn produced nothing, so retry replays the request.
			{ID: 14, Role: "error", Content: "Connection dropped"},
		}}})
	})
	a := f.window()
	f.waitWindow("the failed conversation", func() bool { return a.ThreadWithID(1).ID == 1 })
	f.win.Do(func() { a.SelectThread(a.ThreadWithID(1)) })
	f.waitWindow("the failed turn", func() bool { return len(a.ThreadWithID(1).Messages) == 4 })
	f.win.Do(func() {
		a.RememberDraft("keep my draft")
		a.SetAttachments([]string{"/tmp/unsent.txt"})
		if err := a.RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 14}); err != nil {
			t.Error(err)
		}
	})
	f.waitWindow("the replacement response", func() bool {
		th := a.ThreadWithID(1)
		return !th.Busy && len(th.Messages) == 4 && th.Messages[3].Content == "partial done"
	})
	f.win.Do(func() {
		th := a.ThreadWithID(1)
		if th.ServerID == "s1" || th.Messages[0].Content != "first" || th.Messages[1].Content != "reply one" || th.Messages[2].Content != shown {
			t.Fatal("retry did not preserve earlier turns and replay the last original request")
		}
		m := th.Messages[2]
		if len(m.Images) != 1 || len(m.Attachments) != 2 || m.Attachments[0] != paths[0] || m.Attachments[1] != paths[1] {
			t.Fatal("retry lost the original attachments")
		}
		if a.Draft() != "keep my draft" || len(a.Attachments()) != 1 || a.Attachments()[0] != "/tmp/unsent.txt" {
			t.Fatal("retry changed the unsent draft or attachments")
		}
	})
}

func TestServiceDraftSurvivesStaleSnapshotsAndQuickSwitches(t *testing.T) {
	f := newServiceFixture(t)
	prefs.Save("notifications-completion", "off")
	f.serv.Do(func() {
		f.srv.C.SetThreads([]model.Thread{
			{ID: 1, ServerID: "s1", Agent: appmodel.AgentClaude, Busy: true, HistoryLoaded: true},
			{ID: 2, ServerID: "s2", Agent: appmodel.AgentClaude, HistoryLoaded: true},
		})
	})
	t.Cleanup(func() {
		f.serv.Do(func() {
			f.srv.C.Update(1, func(th *model.Thread) { th.Busy, th.Queue = false, nil })
		})
	})
	a := f.window()
	var stale svcproto.State
	f.serv.Do(func() { stale = f.srv.C.SnapshotState() })
	stale.Threads[0].Draft = "已经发送的消息"
	f.win.Do(func() {
		a.SelectThread(a.ThreadWithID(1))
		a.RememberDraft("已经发送的消息")
		a.Send()
		var confirmed svcproto.State
		if err := a.Remote.Call("state", nil, &confirmed); err != nil {
			t.Error(err)
		} else if confirmed.Threads[0].Draft != "" {
			t.Errorf("the service kept a sent draft: %q", confirmed.Threads[0].Draft)
		}
		a.ApplyState(stale)
		a.SelectThread(a.ThreadWithID(2))
		a.SelectThread(a.ThreadWithID(1))
		if a.Draft() != "" {
			t.Errorf("a stale snapshot resurrected a sent draft: %q", a.Draft())
		}
		a.RememberDraft("对话一尚未发送")
		a.SelectThread(a.ThreadWithID(2))
		a.RememberDraft("对话二尚未发送")
		a.ApplyState(stale)
		a.SelectThread(a.ThreadWithID(1))
		if a.Draft() != "对话一尚未发送" {
			t.Errorf("lost the latest draft of conversation one: %q", a.Draft())
		}
		a.SelectThread(a.ThreadWithID(2))
		if a.Draft() != "对话二尚未发送" {
			t.Errorf("lost the latest draft of conversation two: %q", a.Draft())
		}
	})
	var confirmed svcproto.State
	if err := a.Remote.Call("state", nil, &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Threads[0].Draft != "对话一尚未发送" || confirmed.Threads[1].Draft != "对话二尚未发送" {
		t.Errorf("latest drafts were not persisted at the service: %+v", confirmed.Threads)
	}
}

func TestServicePublishesOnlyChangedMessages(t *testing.T) {
	p := &controller.SvcPeer{Sub: 7}
	th := model.Thread{ID: 7, Messages: []model.Message{{ID: 1, Role: "user", Content: "hi"}, {ID: 2, Role: "assistant", Content: "a"}}}
	first, ok := p.Diff(th)
	if !ok || !first.Reset || len(first.Changed) != 2 {
		t.Fatalf("first update %+v", first)
	}
	th.Messages = append([]model.Message(nil), th.Messages...)
	th.Messages[1].Content = "ab"
	second, ok := p.Diff(th)
	if !ok || second.Reset || len(second.Changed) != 1 || second.Changed[0].Content != "ab" || len(second.Order) != 2 {
		t.Fatalf("second update %+v", second)
	}
	if _, ok := p.Diff(th); ok {
		t.Fatal("an unchanged conversation was sent again")
	}
	th.Messages = th.Messages[:1]
	third, ok := p.Diff(th)
	if !ok || len(third.Order) != 1 || len(third.Changed) != 0 {
		t.Fatalf("a removed message was not sent: %+v", third)
	}
}

func TestServiceReturnsUnsentDraftsToTheWindow(t *testing.T) {
	f := newServiceFixture(t)
	w := f.window()
	var id int
	f.serv.Do(func() {
		c := f.srv.C
		id = c.NextID()
		c.SetThreads([]model.Thread{{ID: id, Title: "Lost", Agent: appmodel.AgentClaude, ServerID: "s1", HistoryLoaded: true}})
	})
	f.waitWindow("the conversation", func() bool { return w.ThreadWithID(id).ID != 0 })
	f.win.Do(func() { w.SelectThread(w.ThreadWithID(id)) })
	f.serv.Do(func() { f.srv.C.ReturnDraft(id, "try again", []string{"/tmp/a.png"}) })
	f.waitWindow("the returned draft", func() bool { return w.Draft() == "try again" && len(w.Attachments()) == 1 })
}
