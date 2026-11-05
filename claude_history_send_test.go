//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"miya/internal/remote"
)

func TestPhoneClaudeSendPreservesReadOnlyHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, cwd, "phone-history",
		`{"type":"user","uuid":"u1","message":{"content":"Earlier question"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"text","text":"Earlier answer"}]}}`)
	c := serverController(cwd, bin)
	c.serial = 100
	c.setReady(true)
	c.setThreads([]Thread{{ID: 7, Agent: agentClaude, ServerID: "phone-history", Resumed: true}})
	conn, ex := serveTestPhone(t, c)
	m := newMobileApp()
	r := remote.Request{Op: "subscribe", Thread: 7, Agent: agentClaude, ServerID: "phone-history", Project: cwd}
	conn.requests <- r
	if messages := phoneOf(t, m, conn.nextThread(t)); len(messages) != 2 {
		t.Fatalf("initial phone history: %+v", messages)
	}
	ex.do(func() {
		if c.threadWithID(7).HistoryLoaded || len(c.threadWithID(7).Messages) != 0 {
			t.Error("phone viewing should reproduce a controller with metadata only")
		}
	})
	r.Op, r.Text = "send", "Follow-up"
	conn.requests <- r
	for {
		d := conn.nextThread(t)
		messages := phoneOf(t, m, d)
		if len(messages) < 2 || messages[0].Content != "Earlier question" || messages[1].Content != "Earlier answer" {
			t.Fatalf("sending erased the phone's history: %+v", messages)
		}
		if !d.Thread.Busy && len(messages) >= 4 {
			users := userTexts(m.view.c.current())
			if len(users) != 2 || users[1] != "Follow-up" {
				t.Fatalf("follow-up was lost or duplicated: %q", users)
			}
			break
		}
	}
}

func TestClaudeHistoryLoadDrainsQueueAfterProjectSwitch(t *testing.T) {
	c, d := queueController(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	c.active, c.setActive = createSignal(0)
	c.serial = 100
	cwd := c.cwd
	writeClaudeSession(t, cwd, "s1", `{"type":"user","uuid":"u1","message":{"content":"Earlier question"}}`)
	c.update(1, func(th *Thread) { th.Busy, th.HistoryLoaded = false, false })
	c.loadClaudeHistory(c.threadWithID(1))
	if c.submit(SendRequest{ThreadID: 1, Text: "Queued follow-up"}) != 1 {
		t.Fatal("follow-up was not accepted while history was loading")
	}
	c.projectCache = map[string][]Thread{cwd: c.threads()}
	c.cwd = t.TempDir()
	c.setThreads(nil)
	c.waitClaudeHistory()
	d.drain()
	th := c.threadWithID(1)
	users := userTexts(th)
	if th.Loading || !th.HistoryLoaded || len(th.Queue) != 0 || len(users) != 2 || users[0] != "Earlier question" || users[1] != "Queued follow-up" {
		t.Fatalf("background history and queued send: %+v", th)
	}
}
