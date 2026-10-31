//go:build !windows

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	c := &chatController{cwd: dir, ctx: context.Background(), claudeBinary: claudeBin, projectCache: map[string][]Thread{}}
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
